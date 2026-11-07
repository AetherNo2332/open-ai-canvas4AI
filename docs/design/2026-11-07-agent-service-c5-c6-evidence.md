# Agent Service C5–C6 证据

## 当前基线

本工作树是 `feat/agent-service-sdk` 的 A0–A9 实施分支。它包含非画布 Agent 的持久 Go 运行器、业务 Session/工作/预算和画布 Pi Worker，但当前树没有会话摘要中引用的 C0–C4 通用 Pi 执行文件。因此本次证据只覆盖实际修改的 C5/C6 边界；同源 Pi Worker 迁移的 C0–C4 证据不能从这棵树补写出来。

## C5 controller

`web/src/sdk/agent/controller.ts` 为每个 Session 维护最新读取请求序号和活动请求数。读取结果始终按目标 Session 合并；同 Session 的旧失败不会污染更新后的成功状态，旧响应只能补齐尚不存在的消息序号。`loading` 在最后一个并发读取结束前保持为真，切换 Session 不会把回调写入当前活动 Session。

`web/test/agent-sdk.test.ts` 覆盖 Session 切换、并发读取、迟到失败、同序重复消息、草稿和订阅停止。

## C6 migration contract

`backend/internal/database/migrations_agent_contract.go` 导出 `AgentServiceMigrationContract`。清单包括：

- Session、Session message、memory note；
- work、assignment、report、input、outbox、approval、turn；
- tool operation、budget grant、budget charge；
- Cloud Agent execution、event/message journal、canvas mutation；
- Pi Session 和 Pi entry。

每一项要求保留主键与来源顺序。新 Agent 表由 `backend/cmd/migrate-sqlite-postgres/creation_platform.go` 按合同排序；旧 Cloud/Pi 表仍由迁移器主清单负责。`TestAgentServiceMigrationContractCoversRuntimeIdentity` 与 `TestAgentServiceMigrationContractIsCopied` 检查合同覆盖和复制清单覆盖。

## Cross-process evidence contract

真实 Compose smoke 产出的 JSON 可以交给：

```text
python -B scripts/verify-agent-service-evidence.py --report <evidence.json>
```

校验器要求报告包含唯一 Compose project、PostgreSQL/Redis/Backend/Agent/Web/确定性模型服务、脱敏标记、Pi Worker 协议与 Session format，以及 `SessionID`、`RunID`、`TriggerMessageID`、lease epoch、WorkItem、Assignment epoch、BudgetGrant、AuthorizationVersion、Task、Operation、Resource、Order 身份。它只校验证据完整性，不启动容器，也不把 Go 运行器测试冒充 Node/Pi 跨进程验收。
