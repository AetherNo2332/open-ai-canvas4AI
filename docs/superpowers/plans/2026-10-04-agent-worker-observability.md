# Agent + Worker 可运营观测闭环 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 在最新 canary 上重新实现 Agent/Worker 运行观测闭环，并把同一套聚合口径接入 Web 管理员“运行总览”，覆盖队列、运行终态、重试、工具、Token、成本、质量和告警。

**Architecture:** 后端建立统一观测契约和有界聚合器，任务生命周期、Worker、LLM、工具、压缩事件通过同一入口记录；管理员接口只返回脱敏聚合结果和最近异常。前端运行总览合并运行观测与现有历史分析。OTLP/Prometheus/Tempo/Langfuse 采用可选适配层，观测故障不阻塞任务。

**Tech Stack:** Go、Gin、GORM/SQLite、React/TypeScript、TanStack Query、Ant Design、现有 admin token/CSS。

**Spec:** docs/superpowers/specs/2026-10-03-agent-worker-observability-design.md

## Global Constraints

- 所有 task、run、trace 使用相同关联字段；指标标签只允许低基数维度。
- 不把提示词、工具参数、上传内容、API Key、Cookie、签名 URL写入指标或管理员聚合响应。
- 未知成本返回 null/缺失状态，不能显示为零成本；观测后端故障不得阻塞 Agent 任务。
- 任务成功必须由统一终态映射，HTTP 200、模型成功或 Worker 无异常不能单独判为成功。
- 管理员接口继续使用现有管理员鉴权，前端不暴露观测后端凭据或内部地址。
- 默认 Compose 不新增必需服务；观测组件通过可选 profile/环境变量启用。

## Review Focus

- 运行终态与 Agent/Worker 原始状态不一致时，管理员看到统一终态；覆盖 failed、expired、cancelled。
- 无观测数据或采集器不可用时，接口返回暂无数据/延迟/不可用语义，前端不渲染为 0。
- 高基数 task/run/trace 不进入指标标签；固定白名单测试。
- 重试保留 task 关联并生成独立 run 计数；覆盖租约、模型、工具重试。
- 成本价格未知时 cost 为 null，质量评分不改变任务终态。

### Task 1: 观测契约与聚合核心

**Files:**
- Create backend/internal/observability/contract.go
- Create backend/internal/observability/aggregator.go
- Create backend/internal/observability/aggregator_test.go
- Modify backend/internal/service/service.go
- Modify backend/internal/app/cloud_agent_runtime.go

**Interfaces:**
- Event{TaskID, RunID, TraceID, SessionID, AgentVersion, PromptVersion, Model, Pool, Role, Kind, Status, StartedAt, EndedAt, InputTokens, OutputTokens, CachedTokens, ReasoningTokens, CostMicrocredits *int64, RetryOf string}
- Aggregator.Record(Event), Aggregator.Snapshot(window time.Duration) Snapshot
- Snapshot 提供 queue、worker、task、tool、LLM、cost、quality、alerts 聚合字段。

- [ ] 写失败测试固定字段、终态映射、低基数标签、未知成本和重试关联。
- [ ] 实现有界事件缓冲与快照聚合；超过容量丢弃低优先级原始事件但保留终态计数。
- [ ] 将 task lifecycle、claim、LLM、tool、compaction、finish 接入统一记录入口。
- [ ] 运行 Go 单测。

### Task 2: 管理员运行观测接口

**Files:**
- Create backend/internal/app/admin_observability.go
- Create backend/internal/handler/admin_observability.go
- Create backend/internal/handler/admin_observability_test.go
- Modify backend/internal/handler/api.go
- Modify backend/internal/service/aliases_types.go

**Interfaces:**
- GET /api/admin/observability/overview?window=15m
- Response: {available, delayed, generatedAt, window, worker, queue, tasks, tools, llm, cost, quality, alerts, recentFailures[]}
- recent failure 只返回脱敏 ID、状态、原因摘要和 trace 跳转标识。

- [ ] 写鉴权、窗口校验、空数据和脱敏响应测试。
- [ ] 实现管理员 service/handler，并在观测后端不可用时返回可区分状态。
- [ ] 运行 handler/app 测试。

### Task 3: Agent/Worker 业务埋点与可靠降级

**Files:**
- Modify backend/internal/app/cloud_agent_runtime.go
- Modify backend/internal/app/cloud_agent_pi_bridge.go
- Modify backend/internal/app/cloud_agent_tool_dispatch.go
- Modify backend/internal/app/cloud_agent_context_compaction.go
- Create backend/internal/observability/exporter.go
- Create backend/internal/observability/exporter_test.go

**Interfaces:**
- Exporter.Emit(Event) error 非阻塞；默认 noop/内存实现。
- context.compaction 事件包含触发原因、前后 token、算法版本和结果。
- exporter 超时/队列满只记录丢弃计数，不改变 Agent 运行结果。

- [ ] 写 exporter 故障不影响终态和有界队列测试。
- [ ] 接入 Pi bridge、工具 dispatch、压缩、租约/重试路径。
- [ ] 运行 Agent 回归测试。

### Task 4: Web 管理员运行总览

**Files:**
- Modify web/src/services/api/auth.ts
- Create web/src/services/api/observability.ts
- Create web/src/pages/admin/components/observability-overview-panel.tsx
- Modify web/src/pages/admin/admin-route-pages.tsx
- Modify web/src/styles/admin-ui.css
- Create web/src/pages/admin/components/observability-overview-panel.test.tsx

**Interfaces:**
- getAdminObservabilityOverview(window): Promise<AdminObservabilityOverview>
- 页面展示 Worker/队列、成功/失败/重试、P50/P95/P99、工具成功率、步骤、Token、cost/success、质量、告警和脱敏失败列表。
- 空态、延迟、不可用态分别呈现，不用 0 替代缺失数据。

- [ ] 写 API schema 与空态渲染测试。
- [ ] 实现主题 token、响应式卡片和失败详情入口。
- [ ] 将面板嵌入现有运行总览，保留既有财务分析。
- [ ] 运行前端类型检查与相关测试。

### Task 5: 可选观测 Compose、Golden/告警文档与验收

**Files:**
- Create docker-compose.observability.yml
- Create backend/internal/observability/golden.go
- Create backend/internal/observability/golden_test.go
- Create docs/runbooks/agent-worker-observability.md
- Modify .env.example
- Modify CHANGELOG.md

**Interfaces:**
- Compose profile observability 提供 Collector、Prometheus、Tempo、Grafana；Langfuse 通过显式外部配置启用。
- Golden case 记录 deterministic checks、eval/dataset/judge 版本，不写入未经审核原文。
- Runbook 定义队列增长、Worker 全不可用、错误率/重试率、成功率、P95、token/cost 突增告警和恢复条件。

- [ ] 写 Golden case schema、版本记录和脱敏测试。
- [ ] 添加可选 Compose 配置和最小健康探针。
- [ ] 本地 Compose 启停冒烟；观测组件不可用时任务仍可完成。
- [ ] 更新发行说明并完成全量验证。

## Execution Order

Task 1 -> Task 2 -> Task 3 -> Task 4 -> Task 5。每个任务完成后运行专属测试并提交独立 commit；最终合并前执行 Go、Web、Compose 验收。
