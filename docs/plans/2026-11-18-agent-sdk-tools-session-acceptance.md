# Agent SDK 工具注册与上下文隔离验收

2026-11-18 至 2026-11-19。本地工作树 `agent-sdk-main-20261115`，HEAD `2d0a50d446db689d575512122cc3fd33def4cc08`，包含首轮 Agent SDK 的未提交实现。本次基线状态与已跟踪差异记录在 `.local/agent-sdk-tools-session/baseline-status.txt` 和 `baseline.patch`。

## T1 · 上下文策略与共享偏好

### 代码

新 Run 在 Go 准入时读取并冻结 `language`、`tone`、`verbosity`、`format` 和服务端 `revision`。账户设置接口使用登录用户和条件更新，已占用的 revision 返回 `agent_preferences_conflict`。空设置正常启动，新的续聊轮次读取最新 revision。

已有账户记忆与自由文本提示词继续通过原设置入口维护。新隔离 Run 只读取当前业务规则及声明启用的业务记忆。当前画布和短剧的默认策略均关闭业务记忆。启用时以用户、业务类型和对象 ID 为主键，同对象会话共享记忆文档，各自保有独立历史。文档变更与调用回执提交在同一事务。

旧 Run 可以读取、按原键重放及排空。尝试用旧上下文创建续聊轮次返回 `agent_context_new_conversation_required`，防止旧历史和摘要进入新合同。同一套规则适用于处理存量未隔离会话的后续工作。

### 自动化

环境为 Windows、Go、Bun、SQLite 临时库，以及 WSL 中只绑定回环地址的 PostgreSQL 14 临时测试库。日志目录 `.local/agent-sdk-tools-session/`。

| 核查 | 命令和证据 |
| --- | --- |
| 红测 | `t1-context-red.log` 确认旧账户提示词及记忆进入短剧模型任务。`t1-red.log` 的 HTTP 端点返回 404。`t1-web-red.log` 的客户端模块尚未实现 |
| 新合同 | 在 `backend` 运行 `go test ./internal/app ./internal/handler -run 'TestAgent(ContextIsolation\|PreferenceSnapshot\|SharedPreferences)' -count=1 -v`。`t1-go.log` 的三个用例通过，覆盖两业务共享输出偏好、隔离旧账户及其他业务对象标记、默认关闭和偏好 revision 冻结。后续回归加入了新轮续聊验证 |
| 数据库 | 在 `backend` 运行 `go test ./internal/database -run '^Test(AgentContextPolicyMigration\|CurrentSchemaVersionMatchesMigrationPlan)$' -count=1 -v`。`t1-db.log` 显示 SQLite 与 PostgreSQL 均通过，并发 revision 0 只有一个写入成功，跨用户及跨业务类型读不到 scoped 记忆，重复迁移保留数据 |
| 设置客户端 | 在 `web` 运行 `bun test test/agent-shared-preferences.test.ts` 和 `bun run typecheck`。`t1-web.log` 与 `t1-web-types.log` 通过，覆盖当前 revision 保存、回读及冲突传递 |
| 架构与路由 | `go test ./internal/architecture -count=1 -v` 通过，见 `t1-architecture.log`。`TestOpenAPIRouteContract` 通过，见 `t1-regression-final.log` |
| 应用回归 | `go test ./internal/app -run 'Test(AgentContextIsolation\|AgentPreferenceSnapshot\|AgentRules\|AgentSDK\|UserAgentSystemPrompt\|MemoryUpdate\|CloudAgent.*(Memory\|Continu\|Rule)\|AgentMessageQueue)' -count=1 -v` 通过，见 `t1-app-final.log`。包括隔离会话续聊、现有消息队列、旧记忆排空和当前画布规则冻结 |
| 既有跨进程回归 | `TestAgentSDKRealHTTPWorkerRestart` 通过，见 `t1-regression-final.log`。本地 Go、Node/Pi 与确定性 HTTP 端点完成四次模型调用和计费订单，验证图片字节和 Worker 接管。T1 隔离用例检查的是提交给模型任务的请求正文 |

### 外部验收

| 层级 | 状态 |
| --- | --- |
| 设置页面刷新、冲突交互 | 待真实浏览器验收 |
| 真实视觉与文本模型 | 待验，尚未发起真实模型或付费调用 |
| 部署、排空及回退演练 | 待验，代码留在本地工作树 |

## T2 · 默认 Agent 配置与工具注册

服务端登记 `canvas.default` v1 和 `drama.analysis` v1，注册项保存对象授权函数。准入冻结配置版本、工具执行器版本与能力上限，执行前重新核验对象权限。显式空业务工具集合法。短剧分析保持只读，提问暂未向短剧开放。Node/Pi 支持无 `read` 的运行，Go 和 Worker 的配套协议升为 `canvas-pi-wire/v6`，工具制品升为 v24。

| 核查 | 命令和证据 |
| --- | --- |
| 红测 | `.local/agent-sdk-tools-session/t2-red.log` 记录注册和准入合同缺口 |
| 注册与准入 | Windows 上在 `backend` 运行 `go test ./internal/app -run 'TestAgent(DefinitionRegistry\|ToolAdmission)' -count=1`，通过，见 `t2-go.log` |
| 工具与版本合同 | Go 工具制品、权限、架构、OpenAPI 和旧 wire 拒绝检查通过，见 `t2-contract-final.log` |
| Worker | 在 `agent` 运行 `npm run build` 和 `node --test --test-force-exit dist/test/runner.test.js dist/test/bridge-wire-contract.test.js`。构建通过，89 个用例通过，见 `t2-node-build.log`、`t2-node-final.log` |

真实浏览器、真实模型及部署待验。T3 接续验证注册式 read 执行和资源范围，T4 接续验证短剧提问与回答。

## T3 · 统一 read 适配与资源边界

> **已被取代（2026-11-25）：** T3 验收的"统一 read"命名空间注册表已在功能回退中撤除。业务 `read` 恢复为只读 Skill 文本文件，画布正文、识图与短剧分集改用具名工具，授权、分页、预算与持久回执语义保持不变。取代计划与记录见 [回退计划](<../superpowers/plans/2026-11-25-agent-skill-read-business-tools.md>) 和[验收记录](2026-11-25-agent-skill-read-business-tools-acceptance.md)；下文保留为历史证据。

画布正文、画布图片和短剧分集通过同一个命名空间注册表执行。Run 冻结适配器版本，工具调用沿用已有批次、读取预算、图片交付和检查点回执。新增命名空间可通过注册接入。图片引用在创建模型任务前验证当前画布绑定与资源属主。任务按来源 Run 或画布任务绑定授权，素材按当前画布节点绑定授权。素材替换在生成审批预览与执行时分别检查，继续使用原有快照和变更记录。

| 核查 | 命令和证据 |
| --- | --- |
| 红测 | `t3-red.log` 复现第三业务误入 Skill 路径、图片接受显式分页、同 ID 跨业务任务与账户素材泄露。`t3-image-red.log` 复现未绑定图片被模型步骤接受 |
| 专项 | Windows 上在 `backend` 运行 `go test ./internal/app -run 'TestAgent(ReadAdapter\|ResourceScope\|SDKUnifiedRead)' -count=1 -v`，三个测试通过，见 `t3-specialized.log` |
| 回归 | `t3-go.log` 记录 Go 工具、审批和视觉回归，以及 `TestAgentSDKRealHTTPWorkerRestart`。Go 与替换后的 Pi Worker 完成两业务、四次计费模型请求，替身端点收到 PNG 字节。一个既有独立视觉批次测试在 Windows 跳过 |
| Worker | 在 `agent` 运行 `npm run build` 和 `node --test --test-force-exit dist/test/runner.test.js dist/test/bridge-wire-contract.test.js`。89 个用例通过，见 `t3-node-build.log`、`t3-node.log` |

账户素材库接口保持原有浏览权限。本地测试验证了账户私有素材与当前画布绑定素材的区别。T5 补验了历史图片解除绑定后的拒绝行为。公共素材免绑定未开放；其余跨进程、页面和外部验收见 T5。

## T4 · 业务会话目录与管理

迁移 109 为 Run 和 Pi 会话保存业务类型、对象、配置身份与上下文版本。目录通过数据库索引分页。旧请求可确定归属时可以在对应目录查看，混合或矛盾归属留给账户历史。短剧的 Pi cwd 使用实际项目范围。新短剧 Run 采用支持提问的 `drama.analysis` v2，已有 v1 保留原能力。短剧页面可选择会话和历史轮次、回答、续聊、取消和归档。带业务范围的事件、运行和管理路由先校验该范围。

| 核查 | 命令和证据 |
| --- | --- |
| 红测 | `t4-red.log` 复现不同配置身份的消息被接收、短剧 Pi cwd 为 `canvas://`。`t4-web-red.log` 确认 SDK 缺少目录方法 |
| Go | `go test ./internal/app ./internal/handler ./internal/repository -run 'TestAgentConversationScope\|TestCloudAgentConversation\|TestAgentMessage\|TestOpenAPIRouteContract\|TestAgentRunReadQueryMatchesOpenAPI' -count=1 -v` 通过，见 `t4-go-verified.log`。包括跨用户、跨类型、跨对象、跨配置、独立会话、分页、归档及并发续聊 |
| 迁移 | `go test ./internal/database -run '^Test(AgentConversationScopeMigration\|CurrentSchemaVersionMatchesMigrationPlan)$' -count=1 -v`，SQLite 与本地 PostgreSQL 14 通过。`t4-db.log` 记录归属回填、矛盾数据保留、Pi header 和迁移重跑 |
| Web | `bun test test/agent-conversation-scope.test.ts test/agent-sdk.test.ts test/agent-api-reliability.test.ts` 运行 37 个测试，`bun run typecheck` 通过，见 `t4-web-verified.log`、`t4-web-types-verified.log` |

当前记录为代码与专项自动化通过。真实页面交互、供应商模型及部署待验。

## T5 · 贯通验收与文档站

### 代码与回归

画布恢复历史和每页事件时继续携带目录的 `canvasId`，队列读写也携带同一范围。短剧没有设置剧本专用模型时，分析沿用短剧制作文本模型。Worker 持有效签名但租约已结束的请求返回 `403 agent_lease_lost`，签名无效、过期、撤销或归属不符仍拒绝。测试 Driver 使用与正式 Worker 相同的单会话错误处理入口。

| 核查 | 命令和证据 |
| --- | --- |
| 新增回归 | `t5-canvas-history-red.log` 复现目录范围在历史补页丢失。`t5-browser.log` 复现空的短剧剧本模型覆盖。`t5-renew-red.log` 复现回答入队时，上一轮续租被报成 401 |
| 注册与恢复 | `go test ./internal/app -run '^TestAgentSDKThirdBusinessReadAndRecovery$' -count=1 -v`。仅测试注册 `library.reader`、对象授权和读取适配器，验证拒绝越权、模型任务输入、同一 Pi 会话接管、回执重放只执行一次。此用例在 Go 中驱动模型任务，证据见 `t5-third-vision.log`、`t5-gates-final.log` |
| 权限回收与协议 | `go test ./internal/identity/worker ./internal/app ./internal/handler ./internal/architecture -run 'Test(Grant\|AgentRunGrant\|InternalAgent\|WorkspaceArchitecture\|AgentResourceScope\|AgentGrantAdapter\|OpenAPIRouteContract\|AgentRunReadQueryMatchesOpenAPI)' -count=1 -v` 通过，见 `t5-lease-and-revocation.log`。包括删除画布节点后历史图片引用被拒、失效租约和无效凭据、v4/v5 Worker 拒绝、OpenAPI 和架构守卫 |
| 实际 Go + Node/Pi + HTTP | 隔离 PostgreSQL 14 上，在 `backend` 运行 `go test ./internal/handler -run '^TestAgentSDKRealHTTPWorkerRestart$' -count=1 -v`，以 `TEST_AGENT_POSTGRES_DSN` 指向测试库并使用独立 schema。两用户、两业务、八对象、十六独立会话，确定性模型 HTTP 端点收到 38 次请求，数据库有 38 笔结算，收到一份 PNG，越界标记为零。覆盖断点接管、重复请求、事件按序号恢复、旧 epoch 拒绝和提问回答。见 `t5-http-final.log` |
| 调度、压缩和工具回执 | `t5-gates-final.log` 记录 PostgreSQL 调度事件事务、容量与 draining、租约回收、`TestPiToolBatchReplaySafety` 与 `TestPiContextCompaction*`。`t5-node-final.log` 记录 Worker 202 个测试通过，1 个 Windows 跳过，包括订阅与持久读取先后、重复通知、断线游标及容量恢复 |
| Web | `bun test test/cloud-agent-history.test.ts test/agent-conversation-scope.test.ts test/agent-sdk.test.ts test/agent-api-reliability.test.ts test/agent-shared-preferences.test.ts` 58 通过。受影响文件 ESLint、`bun run build` 通过。证据见 `t5-web-final.log`、`t5-web-eslint-final.log`、`t5-web-build-final.log` |
| 文档站 | `docs` 中 `bun run types:check` 与 `bun run build` 通过，产出 61 个静态页面。本地预览 `/docs/backend/agent-sdk` 返回 HTTP 200，核对了页面标题。新增业务接入说明，更新 HTTP API、迁移、账户偏好、画布与短剧用户说明。见 `t5-docs-types.log`、`t5-docs-build.log` |
| SQLite → PostgreSQL 复制工具 | `go test ./cmd/migrate-sqlite-postgres -count=1 -v` 通过，见 `t5-copy-migration.log`。全量首跑发现复制清单遗漏三张 Schema 108 的表；已补登记并在临时库验证表清单、重复迁移、失败事务回滚和 CLI 结果。未操作生产数据库 |
| 合同回归 | 全量首跑还有六项旧夹具断言失败，已对齐显式技能、旧上下文仅可读、Run/Pi 会话归属合同；原有技能冻结、技能预算拒绝与摘要续聊断言继续覆盖。`t5-app-followup.log` 复验通过 |
| Go 应用全量分片 | 用 `node ../scripts/go-test-shard.mjs 1` 至 `4` 并行执行仓库应用四分片，四片均通过。见 `t5-app-shard-1.log` 至 `t5-app-shard-4.log`。首跑单包触发 10 分钟超时，其中任务单独复验通过。`go vet ./...` 通过 |
| Go 其余包 | `node ../scripts/go-test-shard.mjs rest` 通过，包含最终版跨进程冒烟、数据库、HTTP 和架构检查。见 `t5-go-rest.log` |
| 摘要和业务隔离 | `go test ./internal/app -run '^(TestCloudAgentCompactionContinuationIncludesFinalReplyWithoutReplayingPrompt\|TestAgentContextIsolation\|TestAgentConversationScope.*\|TestAgentResourceScope)$' -count=1 -v` 通过，见 `t5-scoped-compaction-final.log`。覆盖摘要续聊保留同一 Pi 树、同一业务对象的新会话不继承摘要、跨业务拒绝续接及图片绑定撤回 |
| 部署脚本静态测试 | `python -B -m unittest scripts.test_production_deployment scripts.test_agent_parity_container_commands scripts.test_production_agent_smoke scripts.test_install_agent_worker_id` 42 通过，见 `t5-deploy-static.log`。此结果不代表已启动或部署容器 |

SQLite 首轮双 Worker 冒烟遇到写锁竞争，使用 PostgreSQL 独立 schema 验收并延长观察窗口。此前 `t5-http-postgres.log` 虽完成两项客户端断言，但有替换 Worker 在旧轮结束后续租收到 401 的异常退出。`t5-http-final.log` 为修复后的复验。复验仍可见工具操作遇到版本竞争后由持久批次重试成功，38 次模型请求与 38 笔结算相符。

### 实际页面

使用本机 Chrome Headless、正式 `web/dist`、临时 Go API 和 Node/Pi Worker。浏览器脚本为 `agent/test/agent-sdk-browser-smoke.mjs`。从 `backend` 运行上表的 Go 命令前，额外设置 `AGENT_SDK_BROWSER_SCRIPT` 为该脚本绝对路径。非默认 Chrome 路径可用 `AGENT_SDK_CHROME` 指定。页面通过后，Go 另行断言确定性端点收到一张 PNG。

`t5-browser-pass.log`：画布打开助手并提交看图，页面显示结果，端点收到一张 PNG；账户偏好保存后重新加载仍一致；短剧新建分析、显示提问、自定义回答、切换项目不显示旧输出、从会话列表恢复、归档回读、取消后状态变更均通过。仍属本地 Chrome 加模型替身，不代表供应商模型、生产页面或其他浏览器已验收。

### 真实模型、调度与部署

| 项目 | 核查边界 |
| --- | --- |
| 真实文本与视觉供应商 | 待验。模型调用全程使用本机确定性替身 |
| 画布素材授权 | 私有素材按画布节点绑定验证。现有账户素材模型只有属主，没有可信公共标志，当前注册配置没有开放公共模板免绑定访问 |
| D1、D2 | 持久事件、订阅竞态、重放去重及恢复有专项证据。实际 Worker 通知缩短领取时延及丢通知补偿的量测待验 |
| D3、D4 | 本地双 Worker 抢占和旧 epoch 拒绝已验；容量及 draining 由 Go 和 Node 专项覆盖。目标环境 SSE 反向代理、容量满后实际积压恢复待验 |
| D5 | Worker 扩容下的事件查询、无效领取、数据库锁等待和领取延迟待测 |
| 升级与回退 | SQLite 和 PostgreSQL 的 108/109 向前迁移、存量保留、迁移重跑、旧上下文续聊拒绝、旧 Worker 拒绝已验。文档站给出排空、配套切换与保留数据库的回退步骤。目标部署、旧二进制读取新数据和实际回退仍待验 |

本次变更留在本地工作树。
