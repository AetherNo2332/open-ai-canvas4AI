# Canvas Pi Agent 实施交接

## 任务与边界

继续落实 Canvas Agent 迁移。Pi 是唯一 Agent loop；Go 继续掌握鉴权、模型/计费/画布操作与持久化。当前工作区已有实际代码变更，并已部署到本机 Canary；本文件同时记录路线、边界、验证证据和未完成项。

目标边界：Node 负责 Pi 会话、提示装配、工具披露，以及压缩前后的会话投影；Go 继续掌握鉴权、用户/画布归属、渠道与模型路由、上游请求、计费、画布写入、审批、**原有结构化语义压缩**、数据库和公开 SSE。前端只消费 `/api/agent`，不感知 Pi 内部协议。

## 2026-09-28 已验证状态

- **代码与分支**：检出 `D:\13537\open-ai-canvas-canary`，分支 `canary`，HEAD `cff397fe`。工作树仍有迁移相关未提交/未跟踪内容；本机镜像基于该 HEAD 加工作树内容构建，health 返回 build commit `cff397fe-dirty`。不要把此 build id 当成干净提交。
- **本机 Canary**：Compose 项目 `canvas-canary-3000`，配置由 `docker-compose.yml` + `docker-compose.local.yml` + profile `pi` 组成。仅部署了本机 Canary，没有部署服务器或生产环境。后端/agent token 在运行容器间相同；更新时从既有容器进程环境恢复 Compose 插值，未输出或写出凭据。
- **部署结果**：backend、agent、web 容器均为 running；backend 与 web Docker health 为 healthy。Pi agent 镜像没有 Docker `HEALTHCHECK`，因此用 PID 1 存活检查和容器内到 `backend:8080/api/health/ready` 的请求检查确认 worker 活着且能访问后端。
- **端口与数据库**：`http://127.0.0.1:3000/` 与主 JS 资源返回 200；`/api/health/ready` 返回 `ready=true`。数据库 schema 从 41 迁到 42，`current=42`、`expected=42`、`schema.ready=true`。持久卷 `canvas-canary-3000_backend-data` 保留。
- **回滚备份**：`D:\13537\canary-backups\canvas-canary-3000-data-20260928-032418.tar.gz`，521 项、487491142 字节；SHA-256 `22BEF7E944A3154E90E3EF7F0E1FA3C62454DF45961B6B65E118BD701AFCC68`。旧 image ID 和备份信息在 `D:\13537\canary-backups\canvas-canary-3000-20260928-032418.manifest.txt`。不删除此备份，直到后续迁移验收和回滚窗口结束。
- **已提交的增量**：`dd3b79c8`（SQLite→Postgres 表迁移清单）、`f4dbd15b`（模型步预算闸门）、`547af411`（Pi 插话与视觉账本）、`a2acfb8f`（恢复推理/输出预算）、`cff397fe`（Pi stop reason 策略）。
- **定向测试**：Go `go test ./internal/app -run '^(TestPi|TestCloudAgentPi)' -count=1 -timeout=5m` 通过；Go 的升级预算、截断/拒答处置、工具批次拒绝等新增定向测试通过；Node TypeScript 编译通过，全量 Node 测试 70/70 通过；`go test ./cmd/migrate-sqlite-postgres -count=1` 通过。
- **全量 Go 测试仍红**：`go test ./... -count=1 -timeout=15m` 运行约 449 秒后失败。大量旧 `cloud_agent_*` 测试仍调用已废弃的 Go 推进循环，典型报错 `Agent 运行不存在`；`TestCloudAgentToolTableMatchesRuntimeDispatch` 仍期待旧函数；另有技能路径断言、插件数 99/101、资源 purge 快照残留，以及 Windows 文件权限/SQLite 并发文件锁用例失败。完整失败名和日志在 `%TEMP%\canvas-canary-go-test-20260928.log`。不可把这些失败统称为“环境问题”；应逐项区分迁移后过期的 legacy 测试与真实功能回归，并迁移仍需保留的断言到 Pi 协议测试。
- **UI 验收未完成**：computer-use 表面两次返回 `nodeRepl.fetch request failed`，按其技能指引停止，不用其他 UI 自动化绕过。当前仅完成真实 HTTP 首页、JS 资源和健康路由 smoke；没有完成浏览器中的登录、交互或真实模型调用。
- **CGO 环境**：已安装 MSYS2 UCRT64 GCC，并把 `C:\msys64\ucrt64\bin` 持久加入用户 PATH。当前 Codex 进程仍继承旧 PATH，直接开子 PowerShell 时 `go env CGO_ENABLED` 仍为 `0`；从用户 PATH 刷新当前命令后为 `1`，Pi Go 测试通过。重启 Codex/终端后再验证默认 PATH，不要再把 `CGO_ENABLED=0` 当作缺 SQLite 代码。

下面的合同断点和阶段计划是迁移路线，部分发现来自 2026-09-27 的静态分析；实现者应以当前代码、上述验收结果和本轮 diff 重新核验，不能照搬历史状态。

先读仓库根 `AGENTS.md`，再读以下现有材料，避免重写已做的规划：

- `agent/PI_CODING_AGENT_MIGRATION_ROADMAP.md`：完整迁移阶段、数据模型和 Web/Agent/Backend 验收矩阵。
- `agent/PI_MIGRATION_P0_BASELINE_AND_CONTRACTS.md`：实测基线与三端合同；其中测试数比旧 Daily 更新。
- `agent/PI_MIGRATION_P1_SDK_EXTENSION_POINTS.md`：`@earendil-works/pi-coding-agent@0.87.1` 公开 SDK 扩展点的真实探针。
- `agent/PI_MIGRATION_GAPS.md`、`agent/PI_MIGRATION_DAILY_2026-09-27.md`：历史问题与进度；其中部分计数已过期，需以代码和最新测试为准。

## 当前工作区事实

- 交接时位于 `canary`，HEAD 为 `7aa99880`。工作树有大量他人未提交改动及新增文档、Harness 和测试。**先重新执行 `git status --short`；不得清理、回滚或覆盖这些改动。** 本交接只新增本文件。
- `agent/package.json` 已锁定 `pi-agent-core`、`pi-ai`、`pi-coding-agent` 的 `0.87.1`；生产 `agent/src/runner.ts` 仍构造 `pi-agent-core` 的 `Agent`。Coding Agent SDK 的真实探针在 `agent/test/pi-sdk-probe.test.ts`，不能把依赖安装当作运行时切换完成。
- 当前新 run 的 `agentEngine` 固定为 `pi`；旧 Go `advanceCloudAgentByID` / `advanceCloudAgent` 已标废弃，但大量旧测试仍依赖它们，导致全量 Go 套件不能通过。复查 Compose profile 和 `backend/cmd/server/main.go` 的启动检查；如果 engine 配置仍漂移，单独解决，不要悄悄回退旧循环。
- `agent/harness/` 当前是同步策略、工具描述和 schema 的制品目录；运行时提示还需与 Go 侧策略保持一致。工具 schema 为版本化制品，服务端仍是权限与工具参数的权威来源。
- 下文发现属于历史静态追踪。已执行的测试与本机容器状态以本节为准；本轮仍未验证真实上游模型信封、PostgreSQL 多进程并发或端到端浏览器操作。

## 必须消除的合同断点

| 优先级 | 发现与代码入口 | 完成条件 |
| --- | --- | --- |
| P0 | `backend/internal/app/cloud_agent.go` 在创建 run 时就提交根模型任务；`agent/src/runner.ts` 领取后才装配 Harness，`PiModelStep` 遇到活动根任务只返回其结果。首步未使用 Node 最终提示。`agent/src/system-prompt.ts` 又追加 `SYSTEM_POLICY.md`、`MEDIA_POLICY.md`，与 Go 已编译的策略可能重复。 | 首步和后续步骤采用同一版本化提示合同；系统/媒体策略只出现一次；工具权限不能被 `SYSTEM.md` 或工作区文件覆盖；提示和工具 schema 哈希随 run 冻结并可恢复。 |
| P0 | `web/src/lib/model-capabilities.ts` 用 `text.thinking`，面板据此显示推理模式；`backend/internal/app/model_capability.go` 的 `TextCapabilityConfig` 无该字段。 | 管理端编辑、API、Go 存储、公开目录和面板使用同一字段及默认值；不支持时拒绝或降级有明确合同。 |
| P0 | `backend/internal/app/task_creation.go` 从客户端配置剔除 `capabilityConfig`，这是正确的信任边界；但 `backend/internal/app/provider.go::resolveProviderConfig` 未将**选中渠道模型**的权威文本能力补回。执行路径依赖它约束 SSE 与最大输出。 | 路由确定后从 DB 读取并附上权威能力快照；每次重路由重取；真实上游请求遵守 streaming、maxOutputTokens、vision/references、thinking 合同，客户端伪造能力无效。 |
| P0 | `enqueueCloudAgentTask` 在 Pi 路径仍发每步 `context_pressure`；`recordCloudAgentTokenAnchor` 与原有 `cloudAgentRequestCompaction` 目前只从旧 Go 推进路径调用。`web/src/lib/canvas/agent-context-usage.ts` 却把 85% 读数写成“下一次调用前会压缩”。 | Pi 每步完成后读取该用户模型任务的实测 usage 并校准下一步；发请求前按同一读数触发**原有 Go 结构化语义压缩**，将其结果写入 Pi 活动上下文，下一次真实请求使用该检查点。不得让 Pi 默认摘要另压一次。接通前修正 UI 文案。 |
| P1 | `cloud_agent_context_budget.go::cloudAgentRouteIntersectionBudget` 分别取最小窗口和最小输出预留，可能高估最小窗口路由的输入容量。 | 对每条合格路由单独计算可用输入，取最小可用值；窗口未知与无可用路由有明确保守处理。 |
| P1 | Node `canvasModel()` 暂填 1,000,000 窗口、32,768 最大输出；Go/前端使用数据库窗口预算。`cloud_agent_context_pressure.go` 的字节/消息兜底还从 `state.Canonical.Messages` 读数量，Pi 检查点可能更长。 | Pi 模型元数据来自 Go 选中模型能力；压力事件以即将发送的真实信封和 Pi 活动上下文为准；未知窗口不显示百分比。 |

## 实施顺序与每步验收

### 1. 固定首步信封与提示来源

先写固定输入的“首步/第二步最终信封”金样本测试，记录系统提示分段、工具名及参数哈希、模型路由、能力版本和缓存键，禁止记录密钥或完整私有提示。随后决定并实施单一准入流程：首选 Go 只创建运行与预授权，Node 领取后发第一笔模型步骤；迁移根任务时同时保持幂等 ID、计费和旧终态可读。若保留根模型任务，必须先让其使用与 Node 完全一致的版本化提示制品。

验收：第一、第二模型步均包含相同不可变策略；Harness 仅按规则追加一次；新增 `SYSTEM.md` 不会消除强制策略；worker 重启仍使用 run 创建时的提示/schema 快照；根任务不重复计费。

### 2. 打通模型渠道能力

以 `backend/internal/model/models_channel.go`、`backend/internal/app/channel_models.go`、`model_router.go`、`task_creation.go`、`provider.go`、`provider_text.go` 为链路。先统一 `thinking` 字段及校验；再在最终渠道模型和价格档选定后注入服务端能力快照，不恢复对客户端 `capabilityConfig` 的信任。检查 Chat Completions、Responses、Claude 和声明式适配的输出上限、流式、推理与视觉参数，检查路由重试的能力变化。前端能力编辑入口为 `web/src/components/model-capability-editor.tsx`，展示入口为 `canvas-cloud-agent-panel.tsx`。

验收：管理员保存的能力经 DB → 目录 → 前端 → 准入 → 真实上游请求可追溯；客户端伪造能力被忽略；不同路由的能力/价格与实际任务一致；推理开关不会仅因前后端字段不一致而消失。

### 3. 让 Pi 路径拥有真实的上下文治理

在每个 Pi 模型任务结束并已记录 `ApiCallLog` 后执行 usage 定锚；无 usage、异常比率、模型/渠道/工具变更时明确回退估算。模型步骤**准入前**计算真实 canonical 的输入预算，并按 85% 线或窗口未知时的字节/条数线决定压缩。

**保留原有压缩方式作为唯一生产摘要策略**：将 Pi 活动分支投影交给 Go 的 `agentcontext.BuildPrompt` 与 `cloud_agent_context_compaction` 独立模型任务；用 `agentcontext.Parse` 校验结构化检查点，将操作历史与待办任务改用服务端事实，失败时使用 `cloudAgentFallbackCheckpoint`，再按 `cloudAgentBoundCheckpoint` 和 `cloudAgentCompleteTurnTail` 保留最近完整轮次。摘要任务继续由 Go 路由、计费和持久化。`session_before_compact` 可作为 SDK 接入点，返回这份服务端检查点组成的自定义 `CompactionResult`，让 Pi 保存压缩边界与活动分支；**不采用 Pi 默认摘要策略，也不让 Go canonical 与 Pi session 各自独立压缩**。如果 SDK 接入点不能忠实表达旧检查点及保留边界，先用真实 0.87.1 探针证明限制，再选择受控 context edit；不得静默换算法。

压缩前确认 tool call/result 成对且没有待审批/媒体回执；压缩后同步 Pi 活动上下文与 Go 检查点、保留 `context_compaction_requested`、`context_transition`、`context_compacted` 事件和原有失败保底语义。保底检查点仍超预算时停止下一模型准入，不能盲发。

验收：两条逻辑路由（例如窗口/预留分别为 512k/64k 与 1M/16k）不会得到高于 512k 路由实际可用值的预算；阈值前显示估算/实测来源；达到阈值先发起原有语义压缩再发下一步；检查点包含创作锚点、偏好、决策和服务端执行事实，最近完整轮次原样保留；模型摘要失败走原有保底检查点；压缩后下一请求真实变短；重启后恢复的信封和事件序号一致；未完成工具调用不被裁掉、不会额外发生 Pi 默认摘要调用。

### 4. 收敛前端文案与运行配置

面板父子组件共享同一份模型能力推导，保留当前能力读取失败不白屏的处理。上下文环要分别表达“本地估算”“上游实测校准”“达到压缩线”“正在压缩”“压缩失败”，不得把环满格当成模型窗口耗尽；只有实际接通压缩后才宣称下一步会压缩。清理 Compose 与启动校验的 `legacy` 漂移；Pi 服务不可用时公开 run 要保持可解释的排队或失败状态，不暗转旧循环。

验收：真实页面的模型选择、推理开关、压力环、刷新后事件回放、取消/审批/失败终态均符合公开 `/api/agent` 合同；无内部 Pi 字段进入前端。

## 测试门槛与交付记录

- **Agent**：真实锁定版 SDK，固定假 Go Bridge；覆盖首步、动态母/子工具、usage、原有结构化检查点映射到 Pi 会话、恢复、取消、审批等待及另一 run 不被阻塞；断言没有 Pi 默认摘要的第二次模型调用。不要用 mock Agent 循环代替 SDK。
- **Backend**：SQLite 与 PostgreSQL 验证渠道能力、路由重试、计费幂等、画布批次原子性、双 worker 租约、上下文压缩前置和 SSE 单调序号；调用真实 handler/app/repository 路径。
- **Web**：专项状态测试及真实应用浏览器测试；覆盖模型能力缺失、推理选项、压力状态、断线续传、刷新、跨用户隔离和终态；不要用静态仿页面代替。
- 每项修复记录：源码 commit/工作树差异、测试命令与原始结果、数据库迁移版本、脱敏信封哈希、未覆盖风险。旧文档中的测试通过数不替代本轮重跑结果。任何数据库、API 或 SSE 合同变化，同步仓库专题文档。
- 本机 Canary 已部署；没有生产部署。下一位应先解决 legacy Go 测试的迁移策略和其余真实失败，再完成浏览器交互测试、PostgreSQL 多 worker/跨进程验收与真实上游信封验证。不要因当前容器 ready 或局部单测通过宣称迁移完成。

## 建议使用的技能

- `tdd`：为信封、路由能力、压缩与恢复先写能复现断点的合同测试。
- `diagnose`：集成测试出现跨进程、并发或偶发失败时按复现、缩小、定位、修复、回归的顺序处理。
- `handoff`：若工作再次跨会话，更新本文件的已完成项与证据链接，并引用已有路线图，不复制长篇计划。
