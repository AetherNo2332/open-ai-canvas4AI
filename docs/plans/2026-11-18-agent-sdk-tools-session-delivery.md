# Agent SDK 工具注册与上下文隔离开发规划

> T1–T5 的历史验证见[本阶段验收记录](2026-11-18-agent-sdk-tools-session-acceptance.md)。4AI 分支复验及剩余验收见[测试交接](2026-11-19-agent-sdk-test-handoff.md)。
> 执行方式：使用 executing-plans 按分片在主会话推进；不默认启动子代理，不自动提交、推送或部署。

**目标：** 在首轮 SDK 实现之上完成业务 Agent 注册、统一读取适配和业务会话管理；允许跨业务共享语言、输出风格和明确的个人偏好，禁止默认共享业务内容与历史。

**架构：** 保留 Go 控制面、Node/Pi Worker、现有业务服务和 Task/Billing/Resource 链路。服务端注册业务 Agent 配置与工具适配器，启动时冻结配置；业务写入仍经过现有业务服务。Worker 只执行后端授予的能力。

**技术栈：** 仓库现有 Go/GORM、SQLite/PostgreSQL、Node/Pi、React/TypeScript；具体工具版本以执行时约束文件与锁文件为准，不为本计划升级依赖。

**设计依据：** 用户确认“先上下文策略，再 Agent/工具注册，再 read 适配，再会话管理，最后隔离验收”，并允许共享语言、输出风格和偏好。本文件第 2–4 节是本阶段设计合同；此前基线见 [A0–A6 增量规划](2026-11-18-agent-sdk-incremental-delivery.md) 和 [已有验收记录](2026-11-18-agent-sdk-acceptance.md)。

## 1. 基线与全局约束

- 本次核对基线：本地 HEAD `2d0a50d446db689d575512122cc3fd33def4cc08` 加现有未提交 Agent SDK 改动。不是该提交本身已包含全部能力，也不是远端 main 已上线的结论。
- 执行前保存 `git status --short`、HEAD、涉及文件差异及已有测试证据；不得覆盖其他会话修改。已有 Workspace v2 修改保持原状，不作为本阶段开发前置条件。
- 当前已具备：用户归属检查、跨 scope 续聊/入队拒绝、Pi 会话持久化、临时执行目录、统一 read 初版。不得重写这些底座。
- 当前缺口：长期记忆与自由文本系统提示词按账户共享；工具仍集中按条件编译；会话目录仍绑定画布；业务 read 仍为硬编码分支。
- 不建设通用 Workflow 引擎、MCP 网关、新 Worker 服务、完整文件系统或 Local Bridge；不开放 bash，不强制接入电商 Agent。
- 不把“独立临时目录”宣称为操作系统沙箱；不开放宿主机任意路径或任意 URL。
- 权限、业务规则、工具选择、记忆归属在后端执行；前端仅承担展示、选择、交互和 API 调用。
- 本阶段延续 Agent SDK v1，使用独立的配置/快照版本；如协议语义变更使现有 Worker 不安全，必须更新 wire 身份并明确拒绝不兼容 Worker，不能仅改版本字符串。
- 所有勾选必须附执行证据；代码完成、自动化通过、真实页面通过和部署通过分别记录。

## 2. 已确定的上下文与隔离合同

### 2.1 可共享的个人偏好

新增独立的 `AgentSharedPreferences`，不复用整份账户记忆或系统提示词作为偏好载体。首版只存结构化字段：

| 字段 | 值与语义 |
| --- | --- |
| `language` | 可选语言标签，如 `zh-CN`；未设置则跟随当前用户输入，不写入隐式默认值 |
| `tone` | 可选 `neutral / professional / casual` |
| `verbosity` | 可选 `concise / balanced / detailed` |
| `format` | 可选 `auto / prose / bullets` |
| `revision` | 服务端递增版本，用于并发更新与运行快照 |

这些字段构成首版可共享偏好白名单，不增加任意字符串 `notes` 或开放键值包。未来新增偏好字段需确认它不承载项目资料、权限或执行授权。偏好由用户在现有账户设置中维护，Agent 不自动从业务对话提取或修改全局偏好。

偏好不授予工具、资源、支付或自动执行权限；本轮明确的语言/格式要求可以覆盖偏好，但平台安全约束与业务规则始终有效。

每个 Run 冻结偏好值与 revision。用户修改偏好只影响新 Run；正在运行、重试、Worker 恢复继续使用原快照。续聊形成新 Run，可获得最新偏好。

### 2.2 默认不共享的内容

- 会话历史、压缩摘要、工具结果、图片内容与识图摘要：限定当前会话及其已授权业务范围。
- 业务长期记忆：默认 `off`；业务 Agent 配置可显式启用 `scope`，存储键为 `userId + scope.type + scope.id`，不得按 userId 单独查询。
- 启用 scope 记忆意味着同一业务对象下不同会话可以共享这份明确的业务记忆；它不意味着共享其他会话的完整历史。此语义需在界面说明。
- 账户旧记忆和账户自由文本系统提示词：保留存储与查看能力，但新隔离合同的 Run 不自动注入、不自动分类或复制为共享偏好。旧内容需用户明确选择迁入具体业务对象，迁移操作不列为本阶段必交付功能。
- 画布 AGENTS.md 仍按当前画布冻结；短剧可保持现有固定只读规则。业务规则来源通过配置描述，不要求短剧先建设文件工作区。
- 技能代码和公共静态规则允许复用，但技能选择必须由 Agent 配置声明；不再隐式注入所有全局默认技能。技能不获得额外资源权限，也不得携带其他业务运行产生的私有数据。
- 私有素材、任务与生成结果也属于上下文来源。仅校验“同一用户拥有”不足以授予某业务 Agent 访问；通过对象绑定或明确资源授权清单限定范围。公共模板可由配置明确开放。

### 2.3 会话与工作区

- 会话创建时绑定 `userId + scope.type + scope.id + agentDefinitionId`，业务 scope 和 Agent 配置身份不可在续聊中改变。短剧 scope 为项目；单集是本轮焦点，不作为强制隔离边界。
- 一个业务对象可有多个会话；新会话默认不继承旧历史。Agent 配置版本随 Run 冻结，后续 Run 可采用同一配置身份的新版本，但必须重新准入并显示实际能力。
- Worker 临时目录继续按执行实例分离，清理与持久化会话解耦；数据库会话/检查点是恢复依据，不要求固定 Worker 或共享工作目录。
- 同一用户可在账户入口管理自己各业务会话；某业务 Agent 无权据此读取该用户其他业务历史。业务 UI 查询范围与服务端授权同时生效。

## 3. 注册与执行标准

### 3.1 业务是否使用 Agent

普通业务 API 和固定流程不需要 Agent 注册；同一业务允许同时有固定流程和 Agent 入口。

使用 Agent 的业务必须在服务端注册默认 `AgentDefinition`。前端不上传工具实现、工具 schema 或完整能力清单，只选择受准入校验的配置身份和业务对象。

建议首版配置字段：`id`、`version`、`businessType`、`isDefault`、`businessTools`、`readAdapters`、`skillIds`、`memoryPolicy`、`interactionPolicy`。每个启用 Agent 的业务恰有一个默认配置；普通不使用 Agent 的业务无需登记“禁用配置”。

- `businessTools` 必须显式声明：缺字段为配置错误，`[]` 为合法零业务工具。
- 零业务工具可执行基于已提供内容的文本任务；不强制提供 read/edit/write。
- `readAdapters: []` 表示零业务读取能力；Skill 文件读取是独立的运行时能力，不因此获得业务数据访问。
- 默认配置不存在、未知工具、重复名称、错误业务适配器或缺少执行器，均在注册校验或启动准入时失败；不能回退成画布工具集。
- 同一实现可以被多个业务显式复用；“各业务注册自己的工具”指明确声明授权集合，不是复制实现。
- `interactionPolicy` 明确是否允许提问；仅在宿主确实具备回答链路时暴露 `ask_user`。终止运行等内部控制工具与业务工具分别管理。

### 3.2 统一 read 与业务写入

模型侧保留一个 `read(path, offset?, limit?)`。业务通过注册适配器解析引用，不能自己绕开平台图片输入与预算链路。

统一读取回执至少包含：`source`、`contentType`、`revision`、类型化的文本或授权图片引用、分页信息。文本沿用现有 Unicode 字符分页；图片不接受文本分页。图片返回平台可验证的资源引用，平台再执行图片加载、模型视觉能力检查与预算控制。

首批适配器迁移现有 `canvas:text/`、`canvas:image/`、`drama:episode/`，保留现有业务校验与图片处理实现。统一 read 的描述按当前可用适配器生成，不向短剧宣称可读取画布。

不引入万能 write。现有画布写工具注册为业务工具，并继续调用既有命令服务、审批和版本冲突检查。短剧继续只读。未来需要标准 edit/write 的业务必须显式提供适配器，语义为版本受控文本修改/写入；未注册则不暴露。复杂业务动作继续使用专用工具。

执行能力取交集：注册配置 ∩ 当前用户/对象权限 ∩ 本轮授权 ∩ 模型能力 ∩ 平台策略。工具在模型侧不可见和后端不可执行必须一致；冻结快照不能阻止后端执行时检查对象删除、权限撤回等实时安全状态。

### 3.3 前端业务接入与验收（11 月 19 日补充）

已有入口和本地 Chrome 验收见[本阶段验收记录 T5](2026-11-18-agent-sdk-tools-session-acceptance.md)。后续接入从已有 TS client 和真实页面开始，按下列责任交付：

| 层 | 已有实现 | 新业务接入约定 |
| --- | --- | --- |
| 通用通信 | `web/src/sdk/agent/client.ts` 支持绑定 scope、会话查询、Run 操作与事件重连 | 按业务对象创建 client；使用命名配置时绑定 `agentDefinitionId`。切换用户、对象或配置时取消旧请求和订阅，忽略已到达的旧回调。 |
| 业务适配 | `web/src/services/api/agent.ts` 封装画布调用，`drama-agent.ts` 构造项目/分集分析请求；短剧页面自行创建 scoped client | 适配器接收业务对象、用户输入与本轮焦点，封装请求映射和 scoped client 创建。新页面无需知道画布兼容字段，也不向 Go 声明工具权限。固定流程保留自己的入口。 |
| 用户交互 | 画布保留助手面板、节点与预览交互；短剧分析页有历史、提问、回答、续聊、归档与取消；账户设置单独编辑共享偏好 | 业务选择适合的界面。公共交互使用同一套事件和消息语义；业务专属工具回执由业务展示。图片读取复用服务端结果，采用、编辑或提交仍走各业务原入口。 |

**给下一会话的补充任务（尚待专项验收）**

- [ ] T2/T4：对照真实调用方检查业务操作是否始终携带 scope，重点覆盖画布保留的直接 HTTP 调用和短剧可选的未绑定 client。保留账户管理入口的独立语义。用请求断言验证启动、读取、订阅、回复、审批与取消的实际地址和身份。
- [ ] T4：提问、审批与续聊按当前 Run 的服务端状态和宿主支持能力呈现。只读短剧收到建议时展示待采用结果；归档或不可续接的会话仍能查阅。仅在两个页面有相同交互时提取共享逻辑，保留画布专用预览。
- [ ] T5：第三业务测试宿主通过同一 TS SDK 启动零业务工具配置，恢复会话、接收输出并取消；不得导入画布组件。增加旧 SSE 回调晚到的切换用例，断言不会覆盖新业务页面。已有 Chrome 路径沿用原证据，变更涉及的路径再跑。

## 4. 文件边界与接口约定

以下新文件名和接口是本计划的开发目标，并非宣称已经存在；实现前检查同名冲突，按仓库架构守卫拆分职责。

| 位置 | 职责 |
| --- | --- |
| 新 `backend/internal/app/agent_shared_preferences.go`；现有 `cloud_agent_run_create.go`、`cloud_agent_memory_document.go`、`cloud_agent_system_prompt.go` | 偏好服务、按 scope 获取记忆、上下文装配 |
| 新 `backend/internal/model/agent_context_policy.go`、`backend/internal/repository/agent_context_policy.go`；现有数据库迁移入口 | 类型化偏好与 scope 记忆持久化，复用记忆内容规则，增加归属键 |
| 新 `backend/internal/app/agent_definition_registry.go`、`agent_tool_registry.go`、`agent_read_adapter.go` | 配置注册、工具定义与执行绑定、受控读取适配 |
| 现有 `cloud_agent_sdk.go`、`cloud_agent_tools.go`、`cloud_agent_tool_compile.go`、`cloud_agent_tool_dispatch.go`、`cloud_agent_business.go` | 准入、工具编译、执行校验和现有业务实现接入 |
| 现有 `cloud_agent_conversations.go`、`agent_message_queue.go`；repository/model 中同类文件与 `cloud_agent_pi_session.go` | 会话 scope 持久化、目录、历史、归档、队列与恢复 |
| 现有 `backend/internal/handler/agent.go`、`agent_settings.go`、`openapi.yaml` 与 service 别名文件 | HTTP 合同、偏好接口、导出入口；handler 不承载业务规则 |
| 现有 `web/src/sdk/agent/`、`web/src/services/api/agent.ts`、`drama-agent.ts`、`web/src/pages/projects/studio/agent-analysis.tsx` | 配置选择、会话管理、短剧真实交互 |
| 新 `web/src/pages/settings/agent-shared-preferences-pane.tsx`；现有设置页挂载处 | 共享偏好编辑；不在前端解析记忆或决定工具权限 |
| 现有 `agent/src/runner.ts`、`native-skills.ts`、`bridge.ts` 与 harness/contract 制品 | 消费冻结目录、Skill/read 路由、恢复与协议校验 |

跨分片接口约定：

- `AgentContextScope { UserID string; Business AgentBusinessScope }`；UserID 只取认证上下文，不信任请求体。
- `LoadAgentContextSnapshot(scope AgentContextScope, policy AgentContextPolicy) (AgentContextSnapshot, error)`：返回偏好、scope 记忆和规则/技能版本；实现冻结与来源标注，不合并任意旧记忆。
- `ResolveAgentDefinition(businessType, definitionID string) (AgentDefinition, error)`：空 ID 选注册默认值，不接受未注册业务的隐式默认。
- `CompileAgentCapabilities(def AgentDefinition, admission AgentCapabilityAdmission) (AgentCapabilitySnapshot, error)`：只产生已注册且获授权的目录，保存定义版本及适配器/工具版本。
- `ReadAgentContent(ctx context.Context, access AgentToolAccess, input AgentReadInput) (AgentReadResult, error)`：access 来自已认证 Run 与冻结配置，包含 scope 与显式资源授权引用；业务参数不能覆盖它。
- TS SDK 增加 `listConversations(scope, cursor?)`、`listConversationRuns(scope, conversationId, cursor?)`、`archiveConversation(scope, conversationId)`；沿用现有分页约定与 HTTP 路由，不另开重复会话系统。

## 5. 开发分片与验收

顺序：T1 → T2 → T3 → T4 → T5。每片先加入能复现缺口的测试，再实现，再运行相关测试；不要先大范围改生产代码。不得用同一套全量测试反复代替分片证据。

### T1：上下文策略与共享偏好

**交付：** 可编辑的共享偏好、scope 记忆与来源快照；新合同运行不再自动读整份账户记忆/提示词。此片先为当前画布、短剧提供明确策略，T2 再把策略接入注册配置。

**文件：** 第 4 节偏好/记忆/model/repository/handler/设置页；新 `backend/internal/app/agent_context_policy_test.go`、`backend/internal/handler/agent_shared_preferences_test.go`、`web/test/agent-shared-preferences.test.ts`。

- [x] `TestAgentContextIsolation` 验证旧账户、画布 A 记忆与历史标记在模型任务请求中的隔离，以及画布、短剧的共享偏好。
- [x] `TestAgentPreferenceSnapshot` 和 `TestAgentSharedPreferencesHTTP` 验证 Run 快照、revision 冲突与输入拒绝。
- [x] 实现上下文快照、登录用户偏好 GET/PUT 和默认关闭的 scope 记忆。
- [x] 新增账户设置偏好表单，客户端与 API 回读、冲突自动化通过。真实浏览器刷新和交互由 T5 验收。
- [x] 加入独立 Schema 108，SQLite 和隔离 PostgreSQL 验证通过。保留旧记忆。
- [x] 运行计划所列后端和前端专项，证据见[验收记录](2026-11-18-agent-sdk-tools-session-acceptance.md)。

**退出条件：** 同一用户在画布与短剧获得相同输出偏好，但不自动获得对方业务记忆；空偏好正常运行；旧运行快照不漂移。新会话 history 空、scope 记忆的显式共享语义分别验证。

### T2：默认 Agent 配置与工具注册

**交付：** 服务端注册式默认配置；明确空工具列表；能力冻结与执行校验。复用现有工具实现，不复制画布逻辑。

**文件：** 第 4 节 registry、准入、工具编译/派发、运行快照；新 `backend/internal/app/agent_definition_registry_test.go`，更新 `web/src/sdk/agent/contract.ts` 和相关请求适配。

- [x] 编写 `TestAgentDefinitionRegistry`：缺默认配置、缺工具策略、未知工具、重复默认、重复工具名均失败；显式 `[]` 成功；普通不调用 Agent 的业务 API 不受影响。
- [x] 注册 `canvas.default` 和 `drama.analysis`，定义版本从 1 起；短剧保持只读且不继承画布工具。默认配置解析在服务端完成。
- [x] 实现配置/工具注册与能力编译接口；注册对象启动后不可原地修改，同名不同版本必须可识别；当前部署不支持的冻结版本拒绝执行，不默默切换最新版。
- [x] 将平台控制工具、Skill 读取、业务工具分开；按 interactionPolicy 开放提问；模型能力不足时不开放图片读取能力，不扩大为普通文件读取。
- [x] 编写 `TestAgentToolAdmission`：伪造未声明调用、跨 scope 调用、运行中权限撤回被拒绝；配置更新不改变旧 Run 的目录；权限升级只能经过既有受控变更流程且不得超过注册上限。
- [x] 在 backend 执行 `go test ./internal/app -run 'TestAgent(DefinitionRegistry|ToolAdmission)' -count=1`，更新 schema/contract 制品并运行已有工具合同检查。证据见 T2 验收记录。

**退出条件：** 新业务接入时必须注册 Agent 配置；没有业务工具是合法能力集；前端改参数不能获取额外工具。当前画布、短剧各自能力保持预期。

### T3：统一 read 适配与资源边界

**交付：** 注册式文本/图片 read；画布写工具保留业务语义；任务和素材读取不能绕过 scope 隔离。

**文件：** `agent_read_adapter.go`、`cloud_agent_business.go`、`cloud_agent_tools.go`、`cloud_agent_read_tool.go`、`cloud_agent_asset_binding.go`、Pi 相关 read/图片链路；新 `backend/internal/app/agent_read_adapter_test.go`，更新 `agent/test/runner.test.ts`。

- [x] 编写 `TestAgentReadAdapter`：注册后仅处理声明命名空间，拒绝越界、恶意路径、任意 URL、其他 scope 对象；文本分页与图片分页拒绝行为符合现有合同。
- [x] 把三个现有业务 read 路径迁为适配器，使用统一类型化回执，沿用图片加载/预算/持久化回执链路；恢复不得重复执行有副作用的业务调用。
- [x] `TestAgentResourceScope` 验证同一用户其他项目任务、素材和图片也不能仅凭 ID 被读取，当前业务绑定的资源可读。历史图片解绑复验见 T5。
- [ ] 公共模板免绑定需有可信公开标识和显式配置。当前配置关闭这条路径，公共授权单列待验。
- [x] 收紧 `task_get`、素材搜索/读取等旁路的对象范围；保持账户素材库原有访问合同。
- [x] 现有画布写工具接入注册执行器，保留审批、operationId、快照冲突与业务锁；不新增短剧写权限，不新增空壳 edit/write。
- [x] 在 backend 执行 `go test ./internal/app -run 'TestAgent(ReadAdapter|ResourceScope|SDKUnifiedRead)' -count=1`；在 agent 执行 `npm run build` 与 `node --test dist/test/runner.test.js dist/test/bridge-wire-contract.test.js`。证据见 T3 验收记录。

**退出条件：** 画布文本/图片、短剧分集均走统一入口且各自授权；业务图片进入实际模型请求；跨业务资源引用在模型收到内容前失败。已有生成与写入链路不重复计费、不绕过业务服务。

### T4：业务会话目录与完整管理链路

**交付：** 画布、短剧按业务对象创建和恢复会话、查看历史、取消、归档；可交互 Agent 能回答问题。

**文件：** 第 4 节会话/model/repository、handler、TS SDK、短剧 UI；新增 `backend/internal/app/agent_conversation_scope_test.go`、`web/test/agent-conversation-scope.test.ts`。

- [x] 编写 `TestAgentConversationScope`：跨用户、跨业务、同业务不同对象、不同 Agent 配置身份的错误续接/队列/目录请求被拒绝；同对象不同会话不自动合并历史。
- [x] 为会话身份持久化 scope 与 Agent 配置身份，复用现有会话/队列表；Pi 的逻辑 cwd/header 不再用空 canvasId 代表短剧。数据库归属、唯一约束和服务校验保持一致。
- [x] 扩展现有目录、历史与归档接口和 TS SDK；目录按真实 scope 查询，不能扫描全部 StateJSON 再在内存过滤。分页索引随迁移增加。
- [x] 业务请求统一核验 conversation/run 与 scope 绑定，覆盖事件、消息、审批、取消；允许账户管理入口基于用户归属访问自有会话，但不赋予业务 Agent 全账户会话查询工具。
- [x] 短剧 UI 增加会话列表和历史恢复；接通 ask_user 回答及续聊，若某配置仍不支持交互则不暴露提问工具。页面刷新、切换项目时取消旧订阅并清空旧项目展示状态。
- [x] 按第 6 节处理新旧记录、更新 OpenAPI 及前端类型。发布与回退仍需独立验收。
- [x] 运行会话、消息、OpenAPI 和 Web SDK 专项及类型检查。证据见 T4 验收记录。

**退出条件：** 丢失 Run URL 后仍可从业务目录恢复会话；切换画布/短剧项目不显示旧会话输出；服务端拒绝伪造 scope；归档与既有队列运行规则一致，归档不冒充停止运行。

### T5：贯通验收与交付证据

**交付：** 真实双业务闭环、第三业务注册验证、集群恢复及可执行升级/回退记录。

**文件：** `backend/internal/handler/agent_sdk_e2e_test.go`、`agent/test/agent-sdk-driver.mjs`、`web/test/agent-sdk.e2e.test.ts`；新 `docs/plans/2026-11-18-agent-sdk-tools-session-acceptance.md`。

- [x] 第三业务使用仅测试注册的适配器，完成准入→read→模型请求→会话恢复；不得修改核心编译/派发 switch 来接入它。对象授权钩子也必须可注册，不能只让工具名可注册。
- [x] 两用户、两业务、每业务两对象、每对象两会话使用不同标记；检查发给确定性替身的请求、工具回执、历史接口与恢复结果，断言没有越界标记。共享偏好作为明确允许项单独断言。
- [x] 开启 scope 记忆时仅同对象会话共享；关闭时不注入；旧账户记忆和旧自由文本提示词不自动进入新合同会话；图片和压缩摘要同样受隔离约束。
- [x] 本地真实 Chrome 完成偏好编辑、画布看图、短剧分析/提问回答、跨项目切换、列表恢复、取消和归档。模型为确定性替身，证据见 T5 验收记录。
- [x] 确定性模型跑实际 Go+Pi+HTTP，覆盖两个 Worker 争抢、执行中重启、事件序号恢复和单次结算。另有独立协议版本拒绝与工具回执重放用例。D1–D5 目标环境和量测边界见验收记录。
- [ ] 授权条件具备后进行真实视觉模型验证；真实付费调用和部署另需明确授权，缺少条件记为未验，不以 stub 通过代替。
- [x] 运行后端各分片、架构检查、OpenAPI、Worker、Web 专项与构建、文档站构建及发布脚本静态测试。各项实际运行结果见验收记录。

**退出条件：** 下列最终清单全部有对应证据；生产部署若未做，交付标记为“本地验收通过、待部署”，不能标记“已上线”。

## 6. 迁移、发布与回退

1. 首次引入隔离快照版本后，旧运行仍按旧版本读取和排空，不伪造它满足新隔离标准。含旧账户记忆的历史不可直接续接为新隔离会话；保留只读历史，提示创建新会话。不能只清理本轮输入却沿用含污染内容的旧 Pi 历史或摘要。
2. 对可确定归属的旧记录依据持久化 request 迁入 scope 元数据；归属缺失/矛盾的记录保留并标记不可续接，不默认填 canvas。迁移做数量对账、幂等与失败恢复测试。
3. 发布前备份并验证迁移，暂停新准入、排空不兼容运行，再切换匹配版本的后端和 Worker；不得让旧 Worker 消费新能力快照。配置版本撤回后新 Run 拒绝，旧 Run 依赖已保留实现或明确终止，不能替换执行器继续跑。
4. 回退优先关闭新准入并保留新版本的读取/取消/排空路径；保留数据库增量、记忆和事件，不通过删表或回滚用户数据达成二进制降级。执行前写出目标版本能否读取新记录的验证结果。
5. 以上是开发交付要求，不授权当前会话操作服务器、容器或生产环境。所有发布步骤先复盘风险。

## 7. 风险复核重点与最终成型标准

重点输入及负责分片：旧历史/摘要已有跨业务内容（T1/T4/T5）；零工具与未注册工具混淆（T2）；同用户跨项目素材/任务旁路（T3）；配置更新或权限撤回后 Worker 恢复（T2/T5）；页面旧 SSE 回调污染新项目展示（T4/T5）。

- [x] 用户可在账户设置维护共享语言、语气、详略与格式，两个业务的模型请求均带上偏好且偏好不授予权限。
- [x] 启用 Agent 的业务必须注册默认配置；不用 Agent 的业务不受影响；零业务工具合法。
- [x] 画布与短剧的工具集、读取引用和上下文边界由服务端执行；第三业务只需注册实现及授权钩子。
- [x] 统一 read 能读取授权文本，并把授权图片交给视觉模型请求链路；未授权内容在进入模型前被拒绝。替身端点收到了实际 PNG，真实模型效果待验。
- [x] 历史、摘要、工具结果和私有资源不默认跨业务/对象流转；scope 记忆共享需要配置明确启用。
- [x] 两业务有会话目录和恢复路径。短剧通过实际浏览器验证提问、续聊、取消和归档。
- [ ] Worker 切换与重启保持同一冻结能力与上下文；两个 Worker 不造成重复写入/计费。
- [ ] 旧数据保留，旧上下文不被悄悄带入新合同；升级、拒绝不兼容 Worker 和回退路径有证据。

验收台账每项必须填写：测试名称/人工步骤、代码基线和相关差异、环境、命令、日志路径、结果、未覆盖条件。

## 8. 下一会话从哪里开始

先读本文件、第 1 节链接、仓库 AGENTS.md 和当前差异；确认首轮 SDK 改动仍在，再实施 T1。第一条测试应证明“同一用户在两个业务的模型请求中，共享偏好相同、业务标记不跨界”。随后实现最小上下文策略与偏好持久化，不从注册中心或新工作区开始搭框架。

每片完成后在新验收台账记录证据，再进入下一片；用户未要求时不提交或推送。已有 A0–A6 的真实模型、页面、部署待验事项继续保留，T1–T5 不自动将其标为完成。
