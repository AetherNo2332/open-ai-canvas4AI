# 贡献指南

感谢参与 Open AI Canvas。提交改动前，请先确认相关 Issue 或用简短说明描述问题、目标和外部行为变化。

## 开发环境

- 前端：Bun、Vite、React、TypeScript。
- 后端：Go、Gin、GORM、SQLite。
- 本地全栈：`docker compose up -d --build`。

不要提交 `.env`、数据库、数据目录、生成产物、编辑器配置或真实 API/OSS 密钥。新增配置项时只在 `.env.example` 中提供无敏感信息的说明。

### 依赖外部服务的后端用例

后端有 5 个用例默认跳过，必须备齐依赖才会真正执行（CI 的 Backend job 已经提供，并在缺失时直接失败）：

- 3 个 PostgreSQL 用例（`internal/database`、`internal/canvas`、`internal/repository`）需要 `CANVAS_TEST_POSTGRES_DSN` 指向一个可以随意建删 schema 的一次性库：
  ```bash
  docker run -d --name canvas-test-pg -p 55432:5432 \
    -e POSTGRES_PASSWORD=canvas_test -e POSTGRES_DB=canvas_test postgres:16-alpine
  cd backend && CANVAS_TEST_POSTGRES_DSN="postgres://postgres:canvas_test@127.0.0.1:55432/canvas_test?sslmode=disable" \
    go test ./internal/database/ ./internal/canvas/ ./internal/repository/ -count=1
  ```
- 2 个 Redis 用例（`internal/platform`、`internal/app`）要求 `PATH` 上有 `redis-server` 可执行文件：用例自己拉临时实例（unix socket），不需要额外部署 redis 服务。

## 提交要求

- 保持改动聚焦，沿用现有目录职责、命名和错误处理风格。
- 写路径必须明确失败；不可用默认值掩盖保存、权限、生成或删除错误。
- 后端对象读取必须同时校验当前用户和资源归属。
- 涉及上传、外部请求或模型调用时，必须说明大小、频率、费用和 SSRF 边界。
- 用户可见文案使用中文；核心业务入口和安全边界使用简短中文注释说明原因。

Pull Request 应包含改动摘要、风险、验证方式和必要截图。提交即表示你有权按本项目 MIT 许可证贡献相关代码或素材，并保留已有上游署名和许可证通知。
