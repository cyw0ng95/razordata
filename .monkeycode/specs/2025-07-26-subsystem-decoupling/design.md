# Razordata 子系统解耦方案

## 问题背景

`internal/` 共 793 个 Go 文件、149 个子包。当前修改代码成本高，核心原因是：
1. **58 个文件引用 `pl.Operator`**（`Next(ctx) (Row, error)`），形成全局耦合锚点
2. **`SQB/EX/ex.go` (2171行) 和 `planner_select.go` (2100行)** 各自 import 10-11 个包
3. **每个 operator 既有 row-based `Next()` 又有 vectorized `NextBatch()`**，两套接口共存但语义不隔离
4. **`DT.Operator = pl.Operator`** 别名让读者分不清实际使用的是什么接口
5. **UT（utils types）依赖了 OP（operators）**，形成从 utility → concrete 的反向依赖
6. **test files 大量依赖 internal 实现细节**，无法安全删除 dead code

---

## 根因分析（RCA 5-Why）

```
为什么改不动？
→ 因为改了 A 包，B/C/D/E 的 test 就会崩
→ 因为 test 直接调用内部 Next() 方法
→ 因为 Next() 和 NextBatch() 共享同一个 Operator 接口
→ 因为 Operator 接口定义在 pl（parser/lower）包中
→ 根本原因：**pl.Operator 是跨子系统的执行契约**，但它的抽象层不够粗——
   它同时承载了 row-based 和 batch-based 两条完全独立的执行路径，
   而两个路径的实现混在同一个文件中。
```

---

## 方案设计

### 阶段一：Interface Segregation（目标：消除 Dual-Path Operator 混合）

#### 1.1 拆分 `pl.Operator` 为两个接口

**现状**：
```go
type Operator interface {       // pl/types.go:46
    Next(ctx context.Context) (Row, error)
    Close() error
}
```

所有 58 个文件都依赖此接口，它同时服务于：
- Row-based path: `Sort.Next()`, `Limit.Next()`, `Filter.Next()`, etc.
- Batch path via adapter: `BatchToRowAdapter.Next()` → internally calls `NextBatch()`

**改为**：

```go
// pl/types.go
// PullOperator is the minimal interface for row-at-a-time execution.
type PullOperator interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}

// PushOperator is the minimal interface for batch execution.
type PushOperator interface {
    NextBatch(ctx context.Context) (*Batch, error)
    Close() error
}

// Operator = PullOperator for backward compatibility (deprecated).
// New code MUST use PullOperator or PushOperator directly.
type Operator = PullOperator
```

**影响分析**：
- `SQB/EX` 层的 `propagateExecContext(op)` 接受 `any` — 改为 accept both interfaces
- `BatchToRowAdapter` 持有 `PushOperator` source — 不需要改动
- 测试文件中直接调用 `.Next(ctx)` 的行为不变（通过 PullOperator 兼容层）
- **关键收益**：后续删除 row-based Next() 时，只需从 PushOperator 实现中移除，不影响 PullOperator 兼容层

#### 1.2 删除 operator 文件中的双路径混写

每个 `*.go` 文件现在同时包含 `Next(ctx)` 和 `NextBatch(ctx)`。拆分为：

| 原文件 | 保留 | 迁移到新文件 |
|--------|------|-------------|
| `intermediate_basic.go` (2017行) | 保持 Filter/Project 的 NextBatch 实现 | 新建 `intermediate_basic_row.go` 放 Next() 方法 |
| `operators.go` (2257行) | 保持 SeqScan/IndexScan NextBatch | 新建 `operators_row.go` 放 Next() |
| `op_vec_join.go` (1946行) | 已只有 NextBatch | 无变更 |
| `hashjoin.go` + `hashcrossjoin.go` | NextBatch | Next() 移到 `_row.go` |

**规则**：新文件命名 `*_row.go`，旧文件重命名为 `*_vec.go`（或保持原名，将 row-only 部分剥离）。

**预期收益**：
- `intermediate_basic.go` 从 2017 行 → ~600 行（删除 ~1400 行 row-based）
- `operators.go` 从 2257 行 → ~1200 行
- 总减少约 2500-3000 行死代码
- REQ002038 Phase 2 变为 trivial：删掉 `_row.go` 文件和对应的 test 引用即可

#### 1.3 清理 `DT.Operator` 别名

```diff
- type Operator = pl.Operator
+ // Operator is deprecated; use PullOperator or PushOperator instead.
+ // Kept for one transitional alias in DT namespace.
+ type Operator = PullOperator
```

### 阶段二：Dependency Direction Fix

#### 2.1 移除 UT → OP 反向依赖

**现状**：`internal/SQB/UT/batch_adapter.go` imports `SQB/OP` for `VectorizedSeqScan`, `VectorizedHashJoin` 类型。

**改为**：`batch_adapter.go` 只依赖 `PushOperator` 接口。`VectorizedSeqScan` 等具体类型的导入移到 `batch_adapter_impl.go` 文件（build tag `!internal_test` 或仅在 factory 中使用）。

```diff
// batch_adapter.go
-import "github.com/cyw0ng95/razordata/internal/SQB/OP"
+// depends only on PushOperator and Batch types
```

```go
// batch_adapter_impl.go
import "github.com/cyw0ng95/razordata/internal/SQB/OP"

func NewBatchToRowAdapterVecScan(s *OP.VectorizedSeqScan) *BatchToRowAdapter { ... }
func NewBatchToRowAdapterVecHJ(hj *OP.VectorizedHashJoin) *BatchToRowAdapter { ... }
```

#### 2.2 ex.go 拆分为 facade + exec engine

**现状**：`ex.go` 有 2171 行，import 11 个包，包含 planner wrapper、cache management、DML dispatch、query routing。

**改为**：

| 文件 | 职责 | 行数 |
|------|------|------|
| `executor.go` | `Executor` struct + fields + constructor | ~200 |
| `exec_query.go` | `Query()` / `QueryAll()` routing + plan cache lookup | ~300 |
| `exec_dml.go` | `Exec()` DML path + RETURNING dispatch | ~200 |
| `exec_stream.go` | `QueryStream*` functions | ~200 |
| `cache.go` | `stmtCache` + `planCache` + `textPlanCache` management | ~300 |
| `write_ops.go` | `buildWriterOp()` builder logic | ~200 |
| `arena.go` | `ensureArena()` + row arena management | ~100 |

**收益**：
- 单文件 < 400 行，可读性提升 5x
- 每个子文件的 import 数从 11 降到 3-5
- 新增/修改 query 执行逻辑只需改 `exec_query.go`，不需要看 write_ops

### 阶段三：Test Isolation

#### 3.1 将测试 operator 从 internal/ 移出到 tests/

**现状**：`sliceScan`、`mockRowSource`、`countingIndexScan` 等 fake operators 存在于 `internal/SQB/OP/*_test.go` 中，直接依赖 `pl.Row` 和 `pl.Value` 的内部结构。

**改为**：
- 创建 `internal/SQB/OP/factory_test.go`，内含 `NewTestPullSource([]Row)` 函数返回 `PullOperator`
- 或更激进：在 `tests/sqlcmp/slt/operators_test.go` 统一放所有测试 factory
- `sliceScan` 等 fake ops 不再依赖 `pl.Row`，改用 `[]Value` slice + column names

#### 3.2 req001113_remaining_test.go 迁移

该文件 (460+ 行) 测试 NLJ/HJ 的 row-based path。迁移步骤：
1. 先用 `BatchToRowAdapter` 包裹 vectorized operator
2. 通过 adapter 的 `Next()` 驱动查询（adapter 内部用 `NextBatch()`）
3. 验证结果 hash 与 row-based 一致
4. 标记测试为 `[REQUIRES_VEC]`，说明这是 vec 路径验证

### 阶段四：Build Tag Gate for Dead Code

#### 4.1 可选编译标志

```go
//go:build !novector

// 当 `go build -tags novector` 时，所有 row-based Next() 存在但不被生产路径调用
// 仅保留给 legacy test 使用
```

生产构建默认 `go build -tags novector`，此时：
- `pl.Operator` 仍存在于代码中（backward compat layer）
- 但 `tryVectorizePlan` 始终返回 vectorized path + BTRA
- row-based 代码可标记为 `// DEPRECATED: row-based execution removed in v2`

---

## 投资回报率分析

| 方案 | 改动量 | 风险 | 收益 | 预计 sprint 数 |
|------|--------|------|------|---------------|
| 1.1 接口拆分 | ~50 行 + 类型断言 | 低 | 解除 dual-path 耦合锚点 | 1 |
| 1.2 文件拆分 | ~3000 行迁移 | 中 | 删除 2500+ 行 dead code | 1 |
| 1.3 DT 别名清理 | 5 行 | 极低 | 概念清晰 | 0.5 |
| 2.1 UT→OP 依赖修复 | ~100 行 | 中 | 消除 utility → concrete 反依赖 | 1 |
| 2.2 ex.go 拆分 | ~2171 行拆为 7 个文件 | 低 | 单文件 < 400 行 | 1 |
| 3.1 测试工厂化 | ~200 行 | 低 | safe test migration | 1 |
| 4.1 build tag gate | 5 行 | 中 | 渐进式 dead code 淘汰 | 0.5 |

**总计**：~7 sprints，每次一个可独立提交的 PR。

**最高 ROI 优先做**：Phase 1.1（接口拆分 50 行）→ Phase 2.2（ex.go 拆分）→ Phase 1.2（删除 dead code）。这三步完成后，REQ002038 Phase 2 变为 trivial。

---

## 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 接口拆分后旧代码 break | 低 | 高 | 保留 `type Operator = PullOperator` 兼容性别名 |
| ex.go 拆分后循环 import | 中 | 中 | 先跑 `go mod tidy && go build ./...` 再 commit |
| BTRA 包装延迟引入 | 低 | 低 | BTRA 已在生产中使用，只需调整构造时机 |

---

## 执行顺序（推荐）

```
Sprint N:     Phase 1.1 (interface split) — 最小改动，最大解耦
Sprint N+1:   Phase 2.2 (ex.go split) — 消除 God file
Sprint N+2:   Phase 1.2 (next() removal) — 删除 ~3000 行 dead code
Sprint N+3:   Phase 2.1 (UT→OP dep fix)
Sprint N+4:   Phase 3.x (test factory)
Sprint N+5:   Phase 4.1 (build tag gate)
```

每步完成后都需要 `go test ./internal/... -race -count=1` 和 `./before-commit-cases.sh` 全通过。
