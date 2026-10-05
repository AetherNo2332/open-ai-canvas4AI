# Agent/Worker 观测闭环验收文档

验收日期：2026-10-04（Asia/Shanghai）  
验收环境：本机 Docker Compose 项目 canary-3000  
源码分支：canary  
验收基线：ddec70f6  
显示版本：v1.5.7.2(1e8c16a)

## 1. 验收范围

本次验收覆盖：

- Agent/Worker 运行、队列、模型调用、工具调用和上下文压缩事件的统一观测模型。
- 有界本地聚合器和可选 OTLP exporter。
- 管理员观测总览接口。
- Web 管理后台观测面板及 Grafana 跳转入口。
- 本地 Compose 镜像构建、容器重建和健康检查。
- 观测数据脱敏、管理员权限和失败降级行为。

Grafana、Tempo、Prometheus、OTel Collector 的 Compose 配置纳入验收范围，但本次 canary-3000 验收未启动独立 observability profile；Grafana 跳转地址使用已有本机入口。

## 2. 部署过程

使用当前 canary 源码重建 backend 和 web：

    docker compose -p canary-3000 -f docker-compose.yml -f .local/compose-canary-3000.override.yml up -d --build backend web

部署过程中只重建 canary-3000 项目的 backend/web，未删除数据卷，未操作其他 Compose 项目。

## 3. 容器验收结果

| 容器 | 结果 |
| --- | --- |
| canary-3000-backend-1 | Up (healthy) |
| canary-3000-web-1 | Up (healthy) |
| canary-3000-agent-1 | Up (running) |

Backend 健康信息：

- 服务状态：ok
- ready：true
- 数据库检查：通过
- Runtime 检查：通过
- Schema：当前 45，期望 45
- 版本：v1.5.7.2(1e8c16a)

## 4. HTTP 验收结果

| 检查项 | 请求 | 实际结果 | 判定 |
| --- | --- | --- | --- |
| Web 首页 | GET http://localhost:3000/ | HTTP 200 | 通过 |
| 后端就绪 | GET /api/health/ready | HTTP 200 | 通过 |
| 未认证观测接口 | GET /api/admin/observability/overview | HTTP 401 | 通过，权限保护生效 |

管理员观测接口路径：

    GET /api/admin/observability/overview

接口要求管理员身份，并限制时间窗口在 60 秒至 24 小时之间。响应只返回聚合指标、脱敏 Trace 标识和可查询维度，不返回用户正文、工具参数、密钥或签名 URL。

## 5. 配置验收

Backend 容器实际环境变量：

    CANVAS_GRAFANA_URL=http://localhost:3001/d/agent-overview
    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=

空 OTLP endpoint 表示本地观测 exporter 默认关闭；本地聚合器仍工作。启用观测栈时，应将 endpoint 指向：

    http://otel-collector:4318/v1/traces

Compose 配置校验：

    docker compose -f docker-compose.local.yml -f docker-compose.observability.yml --profile observability config --quiet

结果：通过。

## 6. 自动化验证

| 范围 | 结果 |
| --- | --- |
| 观测聚合器、exporter、Golden case、handler、app 专项 Go 测试 | 通过 |
| 管理员观测投影与非管理员拒绝测试 | 通过 |
| Web 观测 URL 测试 | 2/2 通过 |
| bun run typecheck | 通过 |
| bun run build | 通过 |
| git diff --check | 通过 |
| Go 全仓库测试 | 未完成；相关包专项已通过，完整 app/repository 执行超过 3 分钟后未继续等待 |

## 7. 降级和边界结果

- OTLP exporter 使用有界队列；队列满或 exporter 超时时只增加丢弃计数，不改变任务终态。
- 本地 Agent 容器仍请求当前环境未提供的 /internal-agent/* 路由，日志中出现 404。这是现有本地 Agent 协议配置问题，不影响 backend/web 健康检查和观测接口权限验证。
- 本次未执行真实模型收费请求、长期压力测试、多节点故障演练或 Grafana 看板人工视觉验收。
- 因此，本文件证明的是本机 canary 的构建、启动、健康和接口级验收，不等同于生产环境验收。

## 8. 验收结论

本次 Agent/Worker 观测闭环在本机 canary-3000 部署成功。backend 和 web 均健康，版本和 schema 正确，管理员观测入口已注册且未认证请求被拒绝，Grafana 跳转配置已注入，相关自动化检查通过。

结论：**本机部署验收通过；生产验收待补充真实 Agent 协议、观测栈运行、管理员浏览器流程和长期压力数据。**
