# Agent/Worker 观测运行手册

## 启用本地观测栈

使用 `docker-compose.observability.yml` 的 `observability` profile 启动 Collector、Prometheus、Tempo 和 Grafana。应用侧设置 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://otel-collector:4318/v1/traces`，管理员跳转地址设置为 `CANVAS_GRAFANA_URL=http://localhost:3001/d/agent-overview`。

观测组件不可用时，Agent 仍使用有界本地聚合器；exporter 超时或队列满只增加丢弃计数，不改变任务终态。

## 告警处置

- `worker_unavailable`：确认 Agent worker 心跳和容量；队列有积压时先恢复 worker，再检查租约超时。
- `queue_wait_exceeded`：查看最老任务的 `task_id`、`run_id` 和 trace；不要直接把排队任务标记为成功。
- `success_rate_low`：按版本、模型和工具类型切分，优先核对 deterministic check，再看 Judge 质量分。
- 重试率或 P95 突增：检查租约续期、模型 400/429、工具 schema 和上下文压缩事件。
- 成本未知：保持为空，补齐价格快照后再重新汇总；不得用零成本掩盖缺失。

告警消息只带聚合维度和 Trace 查询入口，不包含用户正文、工具参数、密钥或签名 URL。

## Golden Dataset

Golden case 必须使用脱敏输入摘要、版本化 dataset/eval、确定性断言和可选 Judge 版本。生产 Trace 只能抽样复盘，人工审核后才能进入数据集。
