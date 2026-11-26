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
| `bun scripts/verify-before-push.mjs --base canvas4ai/main --mode full` | 见下方「推送前门禁」：26 项 25 通过，唯一失败是 `backend:full` 里的 `TestAgentWakePostgresWorkerClaimsDuringFallback`；该用例在干净的 `canvas4ai/main` 工作树用其自带 v7 Worker 同样失败。 |

## 推送前门禁

最后一次完整门禁在功能分支 HEAD 上执行（`GOTOOLCHAIN=go1.25.0`、`CHROME_BIN=C:\Program Files\Google\Chrome\Application\chrome.exe`、`TEST_AGENT_POSTGRES_DSN` 指向隔离库 `canvas_event_integration`，容器 `task7-continuation-pg-20261126` / `127.0.0.1:4908`）：

- 结果：26 项，**25 通过，1 失败**，用时 1923.3s；计划与逐项日志保存在 `.local/cache/verify/`（`verification-plan.json`、`result-local.json`、`*.log`），门禁输出在 `.local/cache/continuation/gate-full-5.log`。
- 通过项包括：`backend:agent-lint`、`agent:check`、`backend:app-shard-1..4`、`backend:baseline`、`backend:gofmt`、`backend:vet`、`backend:tool-contract`、`changelog`、`docs:*`、`deployment:commands`、`verification:tests`、`payment:*`、`agent:smoke`、`web:format/lint/test/typecheck/e2e`。
- 唯一失败（已定位并修复）：`backend:full → TestAgentWakePostgresWorkerClaimsDuringFallback`（`timed out waiting for worker event stream`）。根因不是本次续接改动：本机主机名是非 ASCII（`你猜猜我是谁`），Worker 用它派生租约槽位时被 `^[A-Za-z0-9._-]+$` 拒绝，直接抛 `Invalid worker slot`，所以从未打开 `/internal-agent/events`。在干净的 `canvas4ai/main` 工作树、用它自带的 `canvas-pi-wire/v7` Worker 构建复现同一失败（日志 `.local/cache/continuation/main-baseline-evidence.log`），修复前的源码在 `golang:1.25-alpine` 容器内也是同一现象（`.local/cache/continuation/linux-wake.log`），说明这是既有缺陷，与平台无关。
- 修复：`agent/src/bridge.ts` 先用 `[^A-Za-z0-9._-]` 归一化 Worker id 再截取槽位，非 ASCII 主机名不再让 Worker 启动失败，`agent/test/bridge-wire-contract.test.ts` 新增用例钉住；该后端用例等待 Worker 冷启动的预算从 5s 提到 60s（只改测试，不改服务超时）。修复后本机实跑 `go test ./internal/handler -run '^TestAgentWakePostgresWorkerClaimsDuringFallback$'` 退出 0，PASS 21.90s（`.local/cache/continuation/wake-postfix.log`）。
- 本轮修复并复验：生产冒烟内部探针同步 `canvas-pi-wire/v8`（`scripts/verify-production-agent-smoke.py`），`agent:smoke` 由失败转为通过；工作树 CRLF 规范为 LF 后 `web:test` 3708 通过/7 跳过/0 失败；`CHROME_BIN` 下 `web:e2e` 78/78 断言通过；`docs:check` 的 `bun run build` 直接执行退出 0（此前门禁 `child output pipes did not close` 为 Bun/Windows 管道问题，本轮未复现）。
- 隔离 PG 下 SDK 跨进程用例通过（`TestAgentSDKRealHTTPWorkerRestart`、`TestAgentSDKNativeSkillsHTTP`）；`TestPasswordResetRecordsSkipReasonsAndDeliveries` 的 60 秒冷却顺序问题在 `canvas4ai/main` 上也 1/3 次失败（同日志文件），属既有测试时序缺陷。
- 上表对应提交之后的改动只有本验收文件、Agent SDK 文档、待验清单、内部版本号，以及上面的 Worker 槽位修复（`4246adcc3`：`agent/src/bridge.ts` 一条行为修复、一条前端合同用例、两个后端用例的等待预算）。最终提交上的完整门禁复跑结果保存在本机 `.local/cache/verify/`，并在 PR 描述里列出，本文件不预写尚未执行的结果。

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

## 环境、隔离与限制

- 本机 Docker Server；复用 `postgres:17-alpine`（PostgreSQL 17.12，镜像摘要 `sha256:a77aa70e641402a21411b92b721c58d10f23b50ae5fdd2333efd22d166e1e5f7`）。独立容器 `task7-continuation-pg-20261126` 仅绑定 `127.0.0.1:4908`，独享匿名卷，无业务挂载、无 Compose 项目；未 `prune`、未 `--no-cache`、未清理他人容器或镜像。Go/Node/Bun 在宿主机运行，本轮未构建 Docker 镜像。
- `TEST_AGENT_POSTGRES_DSN` 指向该容器内的 `canvas_event_integration`；每个 HTTP 用例创建并删除独立 schema，不使用业务库 DSN。健康检查不计为验收。
- Windows 上多包 `go test` 并行、SQLite 与真实跨进程用例互相拖慢；300 秒整包时限对全量套件不足，这是环境限制，未通过扩大产品超时或降低安全合同绕过。
- 真实模型效果、生产计费、浏览器交互、真实素材交付，以及「在每个状态杀死整个 Go 后端进程」未作为本轮真实证据；进程级恢复用新 Service/新连接读取同一数据库文件与真实 Worker 接管覆盖。
- 迁移编号：本功能取 Schema **113**；短剧工作台移植取 **112**。迁移文件按注册顺序排列为 `migrations_zzzzz_shortplay.go`(112) → `migrations_zzzzzz_agent_generation.go`(113)，合并后 `CurrentSchemaVersion` 取 113；两 PR 表无交集，预期冲突仅在 `VERSION`/`CHANGELOG.md`。

## 回滚

回滚前停止新准入，排空或明确停止在途 run，备份数据库与资源；保留批次、内部消息、回执、生成任务和账务记录。在隔离副本验证目标 Go 与 Node Worker 能读现有 Schema、冻结工具目录和 wire 后配套回退；不能降级时使用经核对的一致备份，不删除批次表、不重放已消费消息、不靠重新生成或退款模拟回滚。本轮未在生产执行回滚。
