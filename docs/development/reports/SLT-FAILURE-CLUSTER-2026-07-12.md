# SLT 失败聚类报告 — 2026-07-12

基于 `tests/sqlcmp/corpus/test`（622 文件）跑 SLT_PerFile runner 的 early-panic 范围。
注意：完整 622 文件跑会因 `index/between/1/slt_good_0.test` 在 use-after-free 触发 Go 致命错
中断运行 —— 这是 **真实的 bug 信号**，本身需要在下方 REQ 提案里被记录。

## 样本与覆盖

| 范围 | 文件数 | 测试记录 | PASS | FAIL | SKIP | 通过率 |
|---|---|---|---|---|---|---|
| `evidence/` 全部 12 文件 | 12 | 580 | 303 | 20 | 105 | 52.2% |
| `random/aggregates/` 全部 130 文件 | 130 | 2,940,825 | 1,255,738 | 36,586 | 510,229 | 42.7% |
| `select1.test` | 1 | 1031 | 1031 | 0 | 0 | 100% |
| `select2.test` | 1 | 1031 | 1031 | 0 | 0 | 100% |
| `select3.test` | 1 | 3351 | 3351 | 0 | 0 | 100% |
| `select4.test` | - | - | - | - | - | **panic: nil pointer in `SQB/EX/join_order.go:69` (`hasIndexOnTable`)** |
| `select5.test` | - | - | - | - | - | **panic: 同上** |
| `index/between/1/slt_good_0.test` | - | - | - | - | - | **fatal: `found pointer to free object`（use-after-free）** |

select1/2/3 全通过说明核心 SELECT 流程健全；瓶颈全部集中在：

1. **JOIN/expression 综合 case**（random/aggregates 占 fail 总量 99%+）
2. **大 JOIN 基准**（select4/5 panic）
3. **少数 evidence 用例**（IN/NOT IN NULL 三值语义）

---

## 失败聚类（按出现频次降序）

### 聚类 A：投影列顺序错位（约占 random/aggregates fails 的 70%）

代表性 got/want（来自 `random/aggregates`）：

```
got [81  | 253], want [253 | 81]
got [64  | NULL], want [NULL | 64]
got [-5395 | 3], want [3 | -5395]
got [38 | 3], want [3 | 38]
got [16 | 1], want [1 | 16]
got [78 | 74], want [74 | 78]
```

**几乎所有 random/aggregates 失败都是两列/多列的输出顺序被翻转了**。值本身正确，没有 missing/extra。

**怀疑点**：
- `SQB/EX/planner_select.go`：列引用解析时序翻转（聚合/Aggregate 内子查询 vs 外层 SELECT 的 col index 重排）
- `SQB/OP/project.go`：Project 在多列 DISTINCT/AGG 下没有保持稳定顺序
- `SQB/OP/hashagg.go`：HashAggregate 在 group-by 后输出 key 顺序与 GROUP BY 子句指定顺序未对齐

### 聚类 B：`<expr> IN (SELECT ...)` NULL 三值语义错位

evidence/in1.test 全部 12 条 fail 都属此类：

```
SELECT 4 IN (SELECT * FROM t4n)         -> got [0],    want [1]
SELECT 2 NOT IN (SELECT * FROM t4n)     -> got [NULL], want [0]
SELECT null IN (SELECT * FROM t6)       -> got [0],    want [NULL]
SELECT null NOT IN (SELECT * FROM t6)   -> got [1],    want [NULL]
```

**SQL 三值逻辑规则**：
- `x IN (subquery)` 当 subquery 含 NULL → 结果必须是 NULL（不能是 0/1）；NOT IN 同理
- NULL 出现时，必须传播 NULL 而非退化为 false

**怀疑点**：`SQB/EX/eval.go` 的 `InSubquery` / `NotInSubquery` 实现没正确跑过 NULL 检测路径。`evidence/in1.test` 是 SLT 官方 NULL 三值逻辑测试套 —— 这条通了，42.7% → 60% 是比较容易的提升。

### 聚类 C：`count(*) FROM t1 WHERE x=<未匹配值>` 返回行数异常

evidence/in1.test 第 14-19 行（≈7 条）：

```
SELECT count(*) FROM t1 WHERE x=3         -> got [18], want [3]
SELECT count(*) FROM t1 WHERE x=1         -> got [8],  want [1]
SELECT count(*) FROM t1 WHERE x=4         -> got [26], want [3]
SELECT count(*) FROM t1 WHERE x=5         -> got [26], want [3]
SELECT count(*) FROM t1 WHERE y='unknown' -> got [16], want [1]
```

**值都不是 0 / 1**，而是其他奇怪值。**这看起来是 count 星使用了 filter column 索引错位** —— `WHERE x=3` 时 select 错把 `col1` 或别的数字传出去，但只按一列 count 后 count 出的是其他列的行数。

结合聚类 A 的"列顺序翻转"，**这很可能是同一个根因 —— 投影列顺序 / column index 错位** 影响了两类查询。

### 聚类 D：select4/5 panic — Planner nil deref

```
panic: runtime error: invalid memory address or nil pointer dereference
  internal/SQB/EX/join_order.go:69 hasIndexOnTable
  internal/SQB/EX/join_order.go:13 plannerAsStatsProvider.HasIndex
  internal/SQO/JN/n3.go:82 N3
  internal/SQO/JN/multi_start.go:47 MultiStart
```

`(*Planner).hasIndexOnTable` 拿到 nil 表描述符，可能原因：
- select4/5 的 schema 加载/初始化与 N3 调用之间存在 race
- `StatsCatalog` 返回 nil table 但 N3 没防御

**这是会爆 Go runtime 的硬 bug**，必须修才能让 select4/5 进入正常 pass/fail 计数。

### 聚类 E：`index/between/1/slt_good_0.test` use-after-free

```
fatal error: found pointer to free object
runtime: marked free object in span 0x7f36c3ac8c40, elemsize=16 freeindex=0
```

**REPRO 时跟文件正相关**，但 commit log 显示 REQ001511 修过类似 stream lifetime 问题，可能没修全。这是一个存储层指针失效 — `SQB/EX/stream.go` 嫌疑大。

### 聚类 F：`CAST(... AS INTEGER/REAL)` 与一元负号表达式

random/aggregates 中关键字计数（粗略，含重叠）：

| 关键字 | 失败数 |
|---|---|
| `CAST ( NULL AS INTEGER )` | 2263 |
| `COUNT(` | 9477 |
| `DISTINCT` | 8174 |
| `+ `-` 一元负号后表达式 | 24839 |
| `NOT IN` | 3189 |
| `IS NULL` | 2723 |
| `IS NOT NULL` | 2713 |

很多跟聚类 A 重叠（这些 SQL 都会产生多列输出）。

---

## 优先级判断

| 聚类 | 影响广度 | 修复难度 | 推荐优先级 |
|---|---|---|---|
| **D. select4/5 Planner nil** | 阻塞 select4/5 全跑（共 ≈ 30K 测试用例） | 小，nil-check + 测试 | **P0 立刻** |
| **E. use-after-free** | 全 runner 崩溃阻断所有 in1+ 后面的文件 | 中，需 race detector 定位 | **P0 立刻** |
| **A. 投影列顺序翻转** | 影响 70%+ random/aggregates 失败 | 中，要定位 column index 重排点 | **P1** |
| **B. IN/NOT IN NULL** | 阻塞 evidence/in1 全套 | 小，三值逻辑固定 | **P1** |
| **C. count(*)+WHERE 索引错** | 7 条 + 跟 A 同根 | 中，跟 A 同根可一并 | **P1 并入 A** |
| **F. CAST + 一元负** | 已被 A 覆盖一部分 | 大，表达式求值路径广 | **P2 跟 A 一起评估** |

---

## 推荐的下一批 REQ（5-7 条）

1. **REQ001550** — `SQB/EX/join_order.go:69` `hasIndexOnTable` nil 防 + select4/5 启动 panic 复现测试。
2. **REQ001551** — `index/between/1/slt_good_0.test` use-after-free 复现，stream lifetime 全审计（REQ001511 后续清理）。
3. **REQ001552** — `SQB/EX/eval.go` `InSubquery`/`NotInSubquery` 三值逻辑修正（含 NULL 传播）。
4. **REQ001553** — `SQB/EX/planner_select.go` 投影列顺序 bug：定位 column index 重排点（最可能是 DISTINCT/AGG/CROSS JOIN 一类多列组合）。
5. **REQ001554** — `count(*)+WHERE` 索引错位（很可能是 553 的同根 bug，独立 REQ 便于追踪）。
6. **REQ001555** —（如有余力）补一个 `make bench-slt` 一键跑 622 文件 + 输出失败聚类 JSON 的 CI 脚本，把这次分析流程固化下来。

---

## 跑分器本身的可改进点

- **runner.go:88 的 `dur=223ns` 是测量 bug**：aggregate 一个文件按 23K 测试记录不可能 223ns 跑完。纳秒是 timer 解析成 ms 时漏了乘。修一下能得到真实的 per-file 时延。
- **first-failure 截前 5 一致，对聚类不够**：建议加 `--max-failures=N` flag。
- **panic 不计入 fail**：select4/5/index-between 那 3 个文件因为 Go 致命错没计入通过率统计，但应该记为"阻塞"。
