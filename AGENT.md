# Pi Agent 迁移交接（2026-09-26）

本文件记录 `canary` 分支上的迁移进度，供接手的 Agent 继续开发。完整工作约定见根目录 `AGENTS.md`。用户批准的目标是：独立 Node 22.19+ 容器运行固定版本 `@earendil-works/pi-agent-core@0.87.1`，Go 保留鉴权、画布写入、模型与计费、持久化及原 `/api/agent`/SSE 合同。**本次不部署；当前实现是未完成的迁移切片，不可开启 Pi 流量。**

## 已落地的代码

- `agent/`：Pi 依赖锁、模型结果到 Pi 事件的适配、母类型工具披露、Node 领取与租约循环、基础消息检查点和崩溃后工具回执补齐；`agent/Dockerfile` 提供独立镜像。
- Go `backend/internal/app/cloud_agent_pi_bridge.go` 与 `backend/internal/handler/internal_agent.go`：仅内部 Bearer Token 可访问的领取、快照、续租、模型步骤、Pi 消息、工具批次、工具推进和无工具收尾接口。每次按用户与租约读取运行；工具继续经 Go 既有权限、参数、审批和画布写入路径。
- `cloud_agent_executions` 新增 `engine`/租约字段（schema 41）；Pi 消息以 `kind=pi` 存于现有消息表，旧记录保持可读。旧调度器不领取 `engine=pi` 运行。
- `docker-compose.yml` 的 Pi 服务使用 `profiles: ["pi"]`；Backend 的 `CANVAS_AGENT_ENGINE` 默认 `legacy`，故默认行为不变。

## 继续开发的硬边界

1. **不要部署或启用 `CANVAS_AGENT_ENGINE=pi`。** 目前首步提示和根模型任务仍由 Go 旧路径创建，Node 尚未独立组装 `AGENTS.md`/`AGENT.md`、`SOUL.md`、`TOOLS.md`。工具描述虽来自 Go 的 Markdown，参数 schema 仍由 Go 源码构造，尚未迁到双方共用的版本化定义。
2. 模型适配目前在 Go 任务完成后转发结果；真实增量正文、推理、工具调用、用量与终止原因的逐事件流和 SSE 对照尚未验证。`agent/src/runner.ts` 对画布图片 URL 使用 `canvasContent` 保留原始 Go 信封，需针对看图、观察账本和压缩做实测。
3. 验证 `PiToolBatch` 的模型回执绑定、参数哈希、批次重放及 `PiToolAdvance` 在媒体任务、审批通过/拒绝、画布版本冲突下的幂等性。当前代码只验证了基础模型调用 ID/参数等价和既有 Go 预检；不足以宣称写入恰好一次。
4. 补齐中断/取消、插话、续聊、上下文压缩、技能与记忆、计划、`finish_run`、跨用户隔离以及所有旧 SSE 事件的端到端测试。Pi 的系统提示组装与旧完成记录转成新 Pi 上下文仍需核对。不能为了通过测试绕过 Go 权限/计费路径。
5. 切换应先排空旧活动运行，再把新运行导向 Pi；旧 Go 调度循环暂不能删除。Pi 不可用时新运行必须排队或明确报错，不能自动退回旧引擎。

## 当前验证

- `cd agent && npm test`：4 个 Node 测试通过，含真实 Pi 循环的母类型到子工具披露和模拟租约运行。
- `cd backend && go test ./cmd/server ./internal/handler ./internal/app ./internal/repository ./internal/database -run '^$'`：编译通过。
- 完整 Go 测试在本机 `CGO_ENABLED=0`，SQLite 驱动报 `go-sqlite3 requires cgo`，不能据此判定业务测试通过。尚未运行本地 Compose 端到端、故障恢复和固定请求差异测试；按用户要求跳过 CI。

## 接手顺序

先复核 `git status`、此文件与用户的完整迁移计划；对照旧 `cloud_agent_runtime.go` 的首步创建、模型处理、工具预检、视觉观察、压缩与完成判定。随后补共享 schema/Markdown Harness 组装和内部协议测试，再在隔离数据上跑 Compose 端到端与故障注入。只有验收完成后才讨论 `CANVAS_AGENT_ENGINE=pi` 的流量切换或移除旧循环。
