# Agent + Worker 可运营观测闭环设计

## 状态

已确认的架构设计：OpenTelemetry Collector 统一采集，Prometheus/Tempo/Grafana 分别承担指标、Trace 和运营看板，Langfuse 承担 LLM/工具/token/cost/quality 观测。

## 目标

把 Agent + Worker 集群从“服务进程可用”提升到可运营状态，能够回答三类问题：

1. 机器和 Worker 是否正常工作，队列是否正在积压。
2. 某个任务为什么慢、失败或反复重试，时间消耗在哪个阶段。
3. Agent 是否真正完成用户任务，质量、效率和成本是否随版本改善。

所有运行指标和任务 Trace 必须使用相同的 `task_id`、`run_id`、`trace_id` 和版本字段关联。HTTP 200、Worker 无异常和模型调用成功不能直接等价为任务成功。

## 非目标

- 第一阶段不把 Prometheus 当作原始 Trace 或用户内容存储。
- 不把提示词、工具参数、上传内容、API Key、Cookie 或签名 URL 写入指标标签。
- 不用 LLM Judge 替代订单、资源、工具结果等可确定验证。
- 不在没有可靠价格快照时把成本记为 0。
- 不改变现有 Agent 运行状态机、任务权限和账务状态机的业务语义。

## 总体架构

```text
Web/API -> Backend admission -> Queue -> Agent/Worker
                                      |
                                      v
                             OpenTelemetry SDK
                                      |
                              OTel Collector
                         /          |           \
                  Prometheus      Tempo       Langfuse
                       |             |             |
                   Grafana       Trace UI      GenAI/Eval
```

应用侧只依赖一个本地观测契约和 OTLP exporter。Collector 负责批量、重试、采样、脱敏和分流；业务请求不能因为观测后端暂时不可用而失败。指标、Trace 和 Langfuse 发送必须采用有界队列，超过上限时丢弃低优先级原文，但保留任务终态和聚合计数。

## 观测身份与版本

每个 Agent task 在入队时生成或继承：

| 字段 | 语义 | 标签/属性限制 |
|---|---|---|
| `task_id` | 业务任务身份 | Trace 属性；不作高基数指标标签 |
| `run_id` | 一次运行/重试实例 | Trace 属性；不作指标标签 |
| `trace_id` | 分布式 Trace 身份 | W3C trace context；不作指标标签 |
| `session_id` | 会话身份 | 仅 Trace 属性，脱敏或哈希 |
| `agent_version` | Agent 构建版本 | 允许作为低基数标签 |
| `prompt_version` | Harness/策略版本 | 允许作为低基数标签 |
| `model` | 逻辑模型标识 | 归一化后允许作为低基数标签 |
| `worker_id` | Worker 实例身份 | Trace 属性；指标使用池/角色而非实例 ID |

`trace_id` 通过队列消息、内部 Agent API、Pi bridge 和工具执行上下文传递。没有上游 Trace 时由后端创建；重试保留 `task_id`，每次 `run_id` 独立，但通过 `retry_of` 关联。

## Span 与事件契约

每个 task 至少包含以下 span：

- `task.lifecycle`：入队到终态的根 span。
- `queue.wait`：入队到 Worker claim。
- `worker.claim`：领取、租约、恢复和释放。
- `agent.execute`：Agent 一轮或整个运行的执行区间。
- `llm.call`：模型、协议、输入/输出/cached/reasoning token、响应状态和延迟。
- `tool.call`：工具名、版本、状态、延迟、重试次数和安全分类。
- `context.compaction`：触发原因、压缩前后 token、算法版本和结果。
- `task.finish`：完成、失败、取消、拒绝和用户中断等终态。

事件属性使用结构化枚举：`queued`、`claimed`、`running`、`waiting_model`、`waiting_tool`、`compacting`、`completed`、`failed`、`cancelled`、`expired`。同一任务的任务状态、事件流和 Trace 终态必须由后端统一映射，避免 Agent/Worker 各自定义“成功”。

## 第一批指标

### Worker/Queue

- `agent_worker_up`：按池和角色记录存活 Worker 数。
- `agent_worker_busy_ratio`：忙碌 Worker / 可用 Worker。
- `agent_queue_depth`：可领取任务数。
- `agent_queue_oldest_task_age_seconds`：最老可领取任务等待时间。
- `agent_task_throughput_total`：按终态计数的完成吞吐。
- `agent_task_error_total`：Worker/Agent 失败数。
- `agent_task_retry_total`：租约、模型、工具和运行重试数。
- `agent_task_success_total`：业务任务真正成功数。
- `agent_task_latency_seconds`：端到端、排队、Agent、LLM、工具分段直方图。
- `agent_tool_calls_total` 与 `agent_tool_success_total`：按工具类别和结果计数。
- `agent_steps_per_task`：Agent step 数分布。
- `agent_llm_tokens_total`：input/output/cached/reasoning 分开计数。
- `agent_llm_calls_total`：每任务模型调用数。
- `agent_cost_microcredits_total`：模型/工具成本，价格未知时不写入成功值。

指标标签只使用 `environment`、`pool`、`role`、`model`、`agent_version`、`prompt_version`、`tool_type`、`status` 等低基数维度。

## 任务成功与质量

任务成功由后端任务终态和领域验证共同决定：

1. **确定性检查**：工具是否真实成功、资源/订单状态是否符合预期、必需字段是否存在、权限和副作用是否完成。
2. **Judge 评分**：仅评估正确性、相关性、表达和约束遵循等无法完全确定的维度。
3. **人工标注和用户反馈**：点赞/点踩、重新提问、纠正、取消和放弃作为独立信号。

质量记录必须包含 `eval_version`、`dataset_version`、`judge_model`、`deterministic_checks` 和评分依据，不能只保存一个无来源的总分。`quality_score` 不参与任务终态判定，除非具体 Golden Case 显式声明阈值。

## Golden Dataset

建立版本化的 Golden Dataset，每个 case 包含：输入摘要、预期结果、允许工具、禁止工具、确定性断言、质量维度和脱敏样例。每次 Agent 版本、Prompt/Harness、模型或工具契约变更，执行离线回归并记录：成功率、P95、步骤数、token、cost/success、确定性失败类型和 Judge 分布。

生产 Trace 只用于抽样回放和线上评估，不能自动把未经审核的用户内容写入 Golden Dataset。

## Grafana 与告警

第一版包含四个看板：

1. **Agent Overview**：请求量、真实成功率、P95、失败/重试率。
2. **Worker Cluster**：Worker up/busy、队列深度、最老任务、吞吐和租约异常。
3. **Agent Quality**：工具成功率、步骤数、token/task、质量分布、用户反馈。
4. **Cost**：input/output/cached/reasoning token、模型调用数、cost/task、cost/successful task。

告警初始只使用相对稳定的条件，并要求持续窗口和恢复条件：队列深度持续增长、最老任务超时、busy ratio 饱和且队列增长、任务错误率/重试率异常、成功率下降、P95 超阈值、token 或 cost 突增、Worker 全部不可用。告警消息只包含聚合维度和 Trace 查询入口，不包含用户正文。

## 部署与可靠性

- 本地 Compose 提供可选的 Collector、Prometheus、Tempo、Grafana 和 Langfuse profile，不影响默认开发启动。
- 生产部署通过独立的观测 Compose 覆盖文件接入，观测组件故障不阻塞 Agent 任务。
- Collector 使用批处理、内存限制、重试和有界队列；应用 exporter 超时后快速降级。
- Trace 采样按错误、慢任务、重试和质量抽样优先；健康任务按比例采样。
- 观测数据保留期限、Langfuse 原文开关和脱敏规则由环境变量显式配置。
- 版本发布同时记录 `agent_version`、`backend_version`、`prompt_version` 和 `schema_version`，方便回归比较。

## 测试与验收

- 单元测试：状态映射、指标名称/标签白名单、Trace parent 传播、token/cost 聚合、质量结果合并、脱敏。
- 后端集成测试：入队→claim→LLM→tool→终态的完整 Trace；重试、取消、过期和压缩路径保持同一关联身份。
- Agent 测试：Pi bridge、工具调用、上下文压缩、Worker 租约续期和错误终态均产生正确 span/event。
- Golden Dataset 测试：固定 case 的确定性断言、Judge 版本记录和结果对比。
- Compose 冒烟：Collector、Prometheus、Tempo、Grafana、Langfuse 启停；应用观测后端不可用时任务仍可完成。
- 验收指标：能够从一个 `trace_id` 看到排队、Worker、LLM、工具、压缩和终态；Grafana 能展示八项首批核心指标；一次真实失败不会被显示为成功；成本未知不会显示为零成本。

## 分阶段交付

1. **观测契约**：定义 Go/TypeScript 共用字段、span 名称、状态映射、脱敏和版本策略。
2. **应用埋点**：接入后端 task/queue、Agent runner、Pi bridge、工具桥和 token/cost 解析。
3. **Collector 与存储**：加入可选 Compose profile、OTLP pipeline、Prometheus/Tempo/Langfuse exporter 和保留配置。
4. **质量闭环**：Golden Dataset、确定性检查、Judge 适配、人工反馈和线上抽样。
5. **运营交付**：Grafana dashboards、告警规则、Runbook、文档、专项测试和本地 Compose 验收。

