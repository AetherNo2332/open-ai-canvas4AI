# 贡献指南

感谢参与 Open AI Canvas。提交改动前，请先确认相关 Issue 或用简短说明描述问题、目标和外部行为变化。

## 开发环境

- 前端：Bun、Vite、React、TypeScript。
- 后端：Go 1.25、Gin、GORM；开发可用 SQLite，生产编排使用 PostgreSQL、Redis。
- 本地全栈：`docker compose up -d --build`。

源码热更新、Agent 单独启动与定向验证见[本地开发](docs/content/docs/backend/local-development.mdx)。完整本地 Agent 使用 `docker-compose.local.yml`；根默认编排需显式启用 `pi` profile。

不要提交 `.env`、数据库、数据目录、生成产物、编辑器配置或真实 API/OSS 密钥。新增配置项时只在 `.env.example` 中提供无敏感信息的说明。

### 依赖外部服务的后端用例

CI 强制检查至少6条依赖服务的验收记录：3条 PostgreSQL 业务检查、1条跨后端用户配额检查和2条 Redis 检查。必须备齐依赖；缺失、跳过或失败都会被 Backend job 拒绝。其余使用相同依赖的用例也应实际执行：

- 3 个 PostgreSQL 用例（`internal/database`、`internal/canvas`、`internal/repository`）需要 `CANVAS_TEST_POSTGRES_DSN` 指向一个可以随意建删 schema 的一次性库：
  ```bash
  docker run -d --name canvas-test-pg -p 55432:5432 \
    -e POSTGRES_PASSWORD=canvas_test -e POSTGRES_DB=canvas_test postgres:16-alpine
  cd backend && CANVAS_TEST_POSTGRES_DSN="postgres://postgres:canvas_test@127.0.0.1:55432/canvas_test?sslmode=disable" \
    go test ./internal/database/ ./internal/canvas/ ./internal/repository/ -count=1
  ```
- Agent 准入审计使用独立的 `TEST_AGENT_POSTGRES_DSN`，且要求数据库名为 `canvas_event_integration`，以免误用业务库。CI 为它单独创建数据库；本地可在上述一次性 PostgreSQL 容器中创建后运行：
  ```bash
  docker exec canvas-test-pg psql -U postgres -d postgres -c 'CREATE DATABASE canvas_event_integration'
  cd backend
  TEST_AGENT_POSTGRES_DSN="postgres://postgres:canvas_test@127.0.0.1:55432/canvas_event_integration?sslmode=disable" \
    go test ./internal/database -run TestAgentAdmissionAuditPostgres -count=1
  ```
- 2条 Redis 检查（`internal/platform`、`internal/app`）要求 `PATH` 上有 `redis-server` 可执行文件：用例自己拉临时实例（unix socket），不需要额外部署 redis 服务。

## 更新日志

`CHANGELOG.md` 的条目同时用于 GitHub Release 说明：发布流程从根 VERSION 的内部版本派生正式 tag 和镜像 tag，并要求日志有精确内部版本标题。CI 的 `Changelog entry` 也校验该标题。正式版说明以本 fork 上一正式发布提交的 VERSION 为边界聚合，首次发布仅取当前条目；不使用继承的上游本地 tag 推断 fork 发布历史。提交前用脚本生成草稿，再改写成用户可见的措辞：

```bash
node scripts/changelog.mjs                 # 预览：从 CHANGELOG 顶部条目的 commit 到 HEAD
node scripts/changelog.mjs --write         # 写入 CHANGELOG.md 顶部，并同步 bump VERSION
node scripts/changelog.mjs --write --force # 同版本条目已存在时重新生成
node scripts/changelog.mjs --since <sha>   # 多线合并历史里起点不可达时手动指定
node scripts/changelog.mjs --check         # 校验当前版本是否有日志条目（CI 用）
```

脚本只按提交类型归并标题，不判断用户可见影响：**发布前必须改写措辞并补充升级 / 迁移说明**，不要把 `git log` 原样提交。

## 提交要求

- 保持改动聚焦，沿用现有目录职责、命名和错误处理风格。
- 写路径必须明确失败；不可用默认值掩盖保存、权限、生成或删除错误。
- 后端对象读取必须同时校验当前用户和资源归属。
- 涉及上传、外部请求或模型调用时，必须说明大小、频率、费用和 SSRF 边界。
- 用户可见文案使用中文；核心业务入口和安全边界使用简短中文注释说明原因。

Pull Request 应包含改动摘要、风险、验证方式和必要截图。提交即表示你有权按本项目 MIT 许可证贡献相关代码或素材，并保留已有上游署名和许可证通知。

## 责任与审查

本 fork 的维护入口是 [AetherNo2332/open-ai-canvas4AI](https://github.com/AetherNo2332/open-ai-canvas4AI)。提交者负责给出实现与验证证据；审查者负责核对外部行为、权限、失败恢复和迁移影响。发布执行者负责记录目标提交、版本、镜像 digest、备份位置和回退结果。角色可以由同一维护者暂时承担，但不能据此声称已有第二维护者或独立复核。

涉及账务、对象归属、数据库迁移、Agent 租约、凭据、更新与回退的改动应有独立审查记录。没有独立审查时，在交付中标明缺口；不要用自动检查通过代替人工责任。模块责任随实际维护工作交接，不填写虚构的 CODEOWNERS 账号。

发布前按[工程治理与交接](docs/content/docs/backend/engineering-governance.mdx)记录验收。第二维护者需独立完成一次隔离安装、升级与回退演练，并留下实际输出，才能作为可接班证据。生产操作仍需单独授权。
