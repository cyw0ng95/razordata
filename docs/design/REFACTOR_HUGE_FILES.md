# 巨型文件重构方案

> **分析时间**: 2026-07-06
> **总生产代码**: 85,143 行 / 14 个 >1000 行文件
> **Top 5 巨无霸**: 16,449 行 (占 19.3%)
> **REQs**: REQ001257-001261 (见 REQUIREMENTS.md TBD)

## Subsystem-Cluster 规则约束（不可违反）

**ARCH.md 定义的 Cluster 边界是硬约束。** 所有拆分为 intra-Cluster 文件切分，不跨越 Cluster。

| 文件 | 当前 Cluster | 拆分后 Cluster | 说明 |
|---|---|---|---|
| `planner.go` | SQB/EX | SQB/EX | ARCH: "planner.go still in EX pending SQB finalization" — 迁移到 SQF/PL 是独立架构变更 |
| `eval.go` | SQB/EV | SQB/EV | — |
| `writers.go` | SQB/WT | SQB/WT | — |
| `intermediate.go` | SQB/OP | SQB/OP | — |
| `ex.go` | SQB/EX | SQB/EX | — |

跨 Cluster 迁移（如 planner.go → SQF/PL）不在本重构范围内，后续单独做。

## 现状诊断

### 🚨 巨型文件排名

| 文件 | 行数 | 函数数 | Cluster | 问题诊断 |
|---|---|---|---|---|
| `SQB/EX/planner.go` | **6,816** | 189 | SQB/EX | 规划/代价/连接排序/谓词/索引/视图/CTE 混合 |
| `SQB/EV/eval.go` | **2,940** | ~100 | SQB/EV | 核心求值 + 30+ 标量函数混在一起 |
| `SQB/WT/writers.go` | **2,612** | 135 | SQB/WT | DML + DDL + Admin + 冲突处理混一个文件 |
| `SQB/OP/intermediate.go` | **2,159** | 85 | SQB/OP | FilterProject/Filter/Project/Sort/Limit/Offset 全堆 |
| `SQB/EX/ex.go` | **1,922** | 70 | SQB/EX | Executor 核心 + streamIterator 混在一起 |

---

## 重构计划

### Phase 1: REQ001257 — `writers.go` 拆分（2,612 → 4 个文件）

| 新文件 | 行数预估 | 职责 | REQ |
|---|---|---|---|
| `writers_dml.go` | ~1,000 | Insert/Update/Delete, buildInsertRowFromSelect, evalReturning | REQ001257 |
| `writers_ddl.go` | ~800 | CreateTable/DropTable/CreateIndex/DropIndex, buildUniqueConstraints/buildFKConstraints/buildCheckConstraints/buildGeneratedColumns, registerTableSchema | REQ001257 |
| `writers_admin.go` | ~500 | Pragma (含 loadForeignKey* 方法), Attach/Detach/Truncate/Reindex/Explain, fireInsert/Update/DeleteTriggers, refreshMatViewData/executeRefreshMatViewSQL | REQ001257 |
| `conflict.go` | ~300 | applyConflictUpdate, conflictKey 等冲突处理 | REQ001257 |

### Phase 2: REQ001258 — `eval.go` 拆分（2,940 → 3 个文件）

| 新文件 | 行数预估 | 职责 | REQ |
|---|---|---|---|
| `eval.go` (保留核心) | ~1,500 | Eval/EvalValue/evalFallbackEvalValue, evalBinaryValue/evalUnaryValue/evalBinaryShortCircuit, evalBetween/evalCast/evalCase/evalAggregate, evalExists/evalScalarSubquery/evalInterval, EvalInValue/EvalInHashValue/EvalForTest | REQ001258 |
| `scalar_funcs.go` | ~1,200 | evalAbs/evalRound/evalSubstr/evalConcatWS/evalFormat/evalTrim/evalLtrim/evalRtrim/evalReplace/evalQuote/evalTypeof/evalOctetLength/evalUnicode/evalSqliteVersion/evalSqliteSourceID/evalIIF/evalInstr/evalSign/evalMaxScalar/evalMinScalar/evalRandom/evalRandomBlob/evalZeroblob/evalHex/evalUnhex/evalGlob/evalSoundex/evalUnistr/evalLikelihood/evalLikely/evalUnlikely | REQ001258 |
| `helpers.go` | ~300 | toInt64/compare/NumericArithValue/bandValue/borValue/modValue/divValue/bitandValue/bitorValue/bitxorValue/lshiftValue/rshiftValue/ConcatValue/likeValue/GlobValue/isValue/normalizeInt/valueFromAnyWrap/toFloat64/MatchLike/equalValue/globMatch/parseHex*/* | REQ001258 |

### Phase 3: REQ001259 — `intermediate.go` 拆分（2,159 → 3 个文件）

| 新文件 | 行数预估 | 职责 | REQ |
|---|---|---|---|
| `intermediate_basic.go` | ~900 | FilterProject, Filter, Project (+ replaceComparisonLiterals/findColIndexInRow/compileInExpr/compileBinary/compileColRef/compileBinaryArith/helpers) | REQ001259 |
| `intermediate_sort.go` | ~800 | Sort (parallelSort/cmpKeys/refillBatch) | REQ001259 |
| `intermediate_limit.go` | ~400 | Limit, Offset | REQ001259 |

### Phase 4: REQ001261 — `ex.go` 拆分（1,922 → 2 个文件）

| 新文件 | 行数预估 | 职责 | REQ |
|---|---|---|---|
| `ex.go` (保留核心) | ~1,300 | Executor struct/setters, NewExecutor*, Exec/Query/QueryAll/QueryStream/Explain, buildWriterOp, planWithCache, stmt/plan cache, ExtractParamTypes, walkExpr/walkPlaceholder/walkExprTypes, register helpers, propagate helpers, valueFromString/valueFromAny/valueFromAnySlice/valueSliceToAny/newValue constructors | REQ001261 |
| `stream.go` | ~500 | streamIterator, Cols()/Types()/Next()/Close() | REQ001261 |

### Phase 5: REQ001260 — `planner.go` 拆分（6,816 → 9 个文件）

| 新文件 | 行数预估 | 职责 | REQ |
|---|---|---|---|
| `planner.go` (保留核心) | ~600 | Planner struct, Setters/Getters, Plan()/ParseAndPlan(), planSubStmt(), planInsert/Update/Delete/CreateTable/DropTable/CreateIndex/DropIndex/Explain/Pragma/Analyze/Vacuum/With, ExecuteSubquery* | REQ001260 |
| `planner_select.go` | ~1,200 | planSelect, planSelectScan/NoFrom/Subquery/SqliteMaster, planAggregation/Ordering/LimitOffset, planSelectJoins, decomposeForIndexScan | REQ001260 |
| `cost.go` | ~1,200 | CostParams, estimateCost*/MemoryPressure/RowCount, estimateJoinCost, joinPredSel, estimateInListSelectivity, estimateSelectivity/SelectivityWithStats/EqSelectivity/RangeSelectivity, selectivity helpers | REQ001260 |
| `join_order.go` | ~1,400 | n3JoinOrdering/MultiStart, exhaustiveJoinOrder, groupBushyJoins, isConnectedGraph, estimateJoinOrderCost, findPredicatesForSet/Pair, joinResultRows | REQ001260 |
| `predicate.go` | ~800 | splitAnd/splitPredicatesByTable/canPushDown, extractEqualityAnySide/SingleEquality, extractInListValues, extractOrChainEquality, extractColumnLiteral/ColumnLiteralExpr, tryApplyPointLookup, equiJoinKey/extractEquiJoinKeys/extractSingleOnEquiKey, isColumnLiteralPair, colNameFromExpr, allInSet | REQ001260 |
| `index_plan.go` | ~800 | NewIndexOrSeqScan, pickCheaperScan, tryBitmapHeapScan/tryIndexOnlyScan, selectIndex, indexedColumn*/LikePrefix, rangeBounds/rangeFromOp, columnName, encodeIndexValue, extractLikePrefix, limitInt64, tablePK/indexColumns, pkOrderMatches, propagateLimitToNLJ | REQ001260 |
| `view_subquery.go` | ~800 | resolveView, isViewMergeable/viewColsAreSimpleRefs/mergeViewIntoOuter, resolveAliasesAndFold, decorrelateExists/buildCorrelationFunc, planRecursiveCTE + helpers, tryFlattenSubqueryFrom/isSubqueryFlattenable/pushPredicateIntoSubquery, andExpr/exprReferencesTable/colExprReferencesTable/canonicalColRef/colRefFromCanonical, inferTransitiveEqualities/mergeWhereIntoSubquery/referencesOnlySubqueryCols/collectIdentsFromExpr | REQ001260 |
| `fold_cse.go` | ~500 | foldConstants, eliminateCommonSubexpressions, isConstantExpr, valueToLiteral, isSameColumn/isZero/isOne/isTrue/isFalse | REQ001260 |
| `expr_util.go` | ~400 | cloneExpr, resolveAliases, buildSelectAliasMap, walkExpr/walkSubqueryColRefs, exprHash, collectColRefs/rebuildAnd, collectReferencedColNames/Tables/QualifiedFromSubquery, joinOnReferences/log2ish/collectReferencedColumns/ColsFromExpr/collectTablesFromExpr, walkExprForTables/extractTablesFromExpr/findTableForColumn, resolveTableForColumn/findTableInSchemas/splitAlphaNum/splitSelectCols, projectColumns, operatorProducesSorted | REQ001260 |

**总计: 9 个文件, 最大 ~1,400 行 (原始 6,816 行的 20.5%)**

---

## 执行策略

### 顺序

| 顺序 | REQ | 理由 |
|---|---|---|
| 1 | REQ001257 | writers.go 职责边界最清晰，天然按 DML/DDL/Admin 分 |
| 2 | REQ001258 | eval.go 核心 vs 标量函数接口天然分离 |
| 3 | REQ001259 | intermediate.go 操作符之间依赖少，拆分风险最低 |
| 4 | REQ001261 | ex.go 拆分最简单 |
| 5 | REQ001260 | **最大风险最高回报** — planner.go 最复杂，但拆完收益最大 |

### 每个 Phase 的验收标准

1. `go build ./...` — 编译通过
2. `go test ./... -race -count=1` — 全绿
3. `go vet ./...` — 零警告
4. `gofmt -s -l .` — 零 drift
5. `git add && git commit -m "refactor: REQ001XXX - split X.go"`

### 拆分规则（不可违反）

- **intra-Cluster only**: 所有文件保留在同一个 Cluster 内，不跨 Cluster 移动
- **same package**: 新文件与原文件使用相同的 `package` 名 — 函数互相可见，零 import 变更
- **zero interface change**: 所有公开方法签名不变，函数仅改变文件位置
- **no function signature changes**: 拆分前函数在文件 A 调用文件 A 的其他函数 → 拆分后仍在文件 A 内
- **测试文件不动**: 拆分只动生产代码；`*_test.go` 不需要修改（除非测试内部引用了内部函数 — 这种情况极少，因为 Go 测试文件通常 import 的是公开类型）
- **一次一 Phase**: 绝不跨 Phase 合并提交

### 预期收益

| 指标 | 当前 | 重构后 |
|---|---|---|
| 最大文件行数 | 6,816 | ~1,400 |
| >2000 行文件数 | 4 | 0 |
| >1000 行文件数 | 14 | 3 (planner_select.go, cost.go, join_order.go) |
| 单文件职责数 | 6-7 混合职责 | 单职责 |
| 代码审查效率 | 低 | 高 — 一个 diff 只涉及一个职责领域 |

### 未涉及项（不在本重构范围内）

- **planner.go 迁移到 SQF/PL**: 架构决策，独立任务。ARCH 标注为 "still in EX pending SQB finalization"。
- **长函数（>100 行）进一步拆分**: 文件拆分后，长函数会自然暴露；后续按需处理。
- **test 文件拆分**: 本次只改生产代码。
- **import 重构**: 不改变 import 结构。

---

## 下一步

1. 确认 Phase 顺序
2. 从 Phase 1 (REQ001257: `writers.go` 拆分) 开始
3. 每个 Phase 完成后跑 `go build ./... && go test ./... -race -count=1` 验证
4. 完成后更新 ROADMAP.md 和迭代追踪文档
