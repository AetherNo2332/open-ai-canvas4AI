# C0–C4 接手审计与 C5/C6 交接

本记录区分源码修复、专项验证和完整验收。C0–C4 尚未整体验收，不应据此将 PR 改为 Ready。

## 基线与目标边界

原树 `codex/agent-supervisor-b0b6@b5b30ddec` 干净；隔离分支为 `codex/pi-c0-c4-audit-20261108`。
刷新远端确认 PR #131 OPEN/Draft，head `33cf12b535`、base `d6fdc51699`，仍与本地分叉 16/5。
整合提交 `e7f181b15` 保留两侧历史，合入远端五个 C5/C6 提交，只人工解决版本与日志冲突；未推送。

沿用同源 Pi Worker/Runtime；Go 管身份、持久输入、授权、任务准入、账务、业务与回执。
transport 为 `canvas-pi-wire/v4`，execution 为 `pi-execution/v1`。旧 R-A0-1/R-A0-3 与 B 阶段 Go 循环选择已被取代。
根工作上限 1,000,000，单次审批阈值 100,000 microcredits。

## 当前验收台账

“部分”表示有局部证据，仍缺本条完整场景。所有运行命令与结果以本记录的证据目录为准，历史文档数字不沿用。

| 条目 | 当前结论 | 已验证或已修复 | 仍需闭合 |
| --- | --- | --- | --- |
| C0-A | 部分 | agentservice DTO 专项；真实三角色执行身份 | DTO 与实际 Snapshot 全字段一致；画布纳入统一身份；完整 Scope 负例 |
| C0-B | 通过（清单审计） | 下文列出实际存储、调度、历史与授权位置 | C6 验证数据库切换 |
| C0-C | 通过（文档合同） | 旧裁定取代标记、两层协议、Go/Pi 分工已明确 | 无 |
| C0-D | 部分 | 整合 migration contract，补三张本地新增表 | 在途旧路由、恢复、回退与跨库复制实证 |
| C1-A | 部分 | HTTP 验收实际启动 Node server，短剧由 Pi 执行 | 单独记录续租/心跳与全部身份链 |
| C1-B | 部分 | 双 Session 专项；修复同 Session 后续消息抢 active run | 同项目两 Session entries、workspace 与模型输入交替隔离 |
| C1-C | 部分 | 接管专项扩大到旧 epoch 模型、工具、checkpoint、终态拒绝且零账单 | 多 Node 实例实际接管证据 |
| C1-D | 部分 | 无 Worker queued 专项；忙 Session 后续消息等待而不耗错误重试次数 | 停止/重启实际 Worker，原消息与 checkpoint 继续 |
| C2-A | 部分 | Node 真实 supervisor/drama/ecommerce，统一 runner/runtime | 同环境画布第四类角色证据 |
| C2-B | 部分 | 修复工作工具白名单过滤；模型不再看见未授权业务工具 | Definition/用户/Scope 全交集、即时撤权矩阵 |
| C2-C | 部分 | 当前运行工具保存在 Execution.Tools；恢复专项 | 提示/技能版本冻结、跨 Session 注入拒绝矩阵 |
| C2-D | 未验收 | Node 原生压缩专项存在且执行 | 两轮真实 Pi 原生压缩、记忆保留与工作账务幂等 |
| C3-A | 部分 | 用户 HTTP 驱动总控 Pi，模型 delegate_work 派工两业务 Pi | agent_session_open/send 作为实际通信入口的完整场景 |
| C3-B | 部分 | 操作回放专项；镜头已提交操作重放十次不覆盖后来编辑 | 同 MessageID 十次跨进程投递，子工作/授权/唤醒计数 |
| C3-C | 部分 | 修复遗留 append_work_input 的跨 root 写入，事务内核对 | owner/root/scope 所有目录、打开、发送、读取负例 |
| C3-D | 部分 | 修复后续输入抢走当前 Pi 写者；报告唤醒链存在 | 用户同 Session 直聊排序与等待期模型数稳定 |
| C4-A | 部分 | HTTP 场景校验订单/charge 数及根 grant；脱敏明细导出 | 两轮压缩、逐笔金额对平与待核对占额 |
| C4-B | 部分 | 实际单次审批；镜头写入/成功回执同事务，失败回滚、已提交重放保留用户编辑 | 实际 Node 断线、任务提交失败及其他业务写入窗口 |
| C4-C | 部分 | retry/generate/approve 先绑定短剧工作 grant；旧 lease 拒绝专项 | 三收费入口报价审批、停止/撤权/改派与 authVersion 的全部入口 |
| C4-D | 部分 | 完成报告在工作事务内检查全部 grant 任务，首任务完成不能提前终结 | 失败替代/重试关系、资源交付与业务采用规则 |

## 已修复的结构边界

- Node server 的 slot 不再含 hostname，避免中文主机名在 Bridge 本地身份校验处失败。使用进程 ID 与槽号，授权身份仍来自 Go Worker registry。
- 通用 Pi 投递在账号锁内拒绝存在非终态运行的同 Session 新运行，后续 AgentTurn 留队等待，保留原 active run 与租约。业务等待不计为失败重试。
- 工具快照在 Go 创建运行时按工作授权过滤；原运行仍读取冻结集合，执行网关继续复核。
- `bindDramaWork` 隔离工作/grant/Scope 校验与制作启动；retry/generate/approve 在唤醒调度之前绑定 grant。
- `drama_update_shot` 在事务中锁定操作记录，镜头变更与成功回执一起提交。外部 Pi 回执丢失时重放已成功操作直接读取结果。
- 工作完成报告在同一工作事务中读取 grant 下所有未成功任务；不会因 operation 列表截断或一个任务完成就提前完成。
- 遗留 supervisor append 工具也在输入写入事务中限制 root，不能成为新 Session 工具边界的旁路。

## C0-B：实际耦合与迁移裁定

| 边界 | 实际位置与当前行为 |
| --- | --- |
| Pi Session 主键 | `model/cloud_agent_pi_session.go`、`repository/cloud_agent_pi_session.go`：owner + conversation；通用 conversation 为 `agent-session:<SessionID>`，CanvasID 为空字符串 |
| 通用 Scope/执行绑定 | `app/agent_pi_execution_state.go`：Execution JSON 冻结 Session/Definition/Scope/消息/Assignment/grant/authVersion；尚未新增显式列 |
| 入队/单写者 | `app/agent_pi_session_run.go`、`repository/agent_compat.go`：同 Session 前轮未终结则留队等待，不切换 active run |
| 输入顺序 | `repository/agent_turn.go` 按 message_seq 选择每 Session 最早输入；`app/agent_runner.go` 只投递通用 Pi |
| Node 调度 | `agent/src/bridge.ts` 使用 user + sessionId，画布兼容路径回退 canvas/run |
| Go admission | `repository/agent_admission.go`：通用按 user + conversation；画布保留 canvas 键 |
| 模型/压缩/工具 | `cloud_agent_pi_*` 内部路由保持原 transport，Execution 分支读取通用运行合同；完整授权重新核对仍待验收 |
| entries/历史 | Pi entries 绑定 Pi Session 存储 ID；公共 `agent_session_messages` 是独立投影；不得把 UI 历史当 Pi checkpoint |
| 预算 | `cloud_agent_task_enqueue.go` 读取 Execution.Binding.BudgetGrantID；媒体由业务绑定传递；压缩逐笔账务仍待验收 |
| 旧 Go 循环 | `agent_business_turn.go` 与旧摘要尚保留；不能宣称已完成所有在途路由迁移 |
| schema/复制 | 当前最高版本 94，补齐模型清单、migration contract、SQLite→PG copier；本次未加表/列，不新增 schema 版本 |

## 历史 R1–R8 复验

| 问题 | 结论 |
| --- | --- |
| R1 | 原短剧 retry 探针修复后通过；generate/approve 已走同一绑定入口，真实派生/审批矩阵仍待补 |
| R2 | 原探针在接手 HEAD 仍失败；事务内 root 限制修复后通过 |
| R3 | 改派探针通过；旧 epoch 增加模型/工具/checkpoint/终态专项；尚缺实际多 Worker 接管 |
| R4 | 原探针确认覆盖用户后来编辑；修复后回执故障使业务回滚，因此旧探针“首次写入必须已提交”的断言不再适用。新 TestAgentDramaShotReceiptAtomicRecovery 检查回滚和成功后的十次回放 |
| R5 | 原两条探针均失败；增加 grant 全任务门槛后均通过。资源/采用与失败替代策略仍未闭合 |
| R6 | 远端 C5 改动已整合；本任务未做浏览器组件验收，不登记通过 |
| R7 | 原复制清单测试失败；补齐三表后复制器/数据库测试通过；真实 PG 复制待 C6 |
| R8 | 旧 Go 摘要探针仍失败；不能以旧循环测试代替两轮 Pi 原生压缩验收 |

## C5/C6 接口与迁移交接

公共路径继续为 `/api/agent/v2`：工作、会话、消息、事件与审批；内部路径 `/internal-agent`。
Session owner/Definition/Application/SpaceType/SpaceID 固定。费用归属取每次运行的 Execution.Binding，不使用目录上的历史 work 字段代替。
snapshot 身份包含 Session、Run、triggerMessage、Pi Session revision/lease、Scope 与 Execution.Binding；Worker registry 凭据与运行 grant 分离。
工具路径仍是冻结披露 → Pi extension → Go callback，Go 不采信模型提供的执行权限。
accepted 表达持久受理；Pi run 完成、媒体完成、资源交付和工作完成须各自表达。

迁移合同当前增加：

| 表 | 关键字段/索引 | 交接要求 |
| --- | --- | --- |
| agent_operations | owner+operation_key 唯一；Session/Work/Assignment epoch、参数指纹、status、approval_id、result_json、operation_ref | 原样保留操作身份和回执，不将 pending 当成功 |
| agent_turn_contracts | owner+turn_key 唯一；Definition 版本、Session、tools_json | 恢复原冻结工具集合 |
| agent_ecommerce_batches | owner+batch_id 唯一；product/preset/task refs | 保留批次与真实任务关系 |
| agent_work_items | authorization_json/version | 授权与内容版本分别保留 |
| agent_approvals | arguments_hash、authorization_version、consumed operation | 仅原操作可消费 |
| drama_productions | agent_work_id、agent_grant_id | 调度派生任务不得丢 grant |

运行、Pi Session 没有新增显式通用身份列。C6 必须以当前 model 和 v92–94 迁移为准，不能照搬 C0 早期“加列”目标或远端旧 19 项清单。
完整迁移前停止入口、备份数据库和资源、固定 schema/二进制、盘点在途运行/订单/操作，按冻结执行路由处理；先做隔离复制验证再切换。失败恢复使用匹配备份与原二进制，禁止删除 pending 回执或复制其他 Session entries 作为恢复。

## 验证与证据边界

本地证据目录为仓库 `.local/cache/c0-c4/`（Git 忽略）。Go 1.27.1、Node 26.1.0、npm 11.13.0、Bun 1.3.9；Pi 0.87.1。Docker Linux daemon 不可用，因此没有 PG/Redis/容器矩阵与真实渠道证据。

- `npm ci --ignore-scripts --no-audit --no-fund`；`npm test`：202 项，201 通过、1 跳过。见 `node-final.log`。
- `go test ./internal/app -run 'TestAgentSessionPi|TestAgentDramaShotReceiptAtomicRecovery|TestAgentRunnerRoutesBusinessTurnToGenericPi|TestSupervisor|TestAgentRootAuthorizationPolicy|TestAgentWholeWorkTreeBilling|TestAgentApprovalExecutionRecovery|TestAgentDirectChatBudgetOwnership|TestAgentAcceptance|TestAgentToolSchemaArtifactMatchesRuntime' -count=1`，见 `app-final.log`；后续单写者改动另见 `ordered-final.log`。
- `fixes-regression.log`：工作工具过滤、镜头回执事务、历史跨根/多任务/伪完成场景通过。
- `lease-final.log`：旧 epoch 四种写入口与零账单专项。
- `migration-tests.log`：复制器、数据库、架构守卫通过；后续架构守卫发现并修复 drama Execute 复杂度增长，未增加 baseline。
- `go-support-final.log`：数据库与复制器通过；repository 全包因既有 Gmail 并发测试未关闭 SQLite，Windows 清理 users.db 失败。该项未冒称通过，也未修改无关账号模块。
- `historical-probes.log` 保留历史反例原始复现，不能当最终修复后结果。
- 真实跨进程命令：设置 `CANVAS_PI_EVIDENCE_DIR` 为隔离证据目录后执行 `go test ./internal/handler -run '^TestAgentAcceptanceHTTPSupervisorChain$' -count=1 -v -timeout 120s`。它启动生产 Node server、真实 Pi SDK、Go 服务/后台任务与确定性 HTTP Provider；不使用 Go 模拟 Pi 驱动。
- `pi-ascii.log` 曾通过完整三角色链路；后续重复运行暴露消息抢写者问题，失败日志保留；单写者修复后的 `pi-ordered.log` 通过（32.71 秒），三 Session、6 runs、29 Pi entries、11 订单/11 charges、1 consumed 审批、5 操作。最终导出扩展验证见 `pi-delivery.log`，不能沿用早期成功声称最终通过。
- `pi-delivery.log` 最终通过（31.20 秒）。`chain-evidence.json` 导出脱敏身份、触发消息、Scope、Assignment/grant/authVersion、操作、审批、订单金额、charge 与 entries 数。最终样本：3 Session、5 Pi runs、27 entries、5 操作、1 consumed 审批；9 笔文本与 1 笔图片订单，各有唯一 charge，实收合计 100,009 microcredits。进度报告与完成报告的到达时机会改变模型轮数，因此不能把不同运行的计数拼在一起。压缩费用、钱包逐笔对平和实际故障恢复仍待补。

本轮没有推送，所以未运行推送专用全门禁。准备推送前仍须按最新 base rebase、更新内部版本与日志、执行项目 verify-before-push，确认 C0–C4 和后续 C5/C6 的剩余项。作者独立审查，无子代理复核。

修复与可复用回归提交为 `543782fee`。历史 R1/R2/R5 的关键探针已迁入 `agent_historical_regression_test.go`，不依赖临时 overlay；`acceptance-final.log` 包含这些场景及完整 app 专项，结果通过（24.645 秒）。`architecture-final.log` 为最终生产代码架构守卫与 DTO 包通过证据。
