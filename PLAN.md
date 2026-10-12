# Pi Agent 迁移交接（更新于 2026-09-28）

本文件记录当前 `canary` 迁移状态，供接手的 Agent 继续开发。目标是独立 Node 22.19+ 服务以固定版本 `@earendil-works/pi-agent-core@0.87.1` 运行唯一 Agent 循环；Go 保留鉴权、画布写入、模型与计费、持久化及原 `/api/agent`/SSE 合同。用户已授权只在本机 `canvas-canary-3000` / 3000 部署测试；没有生产部署授权。当前工具模型是平铺注册所有本轮符合权限/能力的具体工具，并用 Pi `tool_call` hook 与 Go 服务端逐次检查；类别仅作元数据，不能再改回母工具分层披露。

## 已落地的代码

图片不能按轮次或模型容量删掉，不能用摘要替代；文本压缩后从 Pi active branch 恢复原始图片引用，容量不足明确停止。终态不再发租约绑定回调，历史缺失工具结果仅补模型协议投影，不重执行业务操作。

- `agent/`：Pi 依赖锁、模型结果到 Pi 事件的适配、合格具体工具注册与 `tool_call` hook、Node 领取与租约循环、基础消息检查点和崩溃后工具回执补齐；`agent/Dockerfile` 提供独立镜像。
- Go `backend/internal/app/cloud_agent_pi_bridge.go` 与 `backend/internal/handler/internal_agent.go`：仅内部 Bearer Token 可访问的领取、快照、续租、模型步骤、Pi 消息、工具批次、工具推进和无工具收尾接口。每次按用户与租约读取运行；工具继续经 Go 既有权限、参数、审批和画布写入路径。
- `cloud_agent_executions` 保存 engine、租约和 Pi session 状态；Pi 消息与工具回执持久化到现有存储，旧记录保持可读。新 Agent run 使用 Pi。
- `docker-compose.yml` 的 Pi 服务使用 `profiles: ["pi"]`；新建 Agent run 由 Pi engine 承载，未配置服务 token 时内部 worker 不会启动。

## 继续开发的硬边界

1. **这是一个实验性的项目，完成目的是移植 pi agent 内核**：完全抛弃旧的 agent 循环，不保留双引擎共存。参考 https://pi.dev/docs/latest/configuration 与 https://pi.dev/docs/latest/sdk 修改相关模块以兼容 pi-agent。
   - 已完成：新运行一律使用 Pi；旧 Go Agent scheduler/driver 推进入口已删除，Node Pi worker 是唯一 Agent 循环。保留的 Go 函数是授权、画布/媒体业务、计费、持久化、恢复与清理服务，不构成第二个 Agent loop。
   - 已完成：所有合格具体工具在本轮开始注册；类别只作元数据；Pi hook 绑定当前模型批次准入，Go 每次执行再验证权限、归属、schema、审批、画布版本与回执。
2. Go 承接模型步骤与上游渠道/计费，并将 Pi 请求和结果映射回运行状态。静态与 stub 合同已覆盖；真实供应商的流式事件、推理、真实 usage/stop reason 与完整 SSE 客户端交互仍需验收。
3. 已增加模型步骤 ID 对工具批次及回执的绑定，覆盖重复调用 ID 跨步骤和陈旧步骤拒绝；媒体计费、审批后结算、跨进程故障窗口的 exactly-once 组合验收仍待完成。
4. Pi 提示、压缩、计划、插话、观察账本、工具、审批、续聊和 finish 判定已有实现或专项合同测试；真实多账号 UI、跨 worker 恢复、PostgreSQL 与 SSE 续传仍需验收。不能绕过 Go 权限/计费路径。
5. Pi 不可用时新运行必须明确排队或失败，不能自动退回旧循环。当前只在本机隔离 `canvas-canary-3000` 部署，不代表生产切换。

## 当前验证

- Agent `bun run build` 通过；`bun run test` 78/78 通过。另一次 source + dist `bun test` 155/155 通过。
- Web `bun run typecheck` 通过；完整 `bun test` 2108/2108 通过，271 个文件、11767 个断言；Vite/Docker 生产构建通过。
- `CGO_ENABLED=1 go test ./internal/app -count=1 -timeout 12m` 通过，用时 452.941 秒；Pi 定向测试与 `internal/handler` 全量测试通过。当前 CGO/GCC 可用，CGO 环境不是阻塞。
- 本机 Compose 3000 的 backend 已部署提交 `eae5177bca533afc730530bd84bc1604ca799831`；`/api/health/ready` HTTP 200、`ready=true`、schema 43/43，buildTime `2026-09-28T09:40:15Z`。仅重建 backend，web/agent 保持运行；未部署到生产。
- Playwright 管理员 UI 验收通过：品牌名后 canary 黄色底黑字无衬线标记位置与样式正确；Agent 创建画布并以只读权限运行 `qwen3.8-27b`，成功调用 `canvas_get_state` 并完成回答“0 个节点”，无浏览器页面错误。该运行未执行写入或媒体生成。
- Agent 全量测试 78/78 与 source/dist 测试 155/155 通过；Web 全量测试 271 个文件、2108 项、11767 条断言通过；Go repository/database/app 全量套件及 handler/protocol/service 套件通过。PG、真实媒体生成与费用结算、跨 worker 故障注入和 SSE 断线续传仍需独立验收。
- 代码已提交 `eae5177bca533afc730530bd84bc1604ca799831` 至 `codex/pi-agent-migration`，PR [#58](https://github.com/AetherNo2332/open-ai-canvas4AI/pull/58) 指向 `canary`，待合并；提交包含 `[skip ci]`，没有 CI 检查运行。

## 接手顺序

新增代码前确认当前 Git 状态与 Pi 单循环边界；对任何不完整能力补协议和持久化测试，再补真实上游、跨账号/worker、PostgreSQL、SSE 及 GUI 验收。

## 经用户确认的完整实施计划

### 目标与固定取舍

- 在仓库内建立独立 Node Agent 服务，以锁定的 `@earendil-works/pi-agent-core` **0.87.1** 作为唯一 Agent 循环；运行时要求 Node **22.19+**。使用 Pi 的自定义模型流函数与工具调用钩子适配画布业务。参考 [Pi Agent 文档](https://github.com/earendil-works/pi/blob/main/packages/agent/README.md) 和 [包定义](https://github.com/earendil-works/pi/blob/main/packages/agent/package.json)。
- Go backend 继续负责用户鉴权、画布数据与写入、模型渠道、任务计费和持久化。Pi 服务独立容器运行，不直接持有数据库写权限；模型请求继续经过 Go。
- 保留已有画布能力、历史记录、`/api/agent` 对外接口及 SSE 事件格式。旧活动轮次排空后完成切换；本计划不包含立即部署。

### 阶段 1：运行边界和内部协议

1. Node 服务承担提示组装、Pi 消息和事件循环、工具披露、上下文治理与恢复。Go 对外 API 不变。
2. 完成仅内部网络可访问且带服务认证的领取运行、租约/检查点、模型步骤、工具执行及控制信号接口。每个写入接口验证用户归属、租约和运行状态；不能只信任 Node 传来的用户 ID。
3. 将旧轮次与新轮次明确标识。Pi 不可用时，新轮次排队或明确报错，不能交给旧循环代跑。

### 阶段 2：模型与画布适配

1. Pi `streamFn` 把每一步请求交给 Go 现有模型任务链路，完整映射流式正文、推理、工具调用、用量及停止原因；保持计费与模型渠道选择在 Go。
2. 画布工具通过 Go 内部接口执行。Go 对**每次**调用重查用户归属、能力、权限、参数、审批及画布版本。一个模型工具批次顺序执行，整批预检必须先于任何写入；媒体生成仍使用已有任务与结算路径。
3. 以固定请求对比迁移前后的模型信封与结果，不允许因协议转换丢失看图内容、工具回执、错误类别或停止原因。

### 阶段 3：Harness 迁移

1. Node 加载现有 `AGENTS.md`／`AGENT.md`、`SOUL.md`、`TOOLS.md`、系统策略和工具描述；工具描述继续由 Markdown 提供，不写死在源码。工具参数 schema 改为 Go/Node 共用、带版本的定义，并对旧运行保留合同快照。
2. 每轮开始将符合权限和能力的所有具体工具平铺注册给 Pi；类别仅为分组元数据，不创建母工具或按母工具逐步披露。`tool_call` hook 限定运行内可见集合及当前模型批次准入，Go 对每次工具执行重复验证。registry 每轮重建；上一模型步骤调用的具体工具名仅写入下一模型步骤对应 schema 描述，不记录敏感参数和结果。
3. 保留视觉观察账本、技能与记忆、计划、续聊、审批、插话及 `finish_run` 的完成判定。上下文压缩要保留可恢复的事实与工具因果顺序。

### 阶段 4：持久化与恢复

1. 沿用现有运行、事件和消息表；保存引擎版本、租约、Pi 消息及工具回执。旧完成记录可读，并能转为新轮次可用的 Pi 上下文。
2. 模型步骤与业务工具使用运行 ID、调用 ID、参数哈希做幂等键。模型结果确认与 Pi 消息检查点须原子提交；工具副作用与回执须能在崩溃后对账，避免重复画布写入或计费。
3. 验证领取竞争、租约过期、重复投递、进程在各事务边界中断后的恢复；SSE 按原事件序号支持断线续传。

### 阶段 5：验收与切换

1. 已通过自动化测试和本机 Compose API smoke 覆盖部分只读能力；剩余重点是 GUI、真实渠道/媒体/审批结算、跨账号并行、跨进程恢复、PostgreSQL 与 SSE 断线续传。
2. 本地 `canvas-canary-3000` 的有限测试部署已授权且已完成；不修改生产环境。用户曾明确要求跳过 CI，本次提交/PR 使用 `[skip ci]`。
3. 旧 Go Agent scheduler/driver 已移除，Pi 已是唯一循环；该架构状态不代表所有能力验收或生产上线已完成。
