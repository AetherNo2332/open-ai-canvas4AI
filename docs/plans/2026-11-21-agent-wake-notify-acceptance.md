# Agent Wake Event 通知默认值验收

## 交付范围

PostgreSQL 未配置 `CANVAS_AGENT_WAKE_MODE` 时使用集中 LISTEN/NOTIFY，SQLite 默认 poll；显式 `poll` 保留 250 ms 轮询。Schema 110 通过现有迁移安装语句级通知触发器。通知只有唤醒作用，事件仍持久化，断线按原游标及授权补读。计数器行锁、sequence、生产者事务和 lease 语义不改。

实现基于 `d62c36fbb` 的未提交工作树完成隔离验收；PR 重基到 `2507eea29`，保留 #143 的 `database/agentwake.Append` 写入护栏。重基后在真实 PostgreSQL 重新运行默认启动、单 listener、并发领取与 Pi Worker 恢复测试，均实际执行并通过。没有生产部署。

## 实测证据

| 检查 | 实际结果 |
| --- | --- |
| `/claim` 与 Worker 恢复 | poll、notify 下各两次并发 HTTP 领取均为 2xx，恰一快照及一个 `run:null`；Run 和 Session owner 一致。持久工具回执后中断，租约自然过期，新执行者恢复原 active leaf，epoch 从 1 到 2；旧 `/renew` 与 `/messages` 均被 403 拒绝。原工具回执、唯一 mutation、唯一节点及不再增长的 revision 证明无副作用重放。 |
| SQLite 默认 poll | 无模式变量和 PG DSN；实际为 poll、0 listener。从游标 1 到固定水位 272 跨页收到可见集合 163 条，包含合法广播，五类对账差错均为 0。 |
| 三组事务及回退配对 | 三组 poll/notify 共 18 个事务场景，54 条事务对账和 27 条回退/重连对账全部零差错；listener 为 1→0→1，恢复先补读 2,546 条。 |
| 干净 PG 默认启动 | 不设模式，正常迁移 v110 安装触发器并连接专用 listener。抑制通知后 600 ms 无投递；正常通知后 83.37 ms 补齐，未触发 fallback。显式 poll 无 listener，抑制通知的写入仍在 251.30 ms 送达。 |
| 既有 54 轮 A/B | 500 空闲 SSE 全部 Wake 读取 QPS 2016.62→0.199；100/s 投递 p95 237.926→1.914 ms；混合/突发 p99 248.229/248.713→3.239/2.936 ms。热点无合法投递样本。此 PR 不重跑矩阵。 |

54 轮及三组配对均为前述隔离验收构建；最终验收后端与三组配对后端字节相同。PR 重基保留相同通知读侧、触发器和事务语义；增加空仓储判断，避免仅注册路由的测试构造空 Service 时崩溃。重基后按影响面定向复验，不把历史测量冒充 PR 新一轮性能压测。

403「Pi 模型请求缺少服务端策略」来自测试 stdin 逐块转字符串损坏跨块 UTF-8 字符；冻结策略和 claim 快照相同。测试驱动合并 Buffer 后一次解码修复。正常 Worker 直接解析 HTTP JSON，服务端完整策略校验、签名和权限未放宽。独立测试容器需要挂载同版本官方 Chat Completions 插件。

## 复跑入口

- `backend/`：`go test -count=1 -v ./internal/handler -run '^TestAgentWake(SQLiteDefaultPoll|PostgresDefaultNotify|NotifyUsesOneListenerPostgres|ClaimRacePostgres)$'`。PostgreSQL 必须提供隔离 `canvas_event_integration` 的 `TEST_AGENT_POSTGRES_DSN`；缺 DSN 的 skip 不算验收。先构建同版本 Agent，并提供 Node、Bun 及官方协议插件。
- 仓库根：`bun scripts/verify-before-push.mjs --base canvas4ai/main --mode affected`，在干净提交上执行；本地计划和原始日志保存在 `.local/cache/verify/`。
- SQLite、PG、HTTP、Worker 和 Pi SDK 均真实运行；模型使用确定性测试提供方。外部供应商模型、真实业务页面和生产部署未验收。

完整原始证据在交付目录 `outputs/agent-wake-final-acceptance-20261121/`，包括 `report.md`、`final-build.json`、`final-notify-fenced.log`、`final-notify-sqlite.log`、`final-notify-startup/` 与 `final-acceptance-03/`。历史 54 轮在 `outputs/agent-wake-pg-notify-ab-20261121/`。原始日志和本机路径不提交到仓库。
