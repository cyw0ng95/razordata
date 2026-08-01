# 用户指令记忆

## 条目

### SLT 调试工作流 — 使用 RAZOR_SLT_RANGE 分段运行

- Date: 2026-07-03
- Context: 在修复 SLT corpus 失败时，通过 RAZOR_SLT_RANGE 环境变量分段运行大型 .test 文件
- Category: 排错调试
- Instructions:
  - 使用 `RAZOR_SLT_RANGE=start:end` 分段运行大型 SLT 测试文件
  - 分段时 records 1-N 包含 setup，能正确测试查询逻辑
  - 仅运行范围 (如 708:710) 会跳过 setup，导致数据为空
  - 使用 `RAZOR_SLT_FAILFAST=1` 提前停止并输出第一个失败
  - 对于 1000+ 记录的 .test 文件，使用二分查找定位第一个失败记录
  - 运行命令：`cd tests/sqlcmp && go test -tags slt_corpus -run 'TestSLT_PerFile/xxx' -v ./slt/`
  - corpus 子模块需要先初始化：`git submodule update --init --recursive --depth 1`

### REQ001190: HashJoin.Close() 状态泄漏已修复

- Date: 2026-07-03
- Context: 修复 HashJoin.Close() 未重置 leftMatched/matchedRight 的问题
- Category: 排错调试
- Instructions:
  - HashJoin.Close() 必须重置 `j.leftMatched = nil; j.matchedRight = nil`
  - 否则 operator reuse 时会导致 outer-join 匹配跟踪错误
  - 测试文件：internal/SQB/OP/hashjoin_close_test.go

### REQ001192: select5.test 320 个多表连接失败 — 根因待查

- Date: 2026-07-03
- Context: 调试 select5.test 时发现多表连接返回 0 行
- Category: 排错调试
- Instructions:
  - 首个失败在可执行记录 #708 (L2408)，4 表连接
  - 手动测试同一查询返回正确结果 (1 行)，但 SLT runner 返回 0 行
  - 差异在于 SLT runner 加载了 64 个表的数据，手动测试只加载 4 个表
  - 增加额外表 (20 个) 的测试仍然通过，排除了表数量问题
  - 需要深入排查 planner 或 HashJoin 在多表场景下的行为
  - 调试测试文件：tests/sqlcmp/slt/req001192_*.go
