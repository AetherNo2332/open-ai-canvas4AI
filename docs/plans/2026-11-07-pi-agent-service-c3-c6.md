# Pi Worker Runtime C3–C6 续作记录

本记录承接 Agent 服务 A0–A9 分支的前端与迁移验收收口。原会话摘要引用的 C0–C4 提交和文件不在当前 `feat/agent-service-sdk` 工作树中；当前树仍包含 Go 业务轮次和画布 Pi Worker 两条执行路径。因此本次只把可回溯的 C5/C6 边界落到现有代码，并把缺失的 C0–C4 基线记录为裁定，不能把现有 Go 轮次描述为已迁入同源 Pi Worker。

## C5：前端 Session controller

- 为同一 Session 的并发读取建立请求序号和活动请求计数。
- 旧请求失败不能覆盖新请求成功；迟到旧响应只能补齐缺失序号，不能覆盖新同序消息。
- 页面关闭订阅只停止本地轮询，服务端工作继续。

## C6：迁移合同与证据入口

- `database.AgentServiceMigrationContract` 是 Agent Session、消息、工作、派工、预算、审批、工具操作以及 Cloud Agent/Pi 历史的 SQLite→PostgreSQL 复制清单。
- `migrate-sqlite-postgres` 按合同顺序构建新 Agent 表清单，并由总迁移测试覆盖旧 Cloud/Pi 表。
- `scripts/verify-agent-service-evidence.py` 只验证真实 smoke 报告的字段完整性、Compose 隔离、Pi Worker 协议和脱敏标记；它不替代实际 Compose smoke。
