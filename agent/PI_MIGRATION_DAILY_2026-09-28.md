# Pi Coding Agent 移植日记（2026-09-28）

## 工作区基线

- Repo：`D:\13537\open-ai-canvas-canary`
- Branch / HEAD：`canary` / `645d1011 fix(agent): expose Pi compaction retention index`
- 本地比 `origin/canary` 多 9 个提交；工作树共 104 项：58 个已跟踪文件修改、45 个未跟踪文件、1 个删除。改动混有此前迁移工作，本日没有清理、暂存或覆盖它们。
- 当前 Docker 中可见 `canvas-canary-3000-*` 容器；本日只用了独立 `docker run --rm --network none` 测试容器，没有对运行实例执行操作，也没有部署或推送。

## 本日实现

1. Go `PiAgentSnapshot` 附带由服务端渠道/模型能力解析出的上下文窗和最大输出 token。Node Pi 模型不再填 1,000,000 / 32,768 假值；限额缺失时以 `FatalWorkerError` 失败关闭。
2. 新增内部 Pi 语义压缩开始、查询、提交协议。开始操作基于 Pi session、run、源 revision/leaf/digest 和压缩意图生成稳定 operation ID；重试复用同一 Go 计费任务，模型压缩失败使用既有服务端 fallback checkpoint。
3. Pi 的 `session_before_compact` hook 等待 Go 的结构化 checkpoint，并返回自定义 `CompactionResult`。Go commit 校验 summary/checkpoint、源 branch 身份和 session revision；事务内追加 Pi 压缩 entry，同时改写 Go canonical checkpoint 和事件。重复提交幂等；任一写入失败整笔回滚。
4. Pi 默认摘要不会在 Go 压缩失败后悄悄运行：hook 返回 cancel；Node 在下一次模型请求前检查并传播提交错误。
5. `645d1011` 已提交 Pi projector 的 `FirstKeptIndex` 保留字段。上述新压缩、模型限额与 Go route 测试代码仍在未提交工作树。

## 验证记录

- `node node_modules/typescript/bin/tsc -p tsconfig.json`（`agent/`）：通过。
- `node --test --test-timeout=20000 --test-force-exit dist/test/*.test.js`（`agent/`）：**64/64 通过**。
- 隔离测试容器：
  `docker run --rm --network none -v "D:\13537\open-ai-canvas-canary\backend:/workspace" -w /workspace -v canvas-gocache:/root/.cache/go-build -e CGO_ENABLED=1 open-ai-canvas-backend-test:sticky-tools go test ./internal/app ./internal/handler ./internal/service -count=1 -timeout 180s -run 'TestPi(ContextCompaction|AgentSnapshotCarriesGoResolvedModelLimits)|TestInternalAgentContextCompactionRoutesAreMounted'`
  结果：app、handler 通过；service 无测试文件。
- 单独复现 6 个旧 Go 测试的命令：
  `docker run --rm --network none -v "D:\13537\open-ai-canvas-canary\backend:/workspace" -w /workspace -v canvas-gocache:/root/.cache/go-build -e CGO_ENABLED=1 open-ai-canvas-backend-test:sticky-tools go test ./internal/app -count=1 -timeout 180s -run 'TestCloudAgentCompactionRequestsWhenTokenLineReached|TestCloudAgentCompactionFallsBackToBytesWithoutModelWindow|TestCloudAgentCancelledRunFinalizesInterruptedCompaction|TestCloudAgentCompactionCanRecoverContextFrameBudgetFailure|TestCloudAgentRunSurvivesTaskInputCompaction|TestCloudAgentLegacyRunSurvivesTaskInputCompaction'`
- 上述 6 项全部失败。其中 4 项 `TestCloudAgentCompaction*` 调用旧 Go 推进路径，要求旧式 `running` 状态/压缩暂停；Pi 首步合同此时保持 `queued/awaiting_first_step`。将这几项改成 Pi hook 和 Go operation 的行为验收，不要修改生产状态机迎合旧测试。
- `TestCloudAgentRunSurvivesTaskInputCompaction` 与 `TestCloudAgentLegacyRunSurvivesTaskInputCompaction` 将当前 holding 占位 task 手动设成已成功，并只填 task `result_json`，没有构造 Pi transcript / assistant final event；因此它们没有命中真实续聊数据形状。这两项不是通过删除断言解决：要建立真正旧版 `cloud_agent` 根任务夹具，验证历史结果导入；另建当前 Pi session continuation fixture，验证 assistant final entry 被带入下一轮。
- 本日没有运行全量 Go `internal/app` 套件；此前记录中的全量超时/基线失败仍然有效。本日也未验证 PostgreSQL、真实 provider、跨进程中断恢复、浏览器、SSE 断线重连。

## 追加：Pi 压缩操作重启恢复（本轮）

- 复审发现：Go 已创建的压缩 task 在 Node worker 重启后没有进入 Pi 快照。恢复 worker 可能把压缩 task 当普通 `PiModelStep` 处理，不能可靠地提交原压缩 entry。
- Go `PiAgentSnapshot` 现在回传待处理 compaction 的 operation ID、源 session revision/leaf 和原触发元数据；Node 对这个明确标识的操作只执行 GET 查询，不会再次 POST 创建计费任务。
- Runner 在处理普通恢复点和发起下一次模型步之前，恢复原 Pi v3 branch，通过锁定的 `AgentSession.compact()` 触发 inline hook，提交原 Go checkpoint 生成的 compaction entry。Go commit 成功后才继续发模型请求；operation/revision/leaf 不匹配时失败关闭。
- 新增真实 `AgentSession` 的 worker 重启恢复测试：断言恢复既有 Go operation、只提交一次，并且之后才有一个模型步；同时新增 bridge wire 和 Go snapshot 测试。
- 验证：`node node_modules/typescript/bin/tsc -p tsconfig.json` 通过；Node 全套 **67/67** 通过。一次性 `golang:1.25-alpine` 容器只读挂载 backend，定向 Go 快照测试通过。宿主机 Go 默认 `CGO_ENABLED=0`，所以 SQLite 测试不在宿主机直接运行；容器在退出时自动移除，没有接触现有 Canvas 容器。
- `npm test` 脚本本身因当前 shell 的 PATH 找不到 `tsc` 而未能启动；用 TypeScript 本地入口显式编译，再显式调用 Node test runner 完成了相同构建与全套测试。
- 本轮仍未验证 PostgreSQL、多 worker 跨进程故障注入、服务端真实压缩任务从 task 创建到提交的端到端费用不重复、浏览器或 SSE 续传。也没有部署或推送。

## 下阶段顺序

1. 先新增 Pi 压缩生命周期集成验收：开始→Go 任务→checkpoint result→Pi tree commit，覆盖取消、失败 fallback、重试、重复提交、revision 冲突与租约接管。
2. 将 4 个旧压缩单测拆为纯 Go 业务策略断言与真实 Pi/Go 编排断言；保留核心预算、轮次保留、fallback 和 terminal 语义。
3. 以真实旧 schema/根任务构造兼容 fixture，完成旧 run 导入/续聊与当前 Pi conversation 多轮续聊验收。
4. 继续迁入插话、视觉观察账本、停止原因与模型失败重试；再移除旧 Go Agent driver 及仅绑定旧实现的测试。
5. 完成 PostgreSQL、双 worker / 提交窗口崩溃注入、媒体/审批收尾、SSE 续传和真实浏览器验收。此阶段完成前不宣称移植完整。
