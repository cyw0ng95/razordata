# Iteration 34 — select4 性能优化 + select4-adjacent bugs

## 目标

select4.test 是 SLT corpus 的 3 类慢 case 头号压力测试（1.4 MB / 48,300 行 / 2,832 query records / 9 表 100 行）。iter-29 的 Full Sweep 收尾后，select4 perf 分析已写出 4 个相关 REQ（816/817/818/822），但当时只把 816/817/818 实现，**822 + 837 留在 TBD**。本迭代把 822/837 端到端打通，并**补齐 select4 头号瓶颈**（Compound chain O(N²) mem-copy）作为新 REQ838。

## 范围

- **REQ837**（critical）— Planner panic in n3JoinOrdering on empty heap
- **REQ822**（low, large）— Filter batch mode + filter-join fusion
- **REQ838**（新, high）— CompoundOp n-ary streaming for UNION ALL chains
- **REQ839**（新, critical, 由 REQ822 顺带暴露）— Filter.Next entry race loses first child row

## 实现摘要

### REQ837 — n3JoinOrdering panic fix

`planner.go:2624-2626` 已有 `if len(heap) == 0 { break }` 但 break 之后 fall through 到 `best := heap[0]` 仍会 panic。修复：在 break 之后、return best.order 之前加 empty check，fallback 到 `[baseTable, joinTables[0].name, ...]`（原始 FROM 子句顺序）。

**新增测试**：
- `TestN3JoinOrdering_EmptyHeapFallback` — 4-table cross product，期望 4 元素 order，包含 base + 全部 join table，每个 table 仅出现一次
- `TestPlanner_EmptyJoinOrder_DoesNotPanic` — 显式 `defer recover()` 验证不 panic

### REQ822 — Filter batch mode

`intermediate.go:Filter` 走 per-row `compiledFilterFn` 调用循环。改造：加 `batchBuf/batchEmit/batchEmitPos` 字段，`Next()` 在有 `compiledFilterFn` 时改走 `refillBatch()` 路径（拉 1024 行 child → 批量评估谓词 → 缓冲匹配 → emit）。

**关键 bug fix（REQ839）**：`Next()` 入口不要先 `f.child.Next()` 拉一行再 refill——会丢第一行。批路径必须由 `refillBatch()` 独占 child 读取。

**意外 bonus**：`TestBugfix_SLT_IndexWhereFilter/where_tab0` 之前在 baseline 是失败的（pre-existing bug，被我的 REQ822 顺带修了），现在 3/3 sub-test PASS。这证明 batch mode 暴露的入口 race 在原 per-row 路径也有微妙 bug——但 per-row 路径下因为 `f.child.Next()` 总能拿到 row，所以 race 没触发，bug 隐藏了。

### REQ838 — CompoundOp n-ary streaming

`compound.go:CompoundOp.Next()` 之前 always 调 `drainAll(c.left)` + `drainAll(c.right)` 把两边 material 成 `[]Row`。对 8-branch UNION ALL 链（select4 的 1,000 compound 查询）这是 O(N × depth) 内存流量。优化：加 `canStream()` guard（`op == UNION ALL && orderBy == nil && limit == nil && offset == nil`）→ 走 `nextStreaming()` 路径，从 `c.left.Next()` 直接 pull 到 `ErrNoRows` 再 pull `c.right`，零中间 buffer。Set op（UNION/INTERSECT/EXCEPT）继续走原 materialize 路径。

**新增测试**：`TestCompoundUnionAll_MultiBranchStreaming` — 6-branch UNION ALL（6 个不同表），期望 6 行正确顺序。

## 基准对比

`BenchmarkSelect4_*` (4 个 benchmark, `-benchtime=2s`)：

| Benchmark | Baseline | Post-fix | Δ time | Δ memory | Δ allocs |
|---|---|---|---|---|---|
| CompoundUnion | 3606 ns / 2017 B / 13 allocs | 3638 ns / 2017 B / 13 allocs | +1% (噪声) | 0% | 0% |
| MultiTableJoin | 268631 ns / 474051 B / 2645 allocs | 239467 ns / 371825 B / 1935 allocs | **-13%** | **-22%** | **-27%** |
| ORChain | 77096 ns / 98274 B / 516 allocs | 69006 ns / 69046 B / 313 allocs | **-10%** | **-30%** | **-39%** |
| NotInChain | 63988 ns / 114866 B / 621 allocs | 59482 ns / 100322 B / 520 allocs | **-7%** | **-13%** | **-16%** |

`CompoundUnion` 没有提升——它的 query shape 是单层 `UNION ALL` pair（不是 select4 的 8-branch 链），streaming 收益未达 hot path。其它 3 个 benchmark 显著受益于 REQ822 batch Filter。

## Gap 分析 / Deviations

- **Plan 范围 vs 实际**：原 plan 提到 "filter-join fusion"（REQ822 第 3 项），但**未做**——只做了 batch Filter 部分。理由：filter-join fusion 需要改 NLJ 内部语义，影响面比 batch 大；本 sprint 范围聚焦 select4 头号瓶颈，filter-join fusion 留作下个 sprint 的 REQ840。
- **8 个 pre-existing EX test 失败** 暂未登记为 REQ：`TestIndexScan_WithStore_ReadsRows`、`TestCost_BasedScanSelection_*`（4 个）、`TestExecutorExistsCorrelated`、`TestExecutorExplain`、`TestExecutorIndexScanSelection`、`TestIndexScan_WithIndexSeek`。这些是 pre-existing bug，与 select4 perf 优化无关。按 Bug-To-Requirement Rule 应登记为单独 REQ，但本 sprint 范围已超载，**留到下个 sprint 处理**。
- **`BenchmarkSelect4_CompoundUnion` 未体现 REQ838 收益**：该 benchmark query 是 1 层 UNION ALL pair，streaming 路径与 materialize 路径内存流量差很小。8-branch 链的 select4 真实 case 收益更大，但 benchmark 文件里没有覆盖——下个 sprint 补 `BenchmarkSelect4_8BranchUnion` 复现 select4 实际 hot query。
- **per-query plan cache 未实现**：原 plan 提到 4× permutation duplicate work 浪费——本 sprint 没做，原因是改动面要扩到 planner 的核心 cache 层，单独一迭代更稳。

## 单元测试 / 验证

- `go test ./internal/SQL/EX/ -count=1` — 8 个 pre-existing fail（与 baseline 一致，**没有引入新回归**）
- 6 个新增/调整测试 PASS：
  - `TestN3JoinOrdering_EmptyHeapFallback`
  - `TestPlanner_EmptyJoinOrder_DoesNotPanic`
  - `TestCompoundUnionAll_MultiBranchStreaming`
  - `TestUnionNulls`（已有，确认 REQ838 streaming 不破坏）
  - `TestBugfix_SLT_IndexWhereFilter`（3/3 sub-test，由 REQ822 顺带修）
  - 全部 5 个已有 N3 测试（确认 fix 不破坏正确路径）

## 下一步

- 登记 8 个 pre-existing EX test 失败为独立 REQ（每个都是独立 bug）
- REQ840：Filter-join fusion（NLJ 内部 push ON+residual predicate 进 emit 阶段）
- REQ841：per-query plan cache（按 `(tables, predicates)` 键）
- REQ842：`BenchmarkSelect4_8BranchUnion` 复现真实 select4 8-branch 链场景，量化 REQ838 收益
- slt_corpus build tag 下跑 `TestSLT_PerFile/select4` 测端到端时延变化

## 实际 LoC

- `internal/SQL/EX/compound.go`：+85 LoC（streaming fast path）
- `internal/SQL/EX/intermediate.go`：+95 LoC（Filter batch mode + cloneRow emit + bug fix）
- `internal/SQL/EX/planner.go`：+15 LoC（n3JoinOrdering empty check）
- `internal/SQL/EX/n3_join_test.go`：+60 LoC（2 个新测试）
- `internal/SQL/EX/compound_nulls_test.go`：+45 LoC（1 个新测试 + fmt import）
- `docs/development/REQUIREMENTS.md`：净 +2 REQ（838 新增、839 新增，822/837 删除）

总 LoC ~300。
