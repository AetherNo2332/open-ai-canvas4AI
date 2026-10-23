# 画布与后端缺口补齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 补齐画布一致性、跨设备Agent历史、资产恢复、协作状态与OSS交付。
**Architecture:** 保留自研commands/reference/task模型，在持久仓储边界连接所有写入口；上层按已有权限/服务别名与前端scope接入，协议迁移不替换Agent记忆。
**Tech Stack:** Go1.25/GORM/SQLite/PostgreSQL/Redis，React19/TypeScript/Zustand/Bun。
**Spec:** docs/design/canvas-backend-parity-spec.md

## Global Constraints
- 用户已明确要求整理后开始；持续执行，按已有授权自行完成可逆实现。
- 保留多Agent会话、当前皮肤和圆角、显式S3 path-style、不代理浏览器OSS字节。
- 不改Agent记忆/compaction/长期偏好算法；不部署、不合并/推送main。
- 每任务新增有意义的行为回归测试，只运行对应专项；最终一次受影响类型/合同检查。
- 并行编辑必须按文件所有权分离，根代理统一提交/元数据，worker不改Git index/HEAD。

## Review Focus
- 命令epoch已建立后旧PUT/Agent改同字段：旧实体版本必须拒绝，活跃页能发现更新（Task1/6）。
- 同账号不同画布并发创建/增长：配额不被总量读写竞态绕过（Task1）。
- 切账号和未知POST身份：历史刷新/重试不能串号、覆盖草稿或重复付费（Task2/3）。
- SVG/XML/未知MIME及公开CDN未转发附件参数：不能内联主动内容或静默代理字节（Task4）。
- 租约过期/保存丢ACK/多实例通知断线：回执可回放、草稿保留、任务结果继续落库（Task5/7）。

## Task 1: 统一持久写入、账号配额及多实例协调
Files: repository/canvas_commands.go、repository/repository.go、repository/canvas_history.go及新增协调文件；canvas/commands/{service,host_mutation,history}.go；app/canvas_command_{runtime,validation}.go；handler/canvas_commands.go；专项测试。
Interfaces: 仓储提供用户/画布事务锁、legacy保存同步command版本/事件；命令服务提供事务内quota callback。后续任务以此为所有写入边界。
- [ ] 写跨协议同字段拒绝、legacy事件可读、账号配额及双Service幂等回归。
- [ ] 同用户/画布的数据库锁先于状态读取，避免epoch分叉；保留SQL CAS。
- [ ] 所有保存分支更新命令版本/事件，删除与恢复有明确epoch/失效语义；保持引用/历史资源保护。
- [ ] 命令Create/Execute/Host/Restore接事务内配额，legacy账号总量保持同一数据库协调。
- [ ] 冲突及同操作回执正确映射，运行最小Go专项并记录输出，独立review后提交。

## Task 2: Agent会话跨设备恢复
Files: 新app/cloud_agent_conversations.go、repository/cloud_agent_conversations.go；handler/agent.go、service/aliases*.go、handler/openapi.yaml；web/services/api/agent.ts、cloud-agent-history.ts、cloud-agent-conversations.ts、canvas-cloud-agent-panel.tsx及相邻测试。
Interfaces: GET /agent/conversations?canvasId&before&limit；GET /agent/conversations/:id/runs?after&limit；沿用现有run/events和conversationId。
- [ ] 适配上游018acdfc目录/轮次接口并校验账号与画布归属；原子拒绝过期父轮接续，保留多会话和现有准入/计费。
- [ ] 前端云端分页恢复/事件投影；4秒目录刷新及focus/online唤醒；运行中SSE续读。
- [ ] 本地草稿、pending身份优先保留；账号切换取消旧请求；仍沿用当前画布入口与皮肤。
- [ ] 补跨设备空缓存发现、分页、去重、过期父轮、未知POST和草稿保护行为测试；运行专项并独立review。

## Task 3: 项目资产关联持久重试
Files: web/lib/canvas/project-asset-link-queue.ts、services/project-asset-sync.ts、user-data-sync.ts及必要调用方与测试。
Interfaces: saveCanvasNodeAsAsset返回新增syncPending；持久记录按账号scope隔离。
- [ ] 适配cd694236持久队列、幂等去重、分类/移动恢复、退避/登录/上线重试。
- [ ] 保存成功与关联待同步分开呈现，保留现有取消和删除语义。
- [ ] 覆盖断线刷新、换账号、相同意图合并与不可重试目标；有限Bun专项和review。

## Task 4: OSS附件隔离、展示签名窗及CDN下载
Files: backend/assets/access.go、新content_type.go；storage/{objects,s3,delivery}.go；handler/resource_delivery.go；必要app资源MIME/存储配置及测试。
Interfaces: 稳定展示签名adapter；仅明确合同可用时公开CDN附件下载；无新增浏览器字节代理。
- [ ] 非内联本地附件/sandbox；源站签名附件直连及不安全CDN明确拒绝，真实类型可信判断。
- [ ] 公开CDN下载增加显式响应query转发合同设置/说明，默认安全；源站固定签名窗遵循后台TTL。
- [ ] 保留S3显式开关，四厂商签名窗/响应缓存/附件合同专项测试。
- [ ] 最小Go专项与必要前端设置检查，独立review。

## Task 5: 节点编辑租约与同步状态
Dependencies: Task1。
Files: model/models_canvas_sync.go、database迁移；repository/canvas_edit_leases.go；canvas/edit_leases.go、host.go；app/canvas_bridge.go；handler新协作路由；web/services/canvas-edit-leases.ts、components/canvas/use-canvas-text-lease.tsx及实际编辑组件。
Interfaces: acquire/renew/release证明；状态返回revision/epoch、token-free leases、active Agent summaries；所有写入口检查作者字段proof。
- [ ] 持久30秒节点租约与事务校验；相同用户不同session仍互斥；仅保护作者字段。
- [ ] 主文本/提示词编辑获取续期、保存确认后释放；离线/失效保留草稿。
- [ ] 既有保存状态UI展示占用；API/OpenAPI/数据库文档同步。
- [ ] 覆盖过期/丢ACK/任务进度/恢复删除/多实例，有限专项和review。

## Task 6: 普通画布命令接入及字段级冲突合并
Dependencies: Task1/5；与Task2共享panel外的user-session文件变更需协调。
Files: web/lib/user-session.ts、stores/canvas/use-canvas-session-store.ts、pages/canvas/use-canvas-project-lifecycle.ts、services/canvas-command-session.ts、editor-binding及API；backend/commands合同/planner/version校验必要扩展和generated契约。
Interfaces: 新旧写入统一事件流；字段基线仅用于允许的补丁，结构关系仍用实体版本；租约proof进入同命令。
- [ ] 登录/打开普通画布即接命令workspace，安全处理新画布/规范化/本地未确认草稿。
- [ ] 同节点不同字段安全合并，同字段冲突不覆盖；批量/删除/引用结构维持基线。
- [ ] 验证旧PUT/Agent与命令交叉写、save丢ACK、断网重连和现有生成/历史入口。
- [ ] 专项测试、契约再生成和类型检查，独立review。

## Task 7: 实时失效与presence
Dependencies: Task1/5/6。
Files: canvas/realtime*.go、app/service与仓储提交后通知；handler/canvas_realtime.go；web/services/canvas-realtime-client.ts、画布presence overlay/状态消费；可选Redis协调及测试。
Interfaces: cookie鉴权画布WS房间；提交后invalidate触发既有事件补拉；cursor/drag/device为临时状态。
- [ ] 事务提交后通知覆盖所有写路；Redis跨实例，断线/Redis不可用仍可事件恢复。
- [ ] 设备1–64租期60秒、15秒续期；请求限速、重验归属、清理连接，不将临时坐标落文档。
- [ ] 前端在线端/光标和占用状态，主题/圆角按既有token。
- [ ] 有限WS/Redis行为专项与客户端恢复测试，独立review。

## Task 8: 文档、集成审查与交付
Dependencies: Task1–7。
Files: docs/design及backend API/数据库/代码地图、pending-test；VERSION/CHANGELOG。
- [ ] 维护实际完成清单和设计边界；不把静态测试写成浏览器/厂商验收。
- [ ] 执行一次受影响Go合同/架构检查、前端类型检查及精确专项集；新失败仅按影响补测。
- [ ] 汇总review package并独立整分支审查，修复发现后单次限定复审。
- [ ] 本地主题分支提交，按仓库脚本生成版本/更新日志，保持main与远端不动。

