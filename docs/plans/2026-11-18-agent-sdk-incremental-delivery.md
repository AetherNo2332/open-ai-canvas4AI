# 基于现有架构的 Agent SDK 增量落地规划

> 状态：已在当前工作树实施 A0–A6 的本地代码切片，所列受影响自动化验收通过；真实模型、付费供应商、真实页面与部署仍待验。2026-11-18。
> 范围：近期平台更新与 Agent SDK。Workspace v2 当前属于工程验证，不是已确定的长期平台边界。

**目标：** 在现有 Go Agent 控制面、Node/Pi Worker、业务服务及资源/任务/计费结构上完成一次可用的 Agent SDK 更新，减少跨域改造并以真实业务反馈决定后续抽象。

**架构：** 保持当前主要调用链与持久化边界。复用已有画布 Agent 运行时和后端业务能力，从当前实现中最窄地提取 Session/Rule Snapshot/Tool 接口；先选一个业务纵向落地，第二个业务接入后再提取重复合同。平台更新、Agent SDK 和工程验证中的 Workspace v2 分开治理，不建立三者互相等待的依赖。

**技术栈：** 现有 Go/GORM、SQLite/PostgreSQL、Node 22 + Pi 0.87.1、现有 Web/TS API。此规划不新增 Agent 独立服务、通用工作流表、MCP 网关或 Workspace 重写。

**源码设计依据：** 远端 `canvas4ai/main@422232409` 的 Agent/短剧架构说明及此前核对；执行前刷新远端、PR 和实际工作树，并重跑入口清单。较新能力必须以本地实码和测试确认为准。

**本次实施记录：** [Agent SDK 实施与验收记录](2026-11-18-agent-sdk-acceptance.md)。代码沿本地 `2d0a50d446db` 实施；两处远端均已刷新，没有自动合并。下文勾选仅表示已取得相应代码/自动化证据，不能代替外部验收。

## 1. 方案与选择

考虑了三种路径：

1. **先重构通用平台层**：先引入通用 Scope、Agent service、Workflow engine、MCP Gateway 和文件抽象。复用性目标清楚，但早期跨数据库、服务、前端和业务改动面过大；缺少两个以上稳定接入方来验证抽象。
2. **沿当前架构做纵向切片（采用）**：保留云端 Go 控制面 + Node/Pi Worker + 当前业务 handler/service/repository；把需要复用的点抽成小合同，从一个真实业务跑通再接第二个业务。实现快，边界由真实调用收敛，也允许 Workspace v2 失败或改向时 SDK 继续工作。
3. **继续只做 Workspace v2，Agent 完全后置**：能减少短期并发，但 Workspace 本身仍处工程验证，而且 Agent 当前已有 Pi 运行代码，完全等待会延后暴露 Agent 的实际需求。

采用第二种。短期决策以“改动局限于当前拥有该数据和行为的模块”为约束，不把跨服务共享愿景提前翻译成跨域基础设施。此为执行建议；本轮只按用户已明确的方向修改规划。

## 2. 当前架构及约束

现有主要链路：

```text
业务页面 / 当前 Agent API
        ↓
Go Agent 控制、授权、规则快照、会话、任务、画布写入
        ↓ /internal-agent
Node Pi Worker（Pi loop、checkpoint、session history、工具桥接）
        ↓
Go Agent 回调 → 现有业务逻辑 / 创作任务 / 资源 / 计费
```

- 后端已维护云端 Agent 运行和授权；Pi loop 在 `agent/src/runner.ts`，当前 Worker 创建临时目录，运行结束清理。
- 画布 Agent 的 Session、CanvasID、Pi 历史和写入命令已存在。不要把 CanvasID 专属数据直接改成新增通用 Scope 表，先通过 SDK facade 适配。
- 现有 `AgentWorkspace`/Workspace v2 规则快照可继续作为可选规则来源。对本期 Agent SDK，它是一个可替换适配器，不是启动条件；可用现有默认规则或当前 Agent 配置时运行。
- 短剧的流程、单集阶段与媒体作业由现有 Go 短剧服务调度；Agent 只通过现有授权业务入口提供辅助。首版不新建通用 Workflow/Stage runtime。
- 电商专用 Agent 若接入，必须经现有业务服务核对报价、收费、持久化、取消及结果采用；不能把现有专用收费路径与 SDK 路径双跑。
- Web cache、Agent 临时目录或 Workspace v2 的云端文件能力均不得仅凭命名被当作 Agent 跨 Worker 的持久文件权威。首版只依赖已持久化的 Pi 历史、Go checkpoint、任务及资源记录。
- 遵守仓库 `AGENTS.md`：handler→service facade→app→domain/repository；服务、任务、权限、收费和资源身份仍以现有后端为权威。

## 3. 本期边界

### 纳入

- 当前 Agent 请求/运行合同审查与窄幅整理。
- Pi loop、Go model bridge、checkpoint、message queue、Worker lease/recovery 的复用和缺口修复。
- 一个无需 CanvasID 的测试接入或一个真实第二业务接入所需的最小 facade 验证。
- 在当前业务所有权处复用现有工具回调；评估真实 MCP 的必要性和传输，不把接口重命名为 MCP。
- 在现有 read 路径基础上统一读取入口的边界设计/最小实现；识图必须复用当前视觉模型请求和费用链路。
- 首个业务端到端闭环、TS client 的最小稳定接口及升级/回退文档。

### 暂不纳入

- 重做 Workspace v2，改写其数据归属、schema、容量、云对象版本或 UI。
- 全平台通用 Workflow/Stage 状态引擎。
- 云端任意文件系统或持久 POSIX 工作目录；不因需要生成就把完整输入项目自动复制到 OSS。
- 本机 Bridge、设备配对、目录授权及 bash 释放。它们保留在总目标中，单独进入后续立项；既有“bash 只能在已连接并授权的本机 Bridge 时提供”的约束不变。
- 同时迁移短剧、电商和画布三套业务写路径。
- 从旧分支整包 cherry-pick，或以新 SDK 名称掩盖未迁移的业务权限和账务。

## 4. 小接口边界

接口风格参考平台创作 SDK v1：版本化、语言间同口径的合同；Transport 可注入；业务错误有稳定 reason；幂等、事件游标和服务端权威状态均写清。Go 合同、现有 handler/app 和 TS 客户端是三处边界，不意味着新增服务。先在现有 app 和 Pi bridge 内定义清晰接口，只有两个具体调用方稳定且出现重复时才移到独立 `internal/agentservice` 包。

| 边界 | 首版合同 | 实施位置原则 |
| --- | --- | --- |
| AgentFacade | 创建/续接当前已有会话、排入消息、读运行/事件、停止 | 现有 `backend/internal/app` API；handler 只解码/鉴权/转调 |
| RuleSnapshot | `Load(owner, businessKind, objectId) -> {text, revision/source}`；现有 AgentWorkspace 或已有业务配置作适配；显式空规则与读取错误区分 | `app/agent_workspace.go` 附近；不先增加规则平台/新表 |
| PiRuntimeBridge | 冻结 Pi 入参、模型步骤、checkpoint、工具回执、租约续期和释放 | 继续用 `agent/src/bridge.ts` 与 Go Agent API，补类型和行为合同 |
| ToolExecutor | 当前工具名/schema/授权/调用/回执；可信身份由 Go 注入 | 按当前业务放在 `app/agent_*` 与现有领域服务；跨业务重复稳定前不建 Gateway |
| VisionRead | 读取明确的图片/文本引用，返回模型可消费内容块、来源和错误；不把对象 URL 直接等同于看图 | 复用现有 Agent vision/model bridge、资源授权和计费入口 |
| BusinessTask | 调现有创建任务、查状态/事件、取消与结果采用 | 继续委托创作 SDK/既有业务服务，不另建执行器或账本 |

对象存储策略、MCP 服务器拓扑、SDK 公共包结构等先不锁死；当两个真实调用方证明合同重复后，再在有证据的边界上扩展。

### 4.1 第一版外部 API 合同草案

当前 API 已有 `POST /agent/runs`、`POST /agent/runs/:id/messages`、Conversation 查询、队列消息、Run 状态、SSE、取消和审批。首版 SDK 应包装这些入口，不增加功能重复的 `POST /agent/conversations`；是否需要独立建空会话，等具体 UI/业务证明现有首条 Run 创建流程做不到时再加。

HTTP 位于登录态 `/api` 下，沿用统一响应信封 `{code,data,msg,reason}`。SDK 合同对 Go 与 TypeScript 同步版本化，放在当前 app 合同/API 类型与 `web/src/sdk/agent`（新增前先查分支实际路径）；共享 JSON fixtures 验证大小写、枚举、可选值及错误语义。浏览器 transport 复用现有 `http.raw`/请求入口；Node、测试传入 transport。SDK 核心不 import React、store 或 UI。

第一版请求沿用 `CloudAgentRequest` 字段，不复制模型、权限、费用或上下文合同。业务 Scope 是对现有 `canvasId` 的向前兼容扩展：

```json
{
  "scope": { "type": "canvas", "id": "canvas-123" },
  "canvasId": "canvas-123",
  "prompt": "总结这个画布的素材",
  "model": "optional-existing-model-selector",
  "permissionMode": "read_only",
  "budget": { "maxCredits": 20, "maxSteps": 12 },
  "idempotencyKey": "canvas:canvas-123:message-uuid"
}
```

合同规则：

- 第一阶段只允许 `scope.type=canvas`，并要求它与 `canvasId` 指向同一对象；客户端省略 `scope` 时维持旧 `canvasId` 语义。只有第二业务接入且已有后端归属校验后才增加 `drama`/`ecommerce`，不能只因 JSON 能表达就开放未知业务类型。
- 不接受客户端 `userId`、已验证 owner、授权结果、视觉能力或可执行工具清单。这些由登录身份、业务服务和冻结策略推导。
- `idempotencyKey` 重投必须完全复用原键和原请求；同键同合同回放原 Run，同键异合同返回稳定冲突错误。用户明确要求重新执行时使用新键。
- 规则正文与 revision/source 由 Go 在 Run 准入时读取并冻结；客户端不传规则正文。规则文件可来自现有 Agent Workspace/v2 adapter，但读取错误不能降级成空规则或先启动模型再补快照。
- `POST /agent/runs` 返回现有 Run 形状；SDK 可在其中定义较稳定的 `AgentRunReceipt { run, replayed }` facade，但不能未经调用方迁移就改变 HTTP 原返回封套。

现有创建与追加路径：

```text
POST /api/agent/runs
POST /api/agent/runs/{runId}/messages
POST /api/agent/conversations/{conversationId}/messages
```

上层 `AgentClient` 优先暴露与用途对应的 `startRun()`、`sendMessage()`、`getRun()`、`readEvents()`、`cancelRun()`、`decideApproval()`。追加消息/插话沿现有 `messageId`、`expectedRunId`、队列 revision 和服务端冲突合同；不另造第二套消息队列。直接创建 Run 与会话队列 API 的差别须在 client 方法文档中说明。

现有 `GET /api/agent/runs/{id}/events?after=N` 是 SSE，支持 `Last-Event-ID`；断线后用单调递增的 event `seq` 续接。SDK 可提供 `subscribeRunEvents()` 封装 SSE，另保留 GET 快照轮询/重读作为连接失败恢复；不要把新 SDK 重写成无游标轮询。现有会话列表继续用 `GET /api/agent/conversations?canvasId=...`，第一步只包装它；跨业务列表筛选留待后续支持非 Canvas Scope 时扩展。

错误采用稳定 `reason/code`、`retryable`、可选 `runId/taskId`。`message` 只用于展示，SDK 重试策略不能解析自然语言。权限、业务对象不存在、预算不足、过期批准、幂等冲突和 Worker 不可用分别保留机器可分辨原因。取消浏览器等待只停止客户端订阅，不等于调用服务端取消。

与创作 SDK v1 的对应原则：

| 创作 SDK v1 | Agent SDK 对应 | 不照搬的部分 |
| --- | --- | --- |
| `contractVersion`、Go/TS 合同与共用 fixtures | Agent API version / capabilitySetVersion 与 Go/TS 运行、消息、事件合同 | 不把 Agent 请求包进 `GenerationRequest` |
| 可注入 `Transport` | 可注入 Agent API/SSE transport | 不让 SDK 核心依赖 UI、画布 store 或浏览器全局对象 |
| `idempotencyKey` 与同键回放 | Run/Message 幂等身份和重复请求回执 | 不用 HTTP 自动重试替代 Worker lease/operation receipt |
| `TaskSnapshot`、游标事件和显式动作 | `AgentRunSnapshot`、SSE seq 和 cancel/approval 操作 | Agent 状态包含交互、审批和工具运行，不等同生成任务状态 |
| 平台唯一计费与资源事实 | Agent 调用已有任务/费用/Resource 身份并引用结果 | Agent SDK 不复制账本、资源上传或供应商执行器 |

第一版 API 验收：旧客户端不传 Scope 仍可运行；Canvas Scope 新旧表达一致；不支持/越权 Scope 在任何模型、工具或收费副作用前拒绝；同键响应丢失重投不生成第二 Run；SSE 断线按 seq 无丢失续传；审批、取消与消息队列仍走现有服务端状态机。

## 5. 落地分片与验收

顺序是本期建议。每片只改其责任模块，先补失败场景，再修复；禁止顺手把独立工程验证重构带入 Agent SDK。

### A0：当前基线与真实更新入口

- [x] 刷新目标分支和远端；记录 HEAD、PR 状态、未提交文件及可执行版本。
- [x] 盘点当前 Go API → app → Pi snapshot → Node runner → callback/tool → task/resource/billing 的真实链路，标明函数、已有测试和线上/未测差距。
- [x] 明确本次更新的交付入口、兼容窗口和所需功能；本轮按用户要求仅实施 Agent SDK。
- [x] 验证旧 Agent/短剧当前测试基线；专项 app/repository/handler 通过，Node 201 通过、1 跳过。

退出：存在逐文件复用矩阵、准确接口调用图和受影响测试列表；没有尚未验证的独立新架构先决条件。

### A1：稳定现有 Pi 云端闭环

改动范围：`backend/internal/app/cloud_agent*.go`、`agent/src/runner.ts`、`agent/src/bridge.ts`、相关 model/repository/handler/API；仅限追踪到的缺口。

- [ ] 画布现有用户可开会话、发消息、Pi 回答；Pi loop 仍只有 Pi 实现，不增加 Go 第二 loop。
- [x] 复用并专项验证 Worker owner/lease epoch；旧 lease 的模型、工具、checkpoint 与写入拒绝沿现有闸门执行。
- [x] 消息重投与既有模型/operation 幂等专项；新增跨进程 smoke 覆盖工具已提交、Pi 结果尚未落库时杀进程再接管。
- [x] Worker 重启/接管以 Go checkpoint/Pi history 恢复；smoke 使用全新 Node/Pi 临时目录，不承诺恢复任意文件。
- [x] 保持旧会话/API；`CANVAS_AGENT_SDK_BUSINESS_DISABLED=true` 停短剧新 Run，保留已准入请求回放、读取、取消与排空。

退出：相关 app、repository、handler、agent Node 测试通过；至少一次跨进程 Worker 重启 smoke；无重复收费任务或旧 lease 写入。

### A1 补充：Worker 如何获得运行

当前实现采用内部 SSE 通知唤醒、主动 `/internal-agent/claim` 领取和 5 秒补偿重查。Go 的 `/internal-agent/events` 每 250ms 查询数据库持久事件，不应描述为整条链路完全无轮询。通知不是任务执行权；公平准入、容量和租约仍在 claim 事务中决定。

- [x] 源码链路及现有 Node 调度专项核查：12/12 通过，具体命令与覆盖边界见[验收记录](2026-11-18-agent-sdk-acceptance.md)“Worker 领取机制核查”。
- [ ] D1–D4：真实后端/数据库/Worker 的通知领取、断流补偿、双 Worker 竞争、容量/draining 与部署 SSE 代理验证。
- [ ] D5：测量事件查询和领取的负载与延迟，未有数据前不新增消息中间件或重写调度。

本项补充派发验收范围，不撤销已有 Worker 重启/回执恢复证据，也不把既有 smoke 当作所有通知故障路径通过。

### A2：冻结规则与最小 SDK API

依赖：A1。改动只落在现有 `agent_workspace.go`、云端 snapshot 装配与 API 类型。

- [x] 添加可选 Scope；Canvas 与旧 ID 一致。经 A4 的真实项目/集归属校验后开放只读 drama，CanvasID 留空。
- [x] Run 冻结规则 text/source/revision；规则修改只影响新 Run。
- [x] 规则缺失显式空；保留原适配器的拒绝语义，并补账户规则损坏在 Task/Order 创建前拒绝。
- [x] Go/TS 合同、注入 Transport、稳定错误、共用 JSON fixtures；复用 run/message/SSE/cancel/approval。
- [x] 旧/新 payload 同幂等身份、跨用户拒绝、同用户多会话及规则冻结专项；画布 API facade 已接共享 SDK。

退出：现有画布调用方和一个 SDK 合同测试调用方共用稳定 API，旧 HTTP/持久化可继续工作；本片不要求新的通用 Scope 表或数据库迁移。

### A3：统一 read 与识图入口

依赖：A2。先覆盖真实图片输入，不把任意文件系统 read/write/edit 全部作为首版目标。

- [x] 实码确认 native Skill read 只返回受选中文本；业务 read 复用持久工具批次，保留原 Skill 路径。
- [x] `canvas:text/<id>`、`canvas:image/<id>`、`drama:episode/<id>`；画布图片复用授权资源与 image content 水合，smoke 断言真实像素。
- [x] 沿用 vision 能力、尺寸/批次、Task/Order；返回来源、授权可见性、分页/截断及错误。
- [x] 新 read 无视觉/无权/未就绪/超限明确失败；MIME/尺寸校验委托既有资源/视觉链，相关原专项复用。
- [x] 原视觉安全专项加新 read 拒绝测试、跨进程回执重放/计费断言；没有单独收取 read 费用。

退出：同一 read API 可读取文本和看图，真实视觉请求能够返回可追溯费用记录；本机文件读取仍未开放。

### A4：一个业务纵向接入（短剧建议）

依赖：A3。继续沿短剧既有后端导演/编排 API、DramaJob/Task 和采用规则。

- [x] 实施“本集剧情/镜头分析建议”，真实剧本页接入共享 SDK；用户实际效果待页面/模型验收。
- [x] 既有 Agent 模型任务、状态/事件与计费；任务 ProjectID 使用真实短剧项目 ID。
- [x] 短剧调度器、作业与采用规则不变；本片为只读建议，不提交第二份 DramaJob。
- [x] 建 Run 及运行中权限切换均限制只读；read 回执 `adopted=false`，页面显示待审阅，不自动覆盖剧情。
- [ ] 真实页面验证失败、取消、迟到结果、任务重连和费用/资源显示。

退出：短剧用户从 Agent 发起单条纵向业务能力，页面端可观察完整 task、费用、结果和采用状态；没有双作业或重复收费。

### A5：第二业务验证后提炼边界

依赖：A4。第二业务在 C0 依据当前更新范围选择电商或画布。

- [x] 两个具体消费者为画布与短剧；仅共享 Run、消息、事件、取消、审批、业务 read 与既有模型任务。
- [x] 画布继续 commands/tasks/mediaversions/事务回执；短剧保持自己的项目/集/调度器。
- [x] 画布旧 API facade → SDK → 原路由；短剧 facade → 同 SDK/路由 → 真实 Project 归属；只有原 Task/Order 执行收费。
- [x] 跨进程 smoke 两业务共用 SDK、Go 控制面和同一 Pi loop；没有新增通用 Scope 表或第二执行器。
- [ ] 第三个测试接入仅用于验证新增业务不修改 Pi loop；若仍需要复制大段业务逻辑，回到 A0 调整边界。

退出：至少两个真实业务使用共享但有限的 Agent API；业务规则和最终数据仍在各自业务域。Scope 每开放一个新业务，先有该业务真实的 owner/object 授权校验与对应纵向测试。

### A6：更新发布与回退验收

依赖：A1–A5。Workspace v2 的验证与结论另列，不合并为一个统一“平台 v2/Agent v1 已通过”结论。

- [x] 无数据库迁移；新增字段持久化在现有 Run JSON；统一 read 将 Worker wire 明确升到 v5，v4 返回 426，保留 Pi 0.87.1/session v3。
- [ ] 在隔离环境验证新旧 Worker 混跑/排空策略、运行中会话续接和消息幂等。
- [x] 本地验新准入开关与既有请求回放/读取；部署排空与二进制回退说明见验收记录。既有新合同运行需保留 v5 处理能力。
- [x] 分别记录代码、跨进程 smoke 与外部待验项；没有发起付费或部署操作。
- [ ] 将已证明的共性和残留差异回写 Agent 总设计，再决定是否立项真实 MCP Gateway、通用 Workflow 或 Bridge。

退出：目标更新可在隔离环境完成升级、运行、排空与回退演练；任一外部/付费验证未做则明确标待验，不把文档计划计为通过。

## 6. 明确延后的议题

- **Workspace v2 去留与服务器存储边界**：保留其当前工程验证代码、PR/未提交工作和证据；单独按真实 OSS/数据库增长、客户端运行时与用户体验评估。Agent SDK 只需可插拔规则/资源适配，不把 v2 定为长期唯一权威。
- **统一 MCP**：先用现有 Go 回调验证业务合同；只有真实跨业务调用证明协议互操作/进程隔离需求，才引入 MCP client/gateway。届时保留既有授权与 operation receipt。
- **通用 Workflow/Stage**：本期复用各业务流程。短剧沿现有调度，电商沿现有业务规则；两个真实业务确认确有共性后再决定是否抽引擎。
- **本机 Bridge 与 bash**：本期不实现，不改变“bash 仅在已配对、在线、授权的本机 Bridge 下释放”的既定安全规则；进入后续专项设计与 OS 隔离验收。
- **云端持久文件/OSS**：本期不新增完整项目目录、任意文件编辑或全量 Workspace 上传。只使用已存在且经授权的资源/业务输入，生成资产遵循当前 Resource 生命周期。

## 7. 与现有文档及证据的关系

- 本计划约束接下来的 Agent/平台更新范围；不重写 `docs/plans/2026-11-16-workspace-v2-local-delivery.md` 已记录的 Workspace 实施、历史验证和 W7 阻塞。
- 原 Pi 集群设计的 P0–P8 是远期目标与安全约束。A0–A6 是本期实施顺序。
- `docs/index.md` 应同时链接本计划、Agent 设计与 Workspace 现有实施报告；删除的 Workspace 初始设计文档是否恢复，应按用户变更记录另行处理，不在本次隐式恢复。
- 执行每个分片只跑受影响专项、类型/架构守卫和必要集成链；真实 UI/收费/生产验收分别标注，绝不由单元测试推断。

## 8. 下一次开发入口

本地实施入口与命令见 [验收记录](2026-11-18-agent-sdk-acceptance.md)。下一步是保留 v5 Worker 的隔离页面验收：短剧详情 → 剧本 → 本集分析建议，以及旧画布 Agent 的续聊/审批/取消；再按授权执行真实模型、收费供应商与目标部署演练。未验项目不由本地测试替代。
