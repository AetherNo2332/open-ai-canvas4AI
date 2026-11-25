# Agent Skill-only read 与业务专用工具验收记录

依据计划：[2026-11-25-agent-skill-read-business-tools.md](<../superpowers/plans/2026-11-25-agent-skill-read-business-tools.md>)。

本文件记录实施基线、每项任务的命令与真实输出位置、未覆盖项。未执行或未覆盖的检查保持「待验」，不写成通过。

## 1. 实施基线

记录于任务 0 开始时。

| 项 | 实际值 |
| --- | --- |
| 工作树 | `.local/worktrees/studio-world-model-port` |
| 分支 | `codex/studio-world-model-port` |
| HEAD | `64bf5a05b6ddef527d507b5780853e02f441a6bd` |
| 计划基线 HEAD | `64bf5a05b`（与计划一致） |
| 最近提交 | `64bf5a05b` → `c51373793` → `d1f48fb3e` |
| Go | `go1.27.1 windows/amd64`（`backend/go.mod` 要求 `go 1.25.0`） |
| Node | `v26.1.0`（`agent/package.json` 要求 `>=22.19.0`） |
| Bun | `1.3.9` |
| Pi SDK | `@earendil-works/pi-agent-core` / `pi-ai` / `pi-coding-agent` 均为 `0.87.1`（锁文件固定） |
| `agent/node_modules` / `agent/dist` / `web/node_modules` | 均存在；`agent/dist` 在每次改动后重新构建 |

### 已有工作区修改（非本次产生，已保留未触碰）

| 路径 | 状态 | 归属 |
| --- | --- | --- |
| `backend/internal/model/models_canvas_history.go` | 已修改 | 用户原有，本次未编辑 |
| `backend/internal/canvas/repro_studio_migration_test.go` | 未跟踪 | 用户原有，本次未编辑 |
| `docs/superpowers/plans/2026-11-25-agent-skill-read-business-tools.md` | 未跟踪 | 本次计划文档 |

## 2. 基线源码事实（任务 0 核对）

| 事实 | 位置 |
| --- | --- |
| `AgentDefinition.ReadAdapters` / `readAdapters` / 注册器参数 | `agent_definition_registry.go` |
| `AgentCapabilitySnapshot.ReadAdapterVersions` | `agent_tool_registry.go` |
| `agentReadAdapter`、`agentBusinessRead`、`agentReadBusinessContent`、三适配器 | `agent_read_adapter.go`（已删除） |
| 业务命名空间描述 | `cloud_agent_tools.go`（原 `cloud-agent-tools/v29`） |
| `agentScopedReadToolSchema` 拼装业务路径 | `agent_tool_catalog.go`（已删除） |
| Worker 业务 read 分支 | `agent/src/runner.ts`（4 处）、`agent/src/native-skills.ts`（`isBusinessRead`） |
| Go 批次放行/过滤业务 read | `cloud_agent_pi_tool_batch.go` |
| 检查点业务 read 豁免 | `cloud_agent_pi_checkpoint.go` |
| 短剧 `read` 提示与 `readPath` 上下文 | `cloud_agent_run_create.go` |
| 短剧读取执行体 | `cloud_agent_business.go`（`agentReadDramaEpisode`） |
| 能力目录版本 1、内部协议 `canvas-pi-wire/v7`、Schema `cloud-agent-tools/v29` | `cloud_agent_capability_budget.go`、`handler/internal_agent.go`、`cloud_agent_tools.go` |

### PR149（`bf0718c7a`）受保护实现：当前状态

| 代码位置 | 状态 |
| --- | --- |
| `agent/src/native-skills.ts` `resolveSkillAlias` | 保留：nativeName 优先、唯一 id/displayName 匹配、歧义报错 |
| 同文件 `resolveSkillPath` | 保留：先别名再冻结白名单，缺失时列可用名称/文本文件 |
| 同文件 `rebaseNativeSkillPath` | 保留：旧绝对路径重定位后仍走授权与路径校验 |
| Go `nativeSkillReadAlias` / `nativeReadCheckpointCall` | 保留（本次只删除同一函数内针对 `agentBusinessRead` 的提前放行分支） |
| `nativeSkillReadToolSchema` 的 `available_skills` 原值指引 | 保留并新增断言 |
| 别名/错误/恢复/检查点断言 | 全部通过，见 §5 |

## 3. 任务状态

| 任务 | 状态 | 主要证据 |
| --- | --- | --- |
| 0 · 实施记录 | 完成 | 本文件 |
| 1 · `drama_read_episode` 贯通 | 完成 | §4.1 |
| 2 · SDK 移除业务 read 扩展 | 完成 | §4.2 |
| 3 · Worker/批次/检查点恢复 Skill-only | 完成 | §4.3 |
| 4 · 目录与协议发布切换 | 完成 | §4.4 |
| 5 · 正文与识图公共链路回归 | 完成（真实模型待验） | §4.5 |
| 6 · 端到端、前端与文档 | 完成（真实模型/页面待验） | §4.6 |

## 4. 执行记录

所有 Go 命令在 `backend/` 执行，Node 命令在 `agent/`，Web 命令在 `web/`，文档命令在 `docs/`。

### 4.1 任务 1 · `drama_read_episode`

实现要点：

- 新建 `backend/internal/app/cloud_agent_drama_read.go`：`cloudAgentDramaEpisodeReadArgs`（`episodeId`、`offset`、`limit`）、`cloudAgentDramaEpisodeReadResult`（`projectId`、`episodeId`、`content`、`contentType`、`visibility`、`offset`、`nextOffset`、`totalRunes`、`hasMore`、`adopted`）与分页校验。缺省 `offset=0`、`limit=12000`；显式 `0`、负数、超上限与非整数都拒绝；超过末尾的 `offset` 返回末尾空页。
- 注册：`cloud_agent_tool_compile.go` 在 `agentBusinessScope(req).Type == "drama"` 时 `add("drama_read_episode", …)`；`agent_tool_catalog.go` 的 `builtinAgentToolPolicy` 把业务限定为 `drama`；`agent_tool_catalog.go` 新增 `cloudAgentSchemaArtifactRequests` / `cloudAgentUnionTools`，离线全集是各业务工具表的并集并去重平台工具；`CloudAgentSupportedToolNames()` 改为同一并集。
- 读取链路：`cloud_agent_read_tool.go` 分派、`cloud_agent_read_cache.go` 计入共享读取次数、`cloud_agent_tool_dispatch.go` 在检查点事务外准备结果并在事务内只结算已有结果、`cloud_agent_tool_categories.go` 归入只读分类、`cloud_agent_tool_feedback.go` 提供参数纠错示例。
- 短剧定义改为 `BusinessTools: ["task_get", "drama_read_episode"]`；系统提示与运行上下文改用具名工具，移除 `readPath`，改为 `episodeId` + `readTool`。

命令与结果：

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/app -run '^TestDramaReadEpisode' -count=1 -v` | 通过（工具/授权/分页/预算/回执四组子测试） |
| `go test ./internal/app -run 'TestDramaReadEpisode\|TestAgentSDKDrama\|TestCloudAgentToolTableMatchesRuntimeDispatch\|TestAgentToolSchemaArtifact\|TestAgentToolCatalogMatchesFrontendNames' -count=1` | 通过 |
| `WRITE_TOOL_SCHEMA=1 go test ./internal/app -run TestAgentToolSchemaArtifactMatchesRuntime` | 已重写 `agent/harness/TOOL_SCHEMA.json`（37 个工具，`cloud-agent-tools/v30`） |

`TestDramaReadEpisodeBudgetAndReplay` 的真实模型步骤断言：经 `PiToolBatch` + `PiToolAdvance` 执行一次后 `ReadToolCalls=1`，同一 callID 再次 `PiToolAdvance` 返回同一回执且计数仍为 1，证明重放不重复执行。

### 4.2 任务 2 · SDK 移除业务 read 扩展

实现要点：

- `newAgentDefinitionRegistry(definitions)`、`newAgentRegistry(definitions, additionalTools)`：删除可变参数适配器；`AgentDefinition` 与 `AgentCapabilitySnapshot` 删除 `ReadAdapters` / `ReadAdapterVersions` / `readAdapters`。
- `CompileAgentCapabilities` 依据 `admission.HasSkills` 设置 `SkillRead` 并决定是否加入 `read`。
- 删除 `agent_read_adapter.go` 与 `agentScopedReadToolSchema`；`cloudAgentToolAllowed` 去掉对 `read` 的无条件放行；`cloudAgentReadTool` 的错误通道 `read` 由 `runCloudAgentCheckpointTool` 统一给出可纠正拒绝（点名三个具名工具）。
- 工具名校验收紧为 `^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`，总长 ≤ 64。
- 第三业务示例改为 `library_read_document({documentId})`，经 `additionalTools` 注册 Schema、业务、版本、执行器与对象授权函数，并保留真实模型任务、Worker 接管和回执重放断言。

命令与结果：

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/app -run 'TestAgent(ReadIsSkillOnlyAcrossBusinesses\|BusinessReadCannotUseReadTool\|RegisteredToolNamesFollowSnakeCase\|DefinitionRegistry\|ToolAdmission\|SDKThirdBusiness\|CapabilityThirdBusiness\|ResourceScope)' -count=1` | 通过 |
| `go test ./internal/app -run 'TestNativeSkillReadTool\|TestAgentToolCatalogMatchesFrontendNames' -count=1` | 通过 |

### 4.3 任务 3 · Worker / 批次 / 检查点恢复 Skill-only

实现要点：

- `agent/src/native-skills.ts` 删除 `isBusinessRead` 及 `PiToolCall` 引入；`agent/src/runner.ts` 三处业务分支改为"全部 `read` 走本地 Skill 路径"，并删除 `read` 执行器中的 `executeBusinessTool("read", …)` 分支。
- Go：`piToolBatchState` 拒绝全部 `read`；`piAdmitToolBatchEnvelope` 与计费模型输出比对时筛去全部 `read`；`piCheckpointNativeSkillRead` 删除业务 read 提前放行分支。
- 新增 Worker 用例：旧业务命名空间（画布文本/画布图片/短剧分集/第三业务）本地拒绝且不打开批次、不调用业务工具；畸形参数在读文件前拒绝；混合批次只有具名业务工具进入 Go 批次并正确配对；当前 Run 的 pending Skill read 本地恢复；失败读取记成失败而不是成功；旧会话历史业务 read 只做展示投影（补"结果未知"），不执行。

命令与结果：

| 检查 | 结果 |
| --- | --- |
| `npm run build` | 通过 |
| `node --test --test-timeout=20000 --test-force-exit dist/test/native-skills.test.js dist/test/runner.test.js dist/test/session-history.test.js` | 90 通过 / 0 失败 |
| `npm test` | 210 通过 / 1 跳过 / 0 失败（跳过项为需要真实浏览器或外部依赖的既有用例） |
| `go test ./internal/app -run 'TestNative(MixedBatch\|FailedRead\|ReadCheckpoint\|SkillFile\|SkillReads)\|TestAgentBusinessReadCannotUseReadTool' -count=1 -v` | 通过 |

### 4.4 任务 4 · 目录与协议发布切换

实现要点：

- 能力目录 `agentCapabilityDirectoryVersion = 2`；内部协议 `canvas-pi-wire/v8`（Go `handler/internal_agent.go`、Node `bridge.ts`、`agent/harness/PI_WIRE_IDENTITY.json`）；工具 Schema `cloud-agent-tools/v30`。
- 定义版本：`canvas.default` v2、`drama.analysis` v2（非交互）、交互式 `drama.analysis` v3。
- `verifyAgentCapabilityDirectory` 拒绝缺少冻结目录的执行（此前直接放行），`authorizeRegisteredAgentRun` / `authorizeAgentRunTool` 不再对 `Capabilities == nil` 提前返回；`cloudAgentRunAllowsTool` 在冻结目录存在时也复核只读与子代理角色限制；Worker `validateCapabilityDirectory` 只接受目录 0/2。
- 新增 `cloud_agent_read_upgrade_test.go`：目录 1、legacy 0、缺少目录三种形态在 queued 领取、返回 Worker 快照、工具批次三处都被拒绝并终结运行、释放占用、保持账务对平；旧会话仍可查历史、按权限取消与归档，新一轮冻结目录 2 与 `canvas.default` v2 且不含历史工具调用。

命令与结果：

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/app -run 'TestAgentReadContractUpgrade' -count=1` | 通过（7 个子测试） |
| `go test ./internal/handler -run 'TestInternalAgent' -count=1` | 通过（含 v4/v5/v6/v7 协议拒绝与共享制品一致性） |
| `go test ./cmd/server -run 'TestServerDrainClosesSubscriptionsAndFinishesOrdinaryRequest\|TestServerDrainClosesRealSSERoutesAndFinishesOrdinaryWrite' -count=1` | 通过（固定协议头同步为 v8） |
| `node --test … dist/test/capability-directory.test.js dist/test/bridge-wire-contract.test.js dist/test/readiness.test.js` | 通过；目录 1、目录 7、空定义身份均被拒绝 |

工具合同制品：`node scripts/sync-harness.mjs` 已同步 `agent/harness/TOOL_DESCRIPTIONS.md` 与 `SOURCE.json`；`TOOL_SCHEMA.json` 由后端重新生成。

### 4.5 任务 5 · 正文与识图公共链路回归

- 旧统一 read 用例（`TestAgentSDKUnifiedReadReturnsTextAndAuthorizedImage`、`TestAgentSDKReadClassifierKeepsMalformedBusinessReadInDurableBatch`、`TestAgentReadAdapter`）已删除；资源隔离覆盖迁移到 `agent_resource_scope_test.go`，仍保留任务、素材、图片授权的跨业务断言。
- 公共识图合同（摘要复用、显式刷新、批量上限、单图与全轮预算、资源未就绪、请求尾部附图）由既有 `cloud_agent_pi_vision_*`、`cloud_agent_vision_*` 用例覆盖，本次未改实现。

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/app -run 'TestAgent\|TestDramaReadEpisode\|TestNative\|TestPiVision\|TestCloudAgentVision\|TestCloudAgentTool' -count=1 -timeout 900s` | 通过 |
| `TestPiModelStepBatchesImageOverflowWithoutDroppingSource`、`TestPiModelStepPersistsOnlyAdmittedImageSHAs` | 通过（夹具补上画布节点的真实资源绑定） |

### 4.6 任务 6 · 端到端、前端与文档

- 确定性模型夹具改为画布正文 `canvas_read_content`、画布图片 `canvas_inspect_image`、短剧分集 `drama_read_episode`（`backend/internal/handler/agent_sdk_e2e_test.go`）。
- 浏览器 SDK 用例（`web/test/agent-sdk.e2e.test.ts`）断言对应具名工具回执，并断言不再出现 `read` 回执；双账户、双对象、四会话、SSE 续读与 Worker 重启断言保留。
- 前端：`AGENT_TOOL_NAMES` 增加 `drama_read_episode`，补"读取分集"分页文案与失败文案；`read` 类别标签改为"读取技能"。
- 新增 Go 护栏 `TestAgentToolCatalogMatchesFrontendNames`：前端已知工具名必须是离线全集子集，画布与短剧目录各自带专用读取工具且不泄露其他业务的工具。
- 文档：更新 `docs/content/docs/backend/agent-sdk.mdx`（注册合同、工具表、发布与回退）、`http-api.mdx`（Schema 与协议版本）、`progress/pending-test.mdx`、`docs/index.md`；四份旧计划/验收补"已被取代"说明并保留历史证据。

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/handler -run '^TestAgentSDKRealHTTPWorkerRestart$' -count=1 -v -timeout 900s` | 通过（176.78s；真实 HTTP + Go + Node/Pi Worker + Bun SDK 客户端 + Worker 重启 + 确定性模型 + 真实像素；输出 `.local/cache/verify/sdk-cross-process.log`） |
| `bun test test/agent-tool-presentation.test.ts test/agent-tool-count.test.tsx` | 20 通过 / 0 失败 |
| `bun test test/agent-canvas-actions.test.ts` | 8 通过 / 0 失败 |
| `cd docs && bun run types:check` | 通过 |
| Prettier | `agent-tool-presentation.ts`、`agent-sdk.e2e.test.ts` 已格式化 |

## 5. PR149 保护验收

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/app -run 'TestNative(MixedBatch\|FailedRead\|ReadCheckpoint\|SkillFile\|SkillReads\|SkillReadTool)\|TestNativeReadCheckpointAcceptsRelativeSkillPath' -count=1 -v` | 通过 |
| `TestNativeSkillReadToolDescribesStableRelativeLocation` | 通过，并新增断言：包含 `available_skills` 原值指引，不再包含 `canvas:text/`、`canvas:image/`、`drama:episode/` |
| Worker `dist/test/native-skills.test.js` | 通过（别名、歧义、白名单、路径重定位、分页与预算断言全部保留） |
| 跨进程别名路径 | 待验（计划要求的"原生名 / 人类 ID / 唯一显示名称"三路跨进程验收未单独执行；Worker 单元与 Go 检查点匹配已覆盖） |

## 6. 最终验证与交付检查

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| Go 相关回归 | `go test ./internal/app -run 'TestAgent\|TestDramaReadEpisode\|TestNative\|TestPiVision\|TestCloudAgentVision\|TestCloudAgentTool' -count=1 -timeout 900s` | 通过 |
| Go 全量包测试 | `go test ./internal/app -count=1 -timeout 2400s` | 仅 3 个既有 Windows 环境用例失败，见 §7；其余全部通过 |
| Worker 回归 | `npm test` | 210 通过 / 1 跳过 / 0 失败 |
| Handler 协议 | `go test ./internal/handler -run 'TestInternalAgent' -count=1` | 通过 |
| 服务端排空 | `go test ./cmd/server -run 'TestServerDrain…' -count=1` | 通过 |
| 跨进程冒烟 | `go test ./internal/handler -run '^TestAgentSDKRealHTTPWorkerRestart$' -count=1 -v -timeout 900s` | 通过 |
| 架构守卫 | `go test -count=1 ./internal/architecture` | **失败**，全部为既有存量漂移，见 §7 |
| 文档检查 | `cd docs && bun run types:check` | 通过 |
| Web 专项 | 见 §4.6 | 通过 |
| 去除旧入口 | `rg -n 'ReadAdapters\|ReadAdapterVersions\|agentReadBusinessContent\|agentBusinessRead\|isBusinessRead\|readPath' backend agent/src web/src docs/content/docs` | 仅命中一条拒绝用例（把 `readPath` 当作被拒的未知字段）；`rg -n 'canvas:text/\|canvas:image/\|drama:episode/' backend/internal/app --glob '!*_test.go'` 无命中 |
| 交付审阅 | `git diff --check` | 通过（无空白错误） |

## 7. 未覆盖与既有失败（不记为通过）

1. **架构守卫既有漂移**：`go test -count=1 ./internal/architecture` 报告 `cloud_agent_previs_*.go`、`internal/auth/linuxdo.go`、`cloud_agent_pi_checkpoint.go:nativeReadCheckpointCall` 等条目超限。这些文件本次均未改动（`cloud_agent_pi_checkpoint.go` 只在另一个函数里删除 5 行），属于本分支既有状态。本次曾因给 `cloudAgentReadTool` 增加分支使其复杂度升到 22，随后抽取 `cloudAgentReadModelList` / `cloudAgentReadDramaEpisodeTool` 把该函数降回阈值内，未新增或上调任何基线条目。
2. **3 个 Windows 环境用例**：`TestSettingsEncryptionKeyPublication`、`TestSettingsEncryptionKeyLinkFallback`、`TestSettingsEncryptionKeyConcurrentFirstUse` 断言 POSIX 文件权限（0600）与硬链接行为，在 Windows 上必然失败；`settings_key_concurrency_test.go` 无平台守卫，且本次未改动该子系统。记录为既有环境限制。
3. **真实模型验收待做**：未对接真实视觉/文本渠道。确定性替身只证明协议与工具顺序，不代表真实模型的识别效果或工具选择。
4. **真实页面验收待做**：未启动 dev server 与浏览器，Studio 画布与短剧分集分析入口未人工核对。
5. **部署与制品回滚演练待做**：未接触任何部署、服务器或镜像。§4.4 的目录版本切换只有本地测试证据。
6. **PR149 跨进程三路别名验收**：未单独构造原生名/人类 ID/显示名称三种跨进程场景。
7. **未运行**：`go test ./...` 全仓、Web 全量 `bun run test`（只跑计划选中的三个专项文件）、`bun scripts/verify-before-push.mjs`（本次不推送）。
8. **CHANGELOG / VERSION**：本次未更新根 `VERSION` 与 `CHANGELOG.md`；进入推送与 release 阶段时按根 `AGENTS.md` 的 bump 与 changelog 规则补做。

## 8. 发布与回滚安排（未执行）

本次改变执行语义，backend 与 agent 必须配套发布：

1. 发布前盘点仍在运行的旧目录（1）、legacy 目录（0）与无目录运行，优先让其自然结束，记录必须中断的 Run 与已提交任务。
2. 先确认 Worker 就绪于 `canvas-pi-wire/v8`，再恢复准入；旧 v7 Worker 会被服务端以 426 拒绝。
3. 被拒绝的旧运行按 `agent_capability_directory_unavailable` 终结并释放占用；已提交回执仍可交付，历史、取消与归档入口不受影响。
4. 回滚需 backend 与 agent 使用配套旧制品；回滚镜像不会恢复已终止的运行，也不会撤销已发生的费用。
5. 本次无表结构迁移；Run 的 StateJSON 执行合同随目录版本切换，历史数据保持可追溯。
