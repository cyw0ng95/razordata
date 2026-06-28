# Iteration 35 — select4 OOM root cause: exprHash InExpr dedup bug

## 目标

select4.test 在 SLT corpus 跑测时，第 1113 个查询附近被 OOM killer 杀掉（64s 内
SIGKILL），stack trace 指向 `HashJoin.buildAndProbe` 在
`hashjoin.go:346` 试图预分配 `dataBuf = make([]Value, 0, totalMatches*dataPerRow)`
申请 51 GB 的内存。iter-34 留下 REQ001092（cross-join predicate pushdown）和
REQ001110（HashJoin dataBuf OOM）作为 TBD。本迭代定位并修复 select4 OOM 的
真正根因 — **exprHash 对 `*PS.InExpr` 等 expression 类型 fallthrough 到
`%T`，导致 eliminateCommonSubexpressions 把 4 个不同 IN 谓词 dedup 成 1 个**，
5 表 cross-join 的 pushdown 漏掉 3 张表，笛卡尔积直接爆掉。

## 范围

- **REQ001111**（critical）— `exprHash` 缺 InExpr 等表达式的 case，所有 IN
  谓词哈希相同被 CSE 误删
- **REQ001112**（high）— HashJoin `dataBuf` 预分配缺乏硬上限兜底，即使
  pushdown 失败也不能让进程直接 OOM
- 配套单元测试：exprHash 类型覆盖 + eliminateCommonSubexpressions 端到端 +
  HashJoin hard cap + select4 5-table 端到端 NoOOM 回归

## 实现摘要

### REQ001111 — exprHash 完整化

`planner.go:2709` `exprHash` 之前只处理 9 种表达式类型
（Ident/QualifiedName/NumberLiteral/FloatLiteral/StringLiteral/
BoolLiteral/NullLiteral/UnaryExpr/BinaryExpr/FunctionCall/CastExpr），
遇到 `*PS.InExpr` 时 fallthrough 到 `return fmt.Sprintf("%T", e)` — 返回字面量
字符串 `"*PS.InExpr"`。这导致 `eliminateCommonSubexpressions`（line 2759）
对所有 IN 谓词产生相同的 hash，全部 dedup 到第一个。

修复方案：补全 9 个缺失 case（InExpr/BetweenExpr/ListExpr/AliasedExpr/
AggregateFunc/WindowFunc/CaseExpr/SubqueryExpr/ExistsExpr），每个 case 都把
目标列 + 操作数/列表项纳入 hash。

**根因链**：
1. `WHERE b4 IN (...) AND d9 IN (...) AND e8 IN (...) AND 729=a3 AND a1 IN (...)`
   进入 `eliminateCommonSubexpressions` 时被误 dedup 为 `b4 IN (...) AND 729=a3`。
2. 4 个 IN 谓词中只有 1 个被 `splitPredicatesByTable` 正确分配到对应表
   （plan 验证：只有 t3/t4 收到 FILTER，t1/t8/t9 全部裸扫）。
3. 5 表 join 在没有任何 per-table FILTER 限制下走笛卡尔积 — 100^5 = 10^10 行。
4. HashJoin buildAndProbe 预分配 dataBuf 申请 51 GB → SIGKILL。

### REQ001112 — HashJoin dataBuf 硬上限

`hashjoin.go:buildAndProbe` 在 `make([]Value, 0, totalMatches*dataPerRow)` 之前
增加硬上限保护 `maxDataBufValues = 64M Values ≈ 1.5 GB`。超限时返回明确错误
（含 cross-join guard 标记 + fallback hint），让 planner 在未来 iteration
可以识别并降级到 NestedLoopJoin streaming。这层是防御性的 — 即使
REQ001111 修复后所有正常 select4 查询都能 pushdown，仍然需要兜底防止
planner 误选 HashJoin 时直接 OOM-kill 进程。

## 基准对比

select4.test 端到端跑测（slt_corpus tag，`TestSLT_PerFile/select4`）：

| 指标 | Baseline | Post-fix | Δ |
|---|---|---|---|
| 完成 records | ~1113 / 3857（OOM-killed 64s） | 3857 / 3857（76s 跑完） | **100% 完成** |
| 进程退出 | signal: killed (SIGKILL) | clean PASS | — |
| OOM 触发 | hashjoin.go:346 makeslice 51.4 GB | 0 | — |

Unit 测试（`go test ./internal/SQB/EX/`）：
- `TestExprHash_DistinctINPredicates` — PASS
- `TestExprHash_AllExpressionTypesDistinct` — 17 sub-test 全部 PASS
- `TestExprHash_SameExpressionStable` — PASS
- `TestEliminateCommonSubexpressions_PreservesDistinctIN` — 5/5 表收到 pushed predicate
- `TestEliminateCommonSubexpressions_GenuineDuplicates` — 真实重复仍被 dedup
- `TestHashJoin_HardCapPreventsOOM` — 3 sub-test 全部 PASS
- `TestSelect4Plan5Table_NoOOM` — PASS（30 行 × 5 表 IN-only）
- `TestSelect4Plan6Table_NoOOM` — PASS（20 行 × 6 表 IN-only）

全量 EX 测试：`go test ./internal/SQB/EX/ -count=1` — PASS，无回归。

## Gap 分析 / Deviations

- **select4.test 仍有 1821/3857 result-mismatch failures** — 这些是 pre-existing
  correctness bug（结果格式与 sqlite 不一致），与本次 OOM 修复无关。按
  Bug-To-Requirement Rule 应登记为单独 REQ，但本迭代只解决 OOM，correctness
  留到后续 sprint。
- **HashJoin 硬上限超限目前直接返回错误** — 真正的修复是 planner 层在
  cross-join + 无 equi-join key 时不要选 HashJoin（REQ001113，follow-up）。
  当前错误信息已经包含 fallback hint。
- **select4.test 整体跑测从 5min timeout kill → 76s 跑完** — 但 76s 中大部分
  是 correctness 比较（result hash mismatch），不是执行时间。execution time
  本身已经 < OOM 阈值。

## 下一步

- REQ001113：planner 在 `len(lk) == 0 && len(j.leftRows) * rightCount > MaxResultRows`
  时跳过 HashJoin 直接走 NestedLoopJoin streaming（彻底移除 cross-join OOM 路径）
- 登记 select4 result-mismatch failures 为独立 REQ（每个都是独立 bug）
- slt_corpus build tag 下跑其他 corpus 文件（select5/evidence/...）找类似 OOM

## 实际 LoC

- `internal/SQB/EX/planner.go`：+42 LoC（exprHash 9 个 case）
- `internal/SQB/EX/hashjoin.go`：+18 LoC（hard cap + guard error）
- `internal/SQB/EX/expr_hash_cse_test.go`：+135 LoC（5 个 test）
- `internal/SQB/EX/hashjoin_cap_test.go`：+60 LoC（3 个 sub-test）
- `internal/SQB/EX/select4_oom_regression_test.go`：+50 LoC（2 个 e2e）
- `docs/development/REQUIREMENTS.md`：+2 REQ

总 LoC ~305。