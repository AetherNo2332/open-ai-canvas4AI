# 生成批次跨 Run 续接验收（4AI）

分支 `codex/generation-continuation-4ai`，基线 `canvas4ai/main` = `c752f8b39`。验收对象是功能提交 `2847a95fa`（本文件随后单独提交）。每次复验前核对 `git rev-parse HEAD` 与 `git status --short`；工作区不干净时结果不计。

本功能把「提交后台生成后挂起整个 run」改为「source run 正常 `finish_run`，实际登记任务全部终态后由既有会话队列只启动一次 continuation run」。保留审批、报价、预算、素材保存、媒体版本和画布回写合同；不自动重新提交失败的付费生成；画布回写延迟不在本次交付范围。

## 结论与证据

| 场景 | 本轮实测 |
| --- | --- |
| 真实 Go HTTP + Node/Pi + 隔离 PostgreSQL，确定性本地模型与图片上游 | `TestAgentGenerationContinuationRealPiDynamicBatch` 通过，96.95s。测试自选 N=3，不写死产品阈值；三条成功回执返回同一 batch ID 与递增计数；`finish_run` 后 source 为 completed；部分终态不续接；用户消息先执行；armed 阶段杀掉旧 Node Worker 后由新 Worker 接手；最终 source/user/continuation 共 3 个 run、1 条内部消息、1 个 continuation、3 次图片上游调用、12 笔测试订单，重放结算后订单与积分不变。 |
| Worker 重启与双客户端隔离（既有基线回归） | `TestAgentSDKRealHTTPWorkerRestart` 通过，354.28s：真实 Go + Node/Pi + Bun 客户端，2 个业务、2 个用户、8 个对象、16 个隔离会话、38 次模型调用/订单、128 个断言；覆盖 Worker 接管、丢失工具回执恢复与问题回答。 |
| 批次与队列状态机 | `internal/repository` 全包通过（41.65s）：单任务、多任务、重复登记、重复结算、`armed → ready` 屏障、detached、`ready → queued` 恰好一次。 |
| Schema 113 迁移 | `internal/database` 全包通过（76.2s）：`TestCurrentSchemaVersionMatchesMigrationPlan`、`TestMigrationV113CreatesGenerationContinuationTables`（两表、source run 唯一索引、队列 kind 列）及既有 v108/v109/v111 升级用例。 |
| 后端架构守卫 | `internal/architecture` 通过：队列绑定抽成 `admitQueuedMessageRun` 后，`CommitAgentMessageAdmission` 闭包复杂度回到基线内；未新增基线条目。 |
| 工具目录预算 | `TestAgentCapabilityDirectorySizes` 通过：canvas/auto 63996 字节、canvas/read_only 13600、drama/read_only 4019、zero/read_only 2027；未放宽 64000 上限。 |
| 分镜异步提交改走续接 | `TestCloudAgentStoryboardAsyncSubmitContinuation` 通过（2.01s）：拆镜回执立即返回并登记同一批次；`finish_run.continuation` 后 source 为 completed、批次 armed；末项终态前 `QueueReadyGenerationContinuations` 返回 0，终态后只排入一条内部消息，重复扫描返回 0；批次结果保留 `splitResult`/`rowId`，用户可见队列看不到内部消息。 |
| SQLite→PostgreSQL 迁移清单 | `cmd/migrate-sqlite-postgres` 的 `TestMigrationListCoversSchemaModels`、`TestMigrationListCoversInitializedSchema` 通过：两张批次表已加入精确表清单，升级/复制不会漏表。 |
| 策略元数据 | `TestLoadAgentPoliciesUsesDocumentMetadata` 通过：system 28、general 1、media 7，正文不含 frontmatter，media 保留“不轮询/续接/不自动重新收费”合同语句。 |
| 密钥文件权限（POSIX 语义） | `TestSettingsEncryptionKey*` 在 Linux 容器内实际执行原始 `0600` 断言：`golang:1.25-alpine`（`sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59`，Go 1.25.14 linux/amd64），仓库只读挂载、临时目录用容器原生文件系统；5 个用例（发布、链接回退、并发首次使用、等待旧写入者、不覆盖已有）全部通过，退出 0，日志 `.local/cache/continuation/linux-settings-key.log`。Windows 上仍执行发布、完整性、并发一致性、防覆盖、回退与临时文件清理检查，`os.Stat` 错误仍然失败；只有 `0600` 位比较限定在能表达 POSIX 权限位的平台，生产 `settings_key.go` 的 GOOS 分支未改。 |
| 提示词与制品一致 | `TestCloudAgentGenerationContinuationPrompts`、`TestAgentToolSchemaArtifactMatchesRuntime` 通过；`agent/scripts/sync-harness.mjs` 已同步 SYSTEM_POLICY/MEDIA_POLICY/TOOL_DESCRIPTIONS/SOURCE。制品版本 `cloud-agent-tools/v30`、`canvas-pi-wire/v8`。 |
| Agent Worker | `cd agent && npm test` 退出 0：207 项，206 通过、1 跳过、0 失败；`npm run build` 退出 0。 |
| 非 ASCII 主机名的 Worker 启动 | 门禁暴露的既有缺陷：本机主机名是 `你猜猜我是谁`，Worker 用它派生租约槽位时被 `^[A-Za-z0-9._-]+$` 拒绝并抛 `Invalid worker slot`，因此从不打开 `/internal-agent/events`。`agent/src/bridge.ts` 改为先归一化 Worker id 再截取槽位，新增 `bridge-wire-contract` 用例覆盖；修复后 `TestAgentWakePostgresWorkerClaimsDuringFallback` 通过（PASS 21.90s），覆盖 listener 断开期间 Worker 认领持久 run。 |

## 命令与结果

原始日志在忽略目录 `.local/cache/continuation/` 与 `.local/cache/verify/`。

| 命令 | 结果 |
| --- | --- |
| `cd backend && go test ./internal/app ./internal/repository ./internal/database ./internal/handler -count=1 -timeout 300s` | 退出 1，不是功能断言失败：`app` 与 `handler` 在 5 分钟包时限内整包超时（`app` 超时点仍是与本功能无关的公告用例）。分项证据见下行。 |
| `cd backend && go test ./internal/repository -count=1 -timeout 300s` | 退出 0（41.65s）。 |
| `cd backend && go test ./internal/database -count=1 -timeout 300s` | 退出 0（76.2s）。 |
| `cd backend && go test ./internal/architecture -count=1` | 退出 0。 |
| `cd backend && TEST_AGENT_POSTGRES_DSN=... go test ./internal/handler -run '^TestAgentGenerationContinuationRealPiDynamicBatch$' -count=1 -v -timeout 300s` | 退出 0（98.9s 含清理）。 |
| `cd backend && TEST_AGENT_POSTGRES_DSN=... go test ./internal/handler -run '^TestAgentSDKRealHTTPWorkerRestart$' -count=1 -v -timeout 900s` | 退出 0（355.4s）。 |
| `cd agent && npm test` | 退出 0（207 项，206 通过、1 跳过）。 |
| `cd agent && npm run build` | 退出 0。 |
| `cd backend && go test ./internal/app -count=1 -timeout 900s` | 见推送门禁的分片结果；300s 时限不足是总时长环境限制，不记为通过。分片里曾出现的 `TestSettingsEncryptionKey*` 失败是 Windows 无法观测 Unix 0600 权限位（生产 `settings_key.go` 本就按 GOOS 跳过 chmod）；已在测试中按 GOOS 保留 Unix 断言、Windows 跳过断言，行为用例仍执行，Linux CI 覆盖不变。 |
| `bun scripts/verify-before-push.mjs --base canvas4ai/main --mode full` | 按提交分别记录在下方「推送前门禁」的表里，不跨提交合并结论。 |

## 推送前门禁

门禁命令固定为 `bun scripts/verify-before-push.mjs --base canvas4ai/main --mode full`。环境：`GOTOOLCHAIN=go1.25.0`、`CHROME_BIN=C:\Program Files\Google\Chrome\Application\chrome.exe`、`TEST_AGENT_POSTGRES_DSN` 与 `CANVAS_TEST_POSTGRES_DSN` 同时指向隔离库 `canvas_event_integration`（容器 `task7-continuation-pg-20261126`，`127.0.0.1:4961`，端口变更原因见「环境、隔离与限制」）。每轮的提交、计划、逐项日志和 skip 统计都在 `.local/cache/verify/`，门禁输出在 `.local/cache/continuation/`。

结果按提交分别记录，不把不同提交、不同模式（full / affected）混写：

| 提交 | 模式 | 退出码 | 结果 |
| --- | --- | --- | --- |
| `f16c4d70f`（首轮交付的代码提交） | full | 0 | 26 项全部通过，失败 0，用时 2214.1s |
| `9454771`（首轮交付的最终提交，相对上一行仅文档与版本号） | full | 1 | 25 项通过；`backend:full` 失败于 `internal/hostupdate TestDocsHealthFailureRestoresSourceOnly`（整包并行偶发，模拟容器恢复报「文档恢复失败」）。该用例在本分支与干净 `canvas4ai/main` 上单跑均通过；同一命令单跑 `backend:full` 退出 0，但那次只设置了 `TEST_AGENT_POSTGRES_DSN`，见下方 skip 口径 |
| 复审修复后的代码与最终提交 | full | 见下 | 修复期间首次门禁因工作区变脏被中止（`Working tree changes included`），不计入任何结论；最终提交的完整门禁结果以 `.local/cache/verify/verification-plan.json` 的 `head` 与 `result-local.json` 为准，并写入 PR 描述 |

skip 口径（首轮遗留问题，本轮修正）：

- 首轮单跑 `backend:full` 时只设置了 `TEST_AGENT_POSTGRES_DSN`，依赖 `CANVAS_TEST_POSTGRES_DSN` 的 PostgreSQL 用例被 skip，那次 `EXIT=0` 不能记为完整覆盖。
- 复审修复起，两个 DSN 同时设置；结论以 `result-local.json` 的 `head` 对应提交和实际 skip 统计为准，skip 不计为通过，PR 描述列出通过项与 skip 数。

首轮定位并修掉的门禁问题（保留记录，均在分支内修复，未降低断言或安全合同）：

- `backend:full → TestAgentWakePostgresWorkerClaimsDuringFallback` 超时：根因是主机名非 ASCII（本机 `你猜猜我是谁`）时 Worker 用主机名派生租约槽位被 `^[A-Za-z0-9._-]+$` 拒绝并抛 `Invalid worker slot`，事件流永不建立。在与基线一致的干净 `canvas4ai/main` 工作树用其自带 v7 Worker 复现（`.local/cache/continuation/main-baseline-evidence.log`），修复前源码在 `golang:1.25-alpine` 容器内同样失败（`.local/cache/continuation/linux-wake.log`），确认是既有缺陷。修复：`agent/src/bridge.ts` 先归一化 Worker id 再截取槽位，`bridge-wire-contract` 新增用例；后端用例只把等待 Worker 冷启动的预算从 5s 提到 60s，不改服务超时。修复后该用例退出 0（PASS 21.90s，`.local/cache/continuation/wake-postfix.log`）。
- 该用例随后暴露第二个既有竞争：监听器会在约 1s 内重连，而断言要求 worker 在监听器离线期间完成认领。现在改为服务级故障控制：`Service.StopAgentWakeListener` 同步停止读取循环并等待退出，测试在「监听器确实停止」的窗口内创建运行并观察 worker 认领，然后 `RestartAgentWakeListener` 恢复并验证完成与单次租约/模型执行；断言仍是「认领必须发生在监听器离线期间」，不再靠杀连接碰成功。本机连续 3 次通过（认领约 0.6–0.85s）。
- `agent:smoke`：生产冒烟内部探针硬编码 `canvas-pi-wire/v7`，已同步 `v8`（`scripts/verify-production-agent-smoke.py`）。
- `web:test` 的 10 条失败是工作树 CRLF 与仓库 LF 文本断言不一致；规范为 LF 后 3708 通过 / 7 跳过 / 0 失败，未改断言。
- `TestSettingsEncryptionKey*` 按平台限定 `0600` 位比较：Windows 仍执行发布、完整性、并发一致性、防覆盖、回退与临时文件清理检查，Linux 容器实跑原始断言（见上表）。
- wake 用例的并发认领竞争：`TestAgentWakeClaimRacePostgres` 曾出现两次并发 claim 都返回「暂无」（`winner="", null responses=2`），随后运行被后续 worker 认领。现在 `raceAgentWakeClaims` 用有界重试等运行可认领，每一轮仍要求恰好一个赢家、出现双认领立即失败，断言强度不变；本机连跑 2 次通过。

## 复审修复（首轮独立审阅，P1 + 3×P2）

1. **P1 单账号续接入队失败曾阻断全局周期派发。** `DispatchAgentMessages` 原来在扫描普通消息前调用续接入队并直接返回错误，任一 ready 批次配额不足会让整轮提前退出。现在续接入队是尽力而为：`QueueReadyGenerationContinuations` 每个批次独立处理、保留首个真实错误并继续，`DispatchAgentMessages` 在派发普通消息后回传该错误。失败批次保持 `ready` 等额度恢复，其它账号的普通消息与续接照常推进。回归 `TestDispatchContinuesAfterContinuationQuotaRejection`：A 账号任务存储耗尽时，B 账号的普通消息与续接在同一轮推进，A 的配额错误原样回传、批次仍 `ready`。
2. **P2 合法长度 instruction 曾生成必然无法准入的续接。** 续接提示词由后端拼装，现在与运行提示词共用 `cloudAgentPromptMaxRunes`（16000 字，单点常量）：`finish_run` 在拿到批次后按 `generationContinuationInstructionLimit(实际任务数)` 校验 instruction（超出返回 `too_long` 字段错误，附带本批次上限，source run 不结束）；拼装侧保留任务身份行，结果明细超预算时压缩摘要并追加省略计数，保证拼装结果一定通过新 run 的同一校验。回归 `TestGenerationContinuationInstructionSharesPromptBudget`（16000 字被拒、预算内通过且提示词 ≤16000、可通过 `validateCloudAgentPrompt`）、`TestGenerationContinuationPromptStaysWithinLimitWithManyTasks`（48 个任务 + 600 字摘要/错误）。
3. **P2 内部消息曾未进入累计存储计量。** `UserStorageUsage` 的 `task_bytes` 现在累计 `agent_queued_messages`（prompt / request_json / last_error）与 `agent_generation_batches`（instruction / request_json / results_json）；未迁移该表或只读 fixture 缺表时按表存在性跳过，累计计量不会整体失败。准入沿用「任务正文减去队列正文」的增量公式，同一份文本不重复计费。回归 `TestUserStorageUsageCountsQueuedMessagesAndBatches`（累计、跨账号隔离、清理后扣减）与 P1 回归里的准入增量核对。
4. **P2 source 失败 / 取消 / 停止曾未关闭 collecting 批次。** 取消与停止在 `CancelCloudAgent` 内联关闭批次；worker 致命失败在 `PiFailRun` 内联关闭；收尾次数耗尽的终止在 finish_run 事务内关闭（`detachTerminatedGenerationBatch`）；其余终态与崩溃窗口由 `QueueReadyGenerationContinuations` 开头的 `DetachOrphanAgentGenerationBatches` 收口，重启后第一轮派发即修复。回归 `TestGenerationBatchDetachedWhenSourceCancelled`（生成任务行与账单保留）与 `TestGenerationBatchDetachedAfterSourceFailureAndRestart`（失败 + 重启 + 重复扫描幂等）。

## 第二轮复审修复（P1×1 + P2×2）

1. **P1 失败批次占满扫描窗口会饿死其它账号。** `ReadyAgentGenerationBatches` 按 `updated_at` 取最旧的前 `limit` 条；失败的批次保持 ready 时位置不变，前 64 条都失败会让后面的正常续接永远进不了窗口。现在续接入队失败后调用 `TouchReadyAgentGenerationBatch` 把该批次轮转到队尾（只改 `updated_at`，不改状态、不增版本），失败原因写入门禁日志并作为首个错误回传给调用方。回归 `TestContinuationScanRotatesFailedBatchesSoLaterAccountsAdvance`：A 账号 64 条 ready 批次占满窗口并配额耗尽，第一轮 0 入队且 B 仍在窗口外；第二轮失败批次让位后 B 入队，A 的 64 条仍保持 ready。
2. **P2 提示词压缩会丢掉任务身份。** 现在按身份行预算（`generationContinuationTaskIdentityRunes`=240，覆盖 80 字符 taskId、80 字符 nodeId、序号、mode 与状态）计算 instruction 上限与拼装预算：逐级降级顺序是「明细 → 去摘要 → 去标题 → 只留 taskId/status」，任何一级都保留全部任务身份，只有任务数量本身超出提示词预算（正常预算达不到）才列出可截断的 taskId 列表并以批次 ID 明示剩余范围。回归 `TestGenerationContinuationPromptStaysWithinLimitWithManyTasks`：48 个任务、240 字标题、80 字符 taskId/nodeId、instruction 取到上限，逐项断言每个 taskId 仍在提示词里、提示词 ≤16000 且能通过 `validateCloudAgentPrompt`。
3. **P2 存储计量漏掉批次任务结果副本。** `UserStorageUsage` 的 `task_bytes` 现在同时累计 `agent_generation_batch_tasks.outcome_json`（与批次正文同属 Schema 113，缺表时同样跳过）。回归 `TestUserStorageUsageCountsQueuedMessagesAndBatches` 扩展为真实结算路径：写入 `agent_generation_batch_tasks` 即计入、`SettleAgentGenerationBatchTask` 的增量等于新 outcome 与 results 正文之差、重放结算不重复计量、删除任务行后回到基线。

## 行为覆盖（对应任务书 Task 7）

- 动态 N：批次大小来自实际登记行数；测试自选 N，不读产品常量。
- 同一 source run 的成功提交回执返回同一 `generationBatchId` 与递增 `batchTaskCount`；被拒提交不登记。
- `finish_run.continuation` 只接受 `all_generations_terminal`，`instruction` 必填；无登记任务时返回业务参数错误；普通 `finish_run` 将批次置 detached，不自动续接。
- 成功、失败、取消、任务缺失和画布回写失败都结算为终态；部分终态只更新结果。
- 用户消息优先；内部 continuation 与用户 run 共用会话 single-flight，活跃 run 期间排队等待。
- armed/ready/queued/consumed 边界重启、重复结算、租约过期与两个独立 Service/连接争抢均只产生一条内部消息、一个 continuation run，不新增费用预留。
- 内部续接消息不出现在用户队列列表/计数里，不能被普通编辑、提升或取消；确定性准入失败保留批次与真实错误，只停止该内部项自动派发，不暂停用户会话队列，不自动重试失败的付费生成。
- 旧 `WaitKind=generations` 检查点在 Pi 快照构建前修复：清等待、写一条兼容回执、推进调用索引，不重放已提交的收费生成；审批、子代理与预演等待不受影响。
- `wait_generations` 已从工具 Schema、类别、定义、执行器、提示词与制品中移除。
- 派发隔离：一个账号的续接入队失败（配额、存储、冲突）只让它自己的批次保持 `ready` 等重试，不阻断同周期其它账号的普通消息和续接；失败原因如实回传。
- 长度预算：续接提示词与运行提示词共用 16000 字上限，instruction 按实际任务数扣减后校验，拼装结果始终能通过新 run 准入。
- 累计存储：未消费的内部续接消息与生成批次正文计入账号任务存储；准入按「任务正文 − 队列正文」计增量，跨账号隔离，清理后扣减。
- 终态收口：source run 失败、取消、停止或崩溃后仍在 `collecting` 的批次会转为 `detached`（取消/worker 致命失败内联，其余由派发前孤儿扫描在重启后第一轮完成），生成任务、结果与账务记录保留。
- 保留审批、报价、预算、资源、媒体版本与画布回写合同；失败付费生成不自动重试。

## 环境、隔离与限制

- 本机 Docker Server；复用 `postgres:17-alpine`（PostgreSQL 17.12，镜像摘要 `sha256:a77aa70e641402a21411b92b721c58d10f23b50ae5fdd2333efd22d166e1e5f7`）。独立容器 `task7-continuation-pg-20261126` 仅绑定 `127.0.0.1:4961`，独享匿名卷，无业务挂载、无 Compose 项目；未 `prune`、未 `--no-cache`、未清理他人容器或镜像。Go/Node/Bun 在宿主机运行，本轮未构建 Docker 镜像。
- 端口变更：容器原先绑定 `127.0.0.1:4908`；2026-11-26 20:30 左右 Docker Desktop 重启后容器退出，且 Windows TCP 排除端口段（`netsh int ipv4 show excludedportrange protocol=tcp`）此时覆盖 4853–4952，4908 已无法绑定。用同一缓存镜像重建容器并改绑 `127.0.0.1:4961`，数据库、认证方式与隔离范围不变（仍与短剧 15432/15439 隔离）。
- `TEST_AGENT_POSTGRES_DSN` 与 `CANVAS_TEST_POSTGRES_DSN` 都指向该容器内的 `canvas_event_integration`；每个 HTTP 用例创建并删除独立 schema，不使用业务库 DSN。只设置其中一个会让另一组 PostgreSQL 用例 skip，这类结果不计为完整覆盖。
- Windows 上多包 `go test` 并行、SQLite 与真实跨进程用例互相拖慢；300 秒整包时限对全量套件不足，这是环境限制，未通过扩大产品超时或降低安全合同绕过。
- 真实模型效果、生产计费、浏览器交互、真实素材交付，以及「在每个状态杀死整个 Go 后端进程」未作为本轮真实证据；进程级恢复用新 Service/新连接读取同一数据库文件与真实 Worker 接管覆盖。
- 迁移编号：本功能取 Schema **113**；短剧工作台移植取 **112**。迁移文件按注册顺序排列为 `migrations_zzzzz_shortplay.go`(112) → `migrations_zzzzzz_agent_generation.go`(113)，合并后 `CurrentSchemaVersion` 取 113；两 PR 表无交集，预期冲突仅在 `VERSION`/`CHANGELOG.md`。

## 回滚

回滚前停止新准入，排空或明确停止在途 run，备份数据库与资源；保留批次、内部消息、回执、生成任务和账务记录。在隔离副本验证目标 Go 与 Node Worker 能读现有 Schema、冻结工具目录和 wire 后配套回退；不能降级时使用经核对的一致备份，不删除批次表、不重放已消费消息、不靠重新生成或退款模拟回滚。本轮未在生产执行回滚。
