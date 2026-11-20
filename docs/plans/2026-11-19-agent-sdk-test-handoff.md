# Agent SDK 测试交接

## 测试范围与状态

基准为 `AetherNo2332/open-ai-canvas4AI` 的 `main`。测试时记录 PR 的完整提交 SHA 和 `VERSION`。本交付覆盖首轮 Agent SDK、T1–T5、画布助手界面简化、短剧分析和账户共享偏好。数据库新增迁移 108、109，Go 与 Node/Pi Worker 的配套协议为 `canvas-pi-wire/v6`。

| 验收层级 | 当前证据 |
| --- | --- |
| 代码 | Go、Worker、Web 与文档站改动已整理到 4AI 交付分支 |
| 自动化 | Go 隔离、注册、读取、偏好、OpenAPI、迁移 108/109 和 Redis 专项通过。Worker 202 项通过，Windows 跳过 1 项。Agent Web 专项 69 项通过，LF 检出上的 Web 全量 3,554 项通过，Web 类型检查和变更文件格式检查通过。文档站类型检查通过，构建生成 61 页。完整门禁的状态见下节，原开发工作树的分片结果见[验收台账](2026-11-18-agent-sdk-tools-session-acceptance.md) |
| 页面 | 原开发工作树使用实际构建的页面、Chrome、Go 和 Pi Worker，通过画布看图、偏好保存回读、短剧提问与回答、项目隔离、续聊、归档和取消。模型为本地确定性替身。界面重设计的视觉与交互按下表复验 |
| 供应商模型 | 待验。需要获准的文本与视觉测试渠道、预算和测试素材 |
| 部署 | 待验。需要获准的目标测试环境、脱敏数据库副本和匹配的 Go/Worker 镜像 |

## 自动化复跑

在 PR 的干净检出上安装锁定依赖。Web 和文档站各执行 `bun install --frozen-lockfile`，Worker 执行 `npm ci --ignore-scripts`。准备可用的 Docker Buildx（linux/amd64 与 linux/arm64）、Chrome、临时 PostgreSQL 和 Redis。按 `.github/workflows/quality.yml` 设置 `CANVAS_TEST_POSTGRES_DSN`、`TEST_AGENT_POSTGRES_DSN` 和 `CANVAS_TEST_REDIS_ADDR`，使用一次性测试数据与凭据。

```sh
git fetch canvas4ai main
bun scripts/verify-before-push.mjs --base canvas4ai/main --mode full
```

如果检出的 `origin` 就是 4AI，基准用 `origin/main`。计划、任务日志和结果位于 `.local/cache/verify/`。选中的任务必须全部运行成功，依赖服务用例须实际执行。专项定位命令如下。

| 本地门禁项目 | 状态与证据 |
| --- | --- |
| Go 全量与 lint | `backend:full`（rest）、架构基线、gofmt、vet 和工具合同通过。Go 1.25.0 与 CI 固定的 golangci-lint 2.5.0 复跑为 0 个问题。Windows 此前 app 分片报文件权限和媒体探测失败，两个媒体用例后来在 Windows 专项通过。WSL Ubuntu 22.04 的 FFprobe 4.4.2 不支持 `stream_side_data`，分支与 `main@422232409` 均复现 `TestTimelineRenderCropsNearAspectAndWritesProvenance` 和 `TestDramaCanvasAssetsProbesUnknownVideoDimensions` 失败 |
| Web 全量与预演 E2E | LF 检出上 `bun run test` 3,554 项通过，见 `.local/4ai-web-lf-final.log`。实际 Chrome 预演为 70 项通过、1 项失败，失败在 `shotOutputs` 录制未生成视频。相同浏览器下 `main@422232409` 的 `shotOutputs` 对照复现相同失败。见 `.local/4ai-web-previs-e2e-final.log` 与 `.local/4ai-web-previs-e2e-base.log` |
| 容器冒烟基线 | 2026-11-19 重建本机 Docker 数据盘后，Compose 构建四个镜像并启动隔离服务。迁移门禁、Worker 就绪状态、容器隔离和错误凭据拒绝等 11 项通过。首个确定性模型 Run 在 `canvas_get_state` 超时。Go 日志记录了工具执行反复遇到 Run 版本冲突。见 `.local/4ai-container-smoke-after-docker-rebuild-mirror/` |
| Luna 容器复跑 | Compose 配置、回环端口、存储与镜像隔离检查通过。BuildKit 在 `auth.docker.io` 鉴权超时，未进入容器启动。两个复跑项目无遗留容器或卷。见 `.local/4ai-container-smoke-luna-20261119/summary.md` |
| 工具轮询回归 | 旧实现在同一 operation 的第二次轮询产生两个 `tool_pending` 事件，Run revision 从 6 升到 7。修复后仅首次入队写事件并唤醒执行器，重复轮询复用操作标识和状态，Go 回归通过。冒烟超时报告保留最近一次脱敏 Run 快照，部署脚本测试 43 项通过 |
| 修复后正式容器冒烟 | 基于 `b5c0205ab` 加本地修复，使用现有 `workspace-gap-mirror` 构建器复跑正式脚本，21 项全通过，4 个确定性模型 Run 全部完成。每轮各有 3 条接单事件和 3 条成功回执。通过自动写入、审批前拦截、审批后落库、SSE 游标续读、图片生成及同节点版本切换。测试项目的容器、卷和网络已清理。见 `.local/4ai-container-smoke-after-tool-poll-fix/report.json` |
| Go + Pi 跨进程 | 修复后的 4AI 工作树在独立 PostgreSQL 14 测试库通过 Worker 接管和偏好 API 测试。覆盖两用户、两业务、八对象、16 个隔离会话、38 次本地确定性模型调用和等量结算，越界标记为零。Windows SQLite 对照在并发用例中出现 `database is locked`，其中一个会话返回 `failed`。见 `.local/4ai-container-smoke-after-tool-poll-fix/handler-sdk-postgres.jsonl` 与 `handler-sdk-restart.jsonl` |
| 支付制品与双架构 | 六种目标平台的支付制品检查通过。使用本机镜像源构建器复跑，Docker `linux/amd64` 和 `linux/arm64` 的 `payment-package-smoke` 均通过。见 `.local/4ai-container-smoke-after-tool-poll-fix/payment-buildx.log` |

本次先创建草稿 PR 供测试交接。转为待评审前，用最终提交复跑仓库规定的 Quality checks，并核对上表的待复验项。

| 目录 | 命令 |
| --- | --- |
| `backend` | `go test ./internal/app ./internal/handler -run 'TestAgent(ContextIsolation\|PreferenceSnapshot\|SharedPreferences\|DefinitionRegistry\|ToolAdmission\|ReadAdapter\|ResourceScope\|ConversationScope\|SDKThirdBusinessReadAndRecovery)' -count=1 -v` |
| `backend` | `go test ./internal/database ./cmd/migrate-sqlite-postgres -run 'Test(AgentContextPolicyMigration\|AgentConversationScopeMigration\|CurrentSchemaVersionMatchesMigrationPlan\|Migrate)' -count=1 -v` |
| `agent` | `npm test` |
| `web` | `bun test test/agent-panel-presentation.test.tsx test/agent-conversation-scope.test.ts test/agent-shared-preferences.test.ts test/cloud-agent-history.test.ts test/agent-api-reliability.test.ts` |
| `docs` | `bun run types:check` 和 `bun run build` |

实际页面冒烟先在 `web` 构建 `bun run build`，在 `agent` 构建 `npm run build`。把 `AGENT_SDK_BROWSER_SCRIPT` 设为 `agent/test/agent-sdk-browser-smoke.mjs` 的绝对路径，必要时用 `AGENT_SDK_CHROME` 指定 Chrome。使用临时库 `TEST_AGENT_POSTGRES_DSN`，在 `backend` 执行：

```sh
go test ./internal/handler -run '^TestAgentSDKRealHTTPWorkerRestart$' -count=1 -v
```

预期浏览器断言实际执行，确定性端点收到 PNG，项目隔离标记不混入请求，模型请求和结算一一对应。保存终端输出及浏览器结果。

## 人工接手清单

用两个测试账户。账户 A 创建两份画布及两个短剧项目，每个对象放入不同的文本标记并准备授权图片。账户 B 建立独立项目。只使用测试素材。在桌面和窄屏检查主要交互，并切换浅色、深色主题。

| 编号 | 操作 | 通过标准 |
| --- | --- | --- |
| UI-01 | 打开画布助手，检查空态与历史。发送长消息，滚动对话，收起再打开面板 | 输入、发送、停止和历史入口可见。窄屏可操作，消息不遮挡输入框，助手滚动不缩放画布 |
| UI-02 | 在短剧分集打开分析，发送消息，回答 Agent 的问题，再续聊、取消及归档 | 流式文本及等待状态可读，操作对应当前轮次。切换项目后旧输出不出现在新项目。归档会话可回读 |
| CTX-01 | 在账户设置保存语言、语气、详略与格式，刷新后分别启动画布和短剧 Run | 偏好回读一致。脱敏模型任务记录含相同偏好 revision，各 Run 只含其业务范围允许的数据 |
| CTX-02 | 两个页签从同一 revision 保存不同偏好。修改偏好期间保留一个运行中的 Run，再发起新轮 | 后提交的旧 revision 报冲突。旧 Run 与恢复继续使用原快照，新一轮取得最新 revision |
| ISO-01 | 用其他画布、短剧项目和账户的会话或运行 ID，测试历史、事件、入队、审批、取消及归档 | 访问被拒，原对象状态、事件及审批决定均无变化 |
| ISO-02 | 给项目写入不同私有标记，再打开同对象新会话和其他业务会话。查看旧账户提示词和旧记忆仍可访问 | 新 Run 的请求与摘要不携带其他会话或其他对象的标记，也不自动注入旧账户自由文本；不可续接的旧历史仍可查看 |
| TOOL-01 | 读取授权画布正文、图片及短剧分集。再尝试未绑定资源、其他项目分集和不支持视觉的模型 | 允许的读取产生对应回执。越界数据不会进入模型请求。短剧只提供注册的只读能力 |
| TOOL-02 | 在画布触发需要审批的写操作。分别拒绝、批准、在审批期间改动目标 | 拒绝时画布无写入。允许时命令与回执对应。目标变化时按现有版本冲突处理 |
| WORKER-01 | 隔离环境用两个 Worker 竞争领取。终止持有租约的 Worker 后重连 SSE | 新 Worker 接管；事件顺序与回执可恢复，旧租约拒绝，账单和有副作用的工具各只提交一次 |
| MODEL-01 | 获准后分别用真实文本和视觉渠道复跑偏好和授权图片用例 | 文本结果与目标语言格式一致。视觉模型在授权图像上完成指定识别任务。记录渠道、模型、调用次数与结算 |
| DEPLOY-01 | 在授权测试环境的脱敏副本核对迁移 108/109，停新准入后排空，配套切换 Go 与 Worker | 迁移前后记录对得上，旧 Worker 被拒绝，新版本可启动和恢复。按[Agent SDK 升级说明](../content/docs/backend/agent-sdk.mdx)验证回退兼容性并记录结果 |

第三业务通过 Go 测试宿主验证注册、读取和恢复。第三业务走 TS SDK 的完整 UI、迟到 SSE 回调的独立回归、目标环境性能量测及公共素材免绑定授权留在后续验收。

每条结果记录编号、提交 SHA、环境、实际现象、通过／失败／待验及日志或截图。失败单附可复现步骤和脱敏 Run ID。真实模型与部署各自签收。
