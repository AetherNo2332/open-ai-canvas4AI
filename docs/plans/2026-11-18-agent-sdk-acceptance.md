# Agent SDK 实施与验收记录

本记录只覆盖 Agent SDK。未涉及生产修改、真实付费请求、提交、推送、PR 创建或部署。

下文路径相对于原开发工作树。历史日志位于该工作树的 `.local`。复跑与交接步骤见 [Agent SDK 测试交接](2026-11-19-agent-sdk-test-handoff.md)。

## A0 基线（2026-11-18）

- 工作树：`codex/agent-sdk-design-20261115@2d0a50d446db689d575512122cc3fd33def4cc08`。
- `git fetch canvas4ai main` 成功；远端 `422232409e1f30eb45e213c20c8a02d4c2efee67`；HEAD 比远端多 12 个提交，未合并。
- `origin=ddcat-ai/open-ai-canvas`，`canvas4ai=AetherNo2332/open-ai-canvas4AI`。原有变更均为文档，保留；没有读取任务排除的规划。
- `git fetch origin main` 成功：`origin/main=f33c39c0dfd8853f2b2c89c7dee15028ef54bb4e`；`HEAD...origin/main` 为 1410/132，不能把这两条不同来源的分支当成同一发布线。没有自动合并或改换基线。
- PR：`gh pr list --repo ddcat-ai/open-ai-canvas --head codex/agent-sdk-design-20261115 --state all` 返回空列表；未发现该 head 的 PR。没有因其他 PR 自动变更代码。
- 实测工具链：Go `1.27.1 windows/amd64`，Node `v26.1.0`，Bun `1.3.9`；Pi 依赖仍固定 `0.87.1`。仓库的 Go 1.25/Node 22 目标容器版本没有在本机执行。

| 能力 | 当前入口与复用位置 | 本轮处理 |
| --- | --- | --- |
| HTTP | `handler/agent.go` → `service` 别名 → `app/cloud_agent.go` | 复用 `/api/agent` |
| Run 准入 | `cloud_agent_run_create.go`，持有任务与首步换单 | 保留唯一预留/计费链 |
| 规则与提示冻结 | `cloud_agent_contract.go`、Run 内已有规则快照 | 复用冻结机制，SDK 显式回执 |
| 队列 | `agent_message_queue.go`、`agent_message_dispatch.go` | SDK 包装现有 conversation messages |
| Pi | `agent/src/runner.ts`、`bridge.ts`、`session-history.ts` | 保留 Pi loop 与压缩 |
| 租约/接管 | `cloud_agent_worker_recovery.go`、Pi session epoch | 先跑已有专项 |
| 工具/写入 | `cloud_agent_tool_dispatch.go` → commands/tasks/mediaversions | 不旁路业务权限与回执 |
| 图片 | `cloud_agent_vision*.go` → model bridge → 任务/资源/账单 | 统一 read 时复用水合与预算 |
| Web | `services/api/agent.ts` → `sdk/agent` → 注入 Transport | 画布现有调用方迁移 |

原有基线：`agent/npm test`：202 项，201 通过，1 跳过，无失败。Go Agent/Drama/repository/handler 专项通过：app 225.703s、repository 1.284s、handler 2.282s，日志 `.local/agent-sdk-baseline-go.log`。最初带过滤器的 architecture 结果为 no tests to run，不能算守卫通过；随后已显式执行不带过滤器的架构检查。

## 实施约束

使用指定工作树直接实施，保留原有文档修改；不提交、不使用子代理，仅运行受影响检查。按执行计划与先失败后修复的方法工作；自查代替独立审查，不能等同于第三方审查。

## A2 首个纵向切片

- Go `AgentBusinessScope`、`agent-sdk/v1` 回执；Canvas Scope 与旧 `canvasId` 共用幂等身份，矛盾/未知范围准入前拒绝。
- 幂等冲突增加稳定 `reason=agent_idempotency_conflict`，保留 HTTP 返回封套。
- TS 核心仅依赖协议类型与注入 Transport；支持 run、续聊、现有队列、审批、取消、SSE seq 续接。
- 画布 API 包装层接入同一 SDK。SSE 原 UI 包装暂留原展示事件与看门狗逻辑，SDK 提供跨业务消费者。
- 已验证：`bun test test/agent-sdk.test.ts`，2/2；`bun run typecheck` 通过（首次 SDK 添加后）。
- 已验证：`go test ./internal/app -run '^TestAgentSDK' -count=1` 通过（2.316s）。
- RED 证据：新增 Go 字段/类型缺失编译失败；TS client 模块缺失失败。新增实现后转为通过。

## A1 / A3：复用 Pi 运行时与统一 read

> **已被取代（2026-11-25）：** 本节验收的"统一 read"已在一次功能回退中撤除，`read` 恢复为只读 Skill 文本文件；画布正文、识图与短剧分集改用 `canvas_read_content`、`canvas_inspect_image`、`drama_read_episode`。下面的记录保留为历史验收来源，现行合同见 [Agent SDK 业务接入](../content/docs/backend/agent-sdk.mdx) 与[回退计划](<../superpowers/plans/2026-11-25-agent-skill-read-business-tools.md>)。

没有增加 Go 推理循环，也没有新建 Agent 服务。业务 read 使用现有 `PiToolBatch → 工具 operation → checkpoint → 回执`；Skill 文本继续走现有 native read 与 lease-fenced Skill 文件入口。

| read 路径 | 内容与授权 | 模型消费方式 |
| --- | --- | --- |
| `skills/<name>/SKILL.md` 或已选 Skill 的引用文件 | 冻结 Skill 版本、文本文件清单、原分页/预算 | 原 native 文本结果 |
| `canvas:text/<nodeId>` | 当前用户当前画布正文、Unicode 分页、来源/可见性 | 工具文本结果 |
| `canvas:image/<nodeId>` | 当前画布内已授权且 ready 的 Resource、原 MIME/尺寸/视觉能力/次数/批次预算 | 原 `image_url resource:<id>` 合同，模型任务执行时水合真实像素 |
| `drama:episode/<episodeId>` | 当前用户当前短剧项目内的集梗概、钩子、剧情及镜头记录，Unicode 分页 | 文本结果，`adopted=false` |

图片读取不把 URL 当看图成功，也不把达到图片次数上限后的纯文本回执冒充成功。模型不支持视觉、跨用户、未就绪、超限等路径明确失败。当前入口不是任意文件系统、任意 URL 或任意 Resource ID 的读取器。

统一 read 的图片继续用既有 `cloud_agent_vision*.go`、资源水合、任务/账单与视觉账本；不把 base64/签名 URL 作为新的持久权威，不另收一份 read 费用。正常两次模型调用是两次真实模型工作，重放同一个工具回执不会新建第三次模型调用或重复订单。

跨进程 smoke 的故障点为：旧 Node 已拿到 Go 成功工具回执，尚未把 Pi toolResult checkpoint 落库。杀进程、过期其租约、启动全新 Node 后，Go/Pi 持久历史与回执恢复继续完成。本地 smoke 不证明任意临时文件可恢复。

## A4 / A5：画布与短剧实际接入

画布原 `services/api/agent.ts` 中创建、续聊、队列消息、查询、审批、取消转用共享 SDK；保留原 API 导出名、`ApiError`、浏览器 Cookie/HTTP 入口以及原 UI 的 SSE 展示/看门狗行为。稳定的 Run、Request、Event、Queue、Skill、Compaction 类型归到 `sdk/agent/contract.ts`，画布仅扩展自己的审批展示和本地事件序号。

短剧接入为真实剧本页的“本集分析建议”：

- `scope={type:"drama",id:<真实项目ID>}`，`canvasId=""`，可带 `dramaEpisodeId`；后端先校验项目/集归属。
- 只允许 `read_only`，关闭画布 context/focus/subagent 请求；现有运行中权限切换入口也不能提升短剧顾问权限。
- `Task.ProjectID` 使用真实短剧项目，模型、压缩、任务读取沿现有平台路径；Run/Pi session 的 CanvasID 保持空，没有创建占位画布或新 Scope 表。
- 原短剧 Go 调度器、DramaJob、保存/采用/版本规则不变；分析只产出待用户审阅的建议。页面不自动写剧本、激活资源或启动第二套短剧作业。
- 共享 SDK 的 SSE seq 恢复、取消、费用与运行状态用于页面；地址中的 `agentRun` 支持按 Run ID 重连，后端仍按登录用户验证。

本轮没有实现自动采用分析建议的写接口。采用继续由已有剧本/镜头编辑入口完成；本轮自动化证明的是“提议未自动采用、未改写剧情”，不是一次真实人工采用流程已经验收。

## A6：兼容、停准入与发布顺序

无数据库表/列迁移。Scope、规则快照和集 ID 写入现有 Run JSON；旧画布记录不带 Scope 时由原 CanvasID 解释。旧 payload 与显式 Canvas Scope 的 Run/队列幂等身份一致，不新增 Conversation 创建接口。

Worker wire 从 `canvas-pi-wire/v4` 升到 **v5**；Pi SDK `0.87.1`、session 格式 `3` 不变。原因是统一 read 的业务引用必须走持久工具批次，旧 Worker 会把所有 read 当 Skill 文件。Go、Node 和 `PI_WIRE_IDENTITY.json` 同步；旧 v4 Worker 在领取/回调前收到 426 `agent_protocol_mismatch`，不静默把业务 read 降级成文件读取。

目标环境的后续操作顺序（本次没有执行）：

1. 备份并核对当前 API/Worker 构建版本与持久任务/账单；先停新准入并给旧 Worker 排空窗口。
2. 以同一源码版本构建 Go 与 v5 Worker，更新工具制品；不拿 v4 Worker 与 v5 控制面当兼容混跑。
3. 切到 v5 控制面/Worker 后，让未完成会话通过原租约/epoch 与 checkpoint 接管；验证既有 API 读取和队列，再开放新增短剧入口。
4. `CANVAS_AGENT_SDK_BUSINESS_DISABLED=true` 仅暂停短剧**新 Run**；已接受请求仍可按原键回放，已存在 Run 仍可读取、取消、排空。环境变量已接入现有 Compose 与 `.env.example`。本地通过该开关的服务测试，不等于已做生产切换。
5. 失败回退优先停新准入并保留 v5 的读取/排空能力；新 Scope/read 合同的在途记录不能交给 v4 强行恢复。保留 Task、BillingOrder、checkpoint、Pi history、事件和资源；不删记录、不倒退数据库来伪装二进制回退。

Docker 引擎探测失败：本机未运行 Docker Desktop Linux Engine。因此容器新旧版本混跑、排空、目标 Go/Node 版本、目标部署升级/回退均未验证。

## 验收证据与命令

命令在对应子目录执行；跨进程测试从本仓 `agent/dist` 加载 Worker，因此先运行 `agent/npm run build`。

| 检查 | 本次结果 | 日志 |
| --- | --- | --- |
| Go 广域受影响专项：`go test ./internal/app ./internal/repository ./internal/handler -run 'Test(CloudAgent\|Pi\|WorkerRecovery\|AgentMessage\|AgentSDK\|AgentRules\|InternalAgent\|Drama)' -count=1` | app 214.328s、repository 0.960s、handler 60.233s，通过 | `.local/agent-sdk-go-final.log` |
| 后续 task_get 边界定向复验：`go test ./internal/app -run 'Test(AgentSDK\|AgentMessageQueue\|CloudAgent.*(Read\|Task))' -count=1` | 25.061s，通过 | `.local/agent-sdk-go-focused-final.log` |
| `go test ./internal/app -run 'Test(AgentToolSchemaArtifactMatchesRuntime\|Native\|AgentCallLimits\|ConcurrentCancel\|CancelCloudAgent\|DifferentIdempotency)' -count=1` | 11.519s，通过；未把未运行的全量后端算通过 | `.local/agent-sdk-native-billing-final.log` |
| `go test ./internal/architecture -count=1`；`go run ./cmd/check-architecture-baseline --base HEAD` | 通过，baseline unchanged or reduced；未放宽基线 | `.local/agent-sdk-architecture.log`、`.local/agent-sdk-architecture-baseline.log` |
| `go vet ./internal/app ./internal/repository ./internal/handler` | 通过，无输出 | `.local/agent-sdk-vet.log` |
| `node scripts/check-go-format.mjs`、范围内 `git diff --check` | 通过；HEAD 仍为 `2d0a50d446db`，没有提交 | 本会话最终命令输出 |
| `agent/npm test` | 203 项，202 通过、1 跳过、0 失败；跳过 Windows rename-target health snapshot 压测 | `.local/agent-sdk-node-final.log` |
| Web SDK、原 API 可靠性、消息队列、审批、完成、全局状态、运行状态七个文件 | **75 通过、0 失败**，252 assertions | `.local/agent-sdk-web-final.log` |
| Web `bun run typecheck`、受影响文件 ESLint | 通过 | `.local/agent-sdk-web-types.log`、`.local/agent-sdk-web-lint.log` |
| Web `bun run build` | 通过；保留原有大 chunk / ineffective dynamic import 警告，没有据此扩大重构 | `.local/agent-sdk-web-build.log` |
| `bun build src/sdk/agent/index.ts --target=node` | 4 modules，9 KB；核心可独立打包，不引入 React/页面/store | `.local/agent-sdk-core-bundle.log` |
| `python -m unittest scripts.test_production_agent_smoke`（仓库根） | 27 通过；这是 smoke **脚本单元测试**，不是容器 smoke | `.local/agent-sdk-smoke-script.log` |
| `go test ./internal/handler -run '^Test(AgentSDKRealHTTPWorkerRestart\|InternalAgentRequiresVersionedPiWireIdentity)$' -count=1 -v` | 51.715s，通过；重启 smoke 49.82s，wire 版本拒绝 1.22s | `.local/agent-sdk-cross-process-final.log` |

跨进程用例：

- Go 使用独立临时 SQLite、测试账户/Resource、真实路由及后台 Worker；Bun 使用真实 SDK fetch/SSE，Node 使用真实 Pi runner，只有供应商响应为本地确定性 stub。
- 一个短剧 Run、一个画布 Run；重复 startRun 得到原 Run，同键异请求返回 `agent_idempotency_conflict`。
- 强杀旧 Node、重新领取会话并恢复缺失 toolResult；旧 epoch 不能续租。
- SSE 断线用 seq 续读、不会把快照 latestSeq 当事件游标；清理完成后观察真实终态。
- 两业务共 **4 次模型请求、4 张结算测试订单、1 次真实图片像素水合**；read 回执各一条，画布表仅保留测试中真实的一个画布，没有给短剧补造画布。

## 已观察的失败与修复

| 失败证据 | 根因与处理 | 复验 |
| --- | --- | --- |
| 新 SDK 类型/模块不存在 | 先写测试后增加 Go/TS 合同与客户端 | SDK 专项通过 |
| 队列全组报 `execution transcript is incomplete` | 队列锁只加载有界运行行；业务范围校验误用完整转录解码器。改为只解析持久 request 字段 | 队列专项与广域回归通过 |
| smoke 接管后维持 running | 测试把 SQLite 租约过期值用本地时区写入，文本比较误判。夹具统一 UTC；未改恢复实现 | 杀进程/接管 smoke 通过 |
| 短剧顾问可在运行时切为 auto | 通用权限切换遗漏业务限制 | 增加失败测试，再在后端拒绝提升，复验通过 |
| 损坏账户规则仍可创建 Run | 读取持久规则后缺少内容完整性校验 | 准入时验证，测试断言 Task/Order/Run 数量全为零 |
| `task_get` 拒绝短剧自己的任务 | 残留 `Task.ProjectID == Request.CanvasID` 耦合 | 改为真实业务项目 ID，失败测试转绿 |
| `cloud_agent.go` 超过 600 行 | 新合同字段触发既有守卫 | 将幂等身份函数移到 SDK 合同文件；没有登记新例外 |
| smoke 结束后后台轮询报数据库已关闭 | 测试清理只调用 Close，未先停后台 Worker | 夹具显式 StopWorker 再关闭；最终 smoke 无此清理错误 |
| Python 首次执行找不到 scripts 模块 | 命令工作目录误设 backend | 改在仓库根执行，27/27 通过 |

作者自查完成，未使用子代理/独立评审。没有把作者自查等同于独立审查。

## 改动文件索引

| 责任 | 文件 |
| --- | --- |
| Go 合同/准入/快照 | `backend/internal/app/cloud_agent_sdk.go`（新增）、`cloud_agent_business.go`（新增）、`cloud_agent.go`、`cloud_agent_run_create.go`、`cloud_agent_request_validate.go`、`cloud_agent_runtime.go`、`cloud_agent_runtime_validate.go` |
| Go 业务与 Pi 边界 | `backend/internal/app/agent_message_queue.go`、`cloud_agent_permission.go`、`cloud_agent_read_tool.go`、`cloud_agent_tools.go`、`cloud_agent_tool_dispatch.go`、`cloud_agent_pi_arguments.go`、`cloud_agent_pi_checkpoint.go`、`cloud_agent_pi_model_step.go`、`cloud_agent_pi_native_compaction.go`、`cloud_agent_pi_tool_batch.go` |
| Go HTTP wire / 测试 | `backend/internal/handler/internal_agent.go`、`internal_agent_protocol_test.go`、`agent_sdk_e2e_test.go`（新增）；`backend/internal/app/cloud_agent_sdk_test.go`（新增）、`cloud_agent_tool_dispatch_test.go` |
| Node / 共享制品 | `agent/src/bridge.ts`、`native-skills.ts`、`runner.ts`；`agent/test/runner.test.ts`、`bridge-wire-contract.test.ts`、`agent-sdk-driver.mjs`（新增）；`agent/contracts/agent-sdk-v1.json`（新增）、`agent/harness/TOOL_SCHEMA.json`、`PI_WIRE_IDENTITY.json` |
| TS SDK | `web/src/sdk/agent/contract.ts`、`transport.ts`、`client.ts`、`http-transport.ts`、`index.ts`（均新增） |
| 业务调用/真实页面 | `web/src/services/api/agent.ts`、`drama-agent.ts`（新增）；`web/src/pages/projects/detail.tsx`、`studio/script-view.tsx`、`studio/agent-analysis.tsx`（新增） |
| Web 测试 | `web/test/agent-sdk.test.ts`、`agent-sdk.e2e.test.ts`（新增）、`agent-api-reliability.test.ts` |
| 停准入配置 / smoke | `.env.example`、`docker-compose.yml`、`docker-compose.local.yml`、`docker-compose.server.yml`、`docker-compose.deploy.yml`、`scripts/verify-production-agent-smoke.py` |
| 计划/证据 | `docs/plans/2026-11-18-agent-sdk-incremental-delivery.md`、本文件 |

## 验收层级

| 层级 | 状态 |
| --- | --- |
| 单元/仓储/HTTP 测试 | 上表列出的受影响专项通过；没有运行/宣称全仓测试通过 |
| 跨进程 Go + Node smoke / Worker 重启 | 已验证；供应商为确定性 stub，数据库/账本/资源/HTTP/Node/Pi 是真实实现 |
| 真实模型 | 未验证 |
| 真实收费供应商 | 未验证，未发起付费请求 |
| 真实页面 | 未验证；真实组件已接入且类型/Lint/生产构建通过，不能等同浏览器验收 |
| 容器与生产升级/回退 | 未执行；Docker 引擎未运行，发布顺序和回退限制已记录 |

## Worker 领取机制核查（2026-11-18）

核查对象：`2d0a50d44` 加当前未提交 Agent SDK 实现；没有重新 fetch 或验证部署实例。结论为 **内部 SSE 监听唤醒＋Worker 主动 claim＋周期兜底；SSE 服务端自身仍轮询数据库事件表**。

```text
持久 AgentWakeEvent
  → Go GET /internal-agent/events（每 250ms 查询增量事件）
  → Worker consumeEvents（SSE 长连接，sequence 游标）
  → run_changed 唤醒 dispatch
  → POST /internal-agent/claim
  → Go 数据库事务选取可运行 Run、授予租约
  → 返回 PiSnapshot 和运行授权 → Pi 执行
```

| 环节 | 当前实现与源码 | 含义 |
| --- | --- | --- |
| 启动 | `agent/src/server.ts` 同时启动 consumeEvents 和 runEventSessions | 有容量的领取循环启动即尝试 claim，不必等第一条通知 |
| 通知 | `agent/src/bridge.ts:consumeEvents` 监听 `/internal-agent/events` | `run_changed` 唤醒 dispatch；通知不携带可直接执行的完整任务，不是指定 Worker 的派单 |
| 领取 | `bridge.ts:claim` → `handler/internal_agent.go` → `app/cloud_agent_pi_bridge.go` → `repository/agent_admission.go:ClaimPiAgentFair` | claim 是主动请求，按数据库中的运行资格、公平性、容量和租约决定；收到通知不保证拿到 Run |
| 空闲/容量 | `agent/src/event-scheduler.ts:runEventSessions` | 空结果后 `pause("dispatch",5000)`；容量不足用 capacity 等待；通知可提前唤醒。claim 成功后若仍有容量继续领取 |
| 补偿与重连 | `bridge.ts:consumeEvents` | 每 5 秒 recover 唤醒当前订阅者；断流后等待 1 秒重连，携带 Last-Event-ID，忽略已见 sequence；游标在进程内，重启从 0 开始 |
| 服务端事件读取 | `handler/internal_agent.go:registerInternalAgentWorkerRoutes`、`repository/agent_scheduler.go:AgentWakeEvents` | 每个 SSE 连接用 250ms ticker 查询持久表，每批最多 128 条，25 秒发心跳；该链路不是 Redis Pub/Sub 或数据库 LISTEN/NOTIFY |

注意：这是内部 Worker SSE，不是浏览器 `/api/agent/runs/:id/events`。5 秒是定时补偿间隔，不是领取延迟 SLA；无容量、健康/协议限制、请求耗时和租约资格都会影响实际开始时间。通知只作提示，真实状态和执行权由后端 claim/租约决定。

本轮验证采用 verification-before-completion 的证据要求。执行于 `agent/`：`npm run build` 成功；`node --test --test-timeout=30000 --test-force-exit dist/test/event-scheduler.test.js` 退出 0，12/12 通过、0 跳过，测试耗时约 16.26 秒。覆盖先订阅后读取、重复通知、dispatch/capacity 唤醒、SSE 续游标/去重、常驻会话与领取槽分离、容量释放及重复活跃会话。SSE 测试使用测试 HTTP 服务，领取测试使用受控 bridge；不等同真实 Go＋数据库＋两个 Worker 的端到端派发验收。

新增待验（不由上述 12 个用例推断通过）：

- [ ] D1：真实 Go/DB 与 Worker 空闲时新建 Run，记录事件写入、SSE 接收、claim 和执行时间；证明通知能提前唤醒。
- [ ] D2：断开 SSE 或错过一次通知且 claim HTTP 可用时，验证定时补偿最终领取；覆盖空 claim 返回与开始监听之间的竞争窗口。
- [ ] D3：两个真实 Worker 同时唤醒/claim，只获得合法单写者租约；重复通知不导致同一 Run 重复执行或收费。
- [ ] D4：容量满/draining/恢复后验证积压继续领取；目标部署验证 SSE 代理无缓冲、重连和历史事件回放。
- [ ] D5：测量 Worker 数量增长时事件表查询、空 claim、数据库锁等待和领取延迟。空闲每连接约 4 次/秒事件查询只是按 250ms 推算，不是性能实测；据实决定是否需要共享事件读取或外部通知机制。

## 下一步具体入口

1. 代码入口：`web/src/sdk/agent/index.ts`；第二业务入口：短剧详情 → 剧本 → 本集分析建议。
2. 无费用重跑：先 `cd agent; npm run build`，再在 backend 执行上表的 `TestAgentSDKRealHTTPWorkerRestart` 命令。
3. 隔离浏览器验收：使用实际项目/集，检查建议、页面关闭后重连、取消、失败提示、费用与“未自动采用”；再回归画布的旧会话续聊、审批、取消与资源采用。
4. 获得相应授权后，再做真实模型/视觉、付费供应商及 v5 控制面/Worker 的目标环境排空与升级/停准入演练。
