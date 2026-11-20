# Agent 分业务工具目录与准入预算验收

基线为 4AI 分镜 PR #138 的 `codex/storyboard-current-agent`，HEAD `708cc4795d7b37997b708a10c33f5c6e794ce962`。源工作树与隔离工作树初始 `git status --short` 均为空。本次改动使用独立 PR，无数据库迁移。

## T1 · 准入缺口

`cloud_agent_run_create.go` 先通过定义和对象权限冻结输入，再在 `buildCloudAgentRunPrompt` 完成工具与模型消息装配，最后由 `admitCloudAgentRun` 创建任务、Run 和预留。基线的上下文检查在无画布规则和技能时提前返回。64,000 / 12,000 字节上限只在工具测试中检查，测试配置未包含所有可开启能力。基线红测见 `.local/capability-budget/t1-red.log`，4,096 窗口的用例意外创建了 Run。全开画布目录为 33 工具、67,458 字节、约 21,570 Token。

## T2–T3 · 注册、预算和冻结

`agent_definition_registry.go`、`agent_tool_registry.go` 和 `agent_tool_catalog.go` 将平台可支持的工具、定义声明、执行器版本与准入条件分开。`canvas.default` 声明画布读写、任务查询、生成和依条件开放的搜索、识图、分镜、子代理。`drama.analysis` v2 只声明 `task_get` 与 `drama:episode` 的统一 `read` 适配器，另有 `plan_update`、`ask_user` 和 `finish_run`。显式空业务工具集默认只有 `plan_update` 与 `finish_run`。Skill 文件 `read` 依本轮的 Skill 配置授权。第三业务 fixture 注册自己的 `library_lookup` Schema 与执行器。

| 目录与条件 | 工具数 | 紧凑 JSON 字节 | 估算 Token |
| --- | ---: | ---: | ---: |
| 离线合同全集 | 36 | 63,677 | 19,655 |
| 画布自动，启用画布、视觉、搜索及父级子代理 | 33 | 61,719 | 19,035 |
| 画布只读，文本模型，搜索及子代理关闭 | 13 | 12,350 | 4,169 |
| 短剧默认 v2，只读 | 5 | 3,553 | 1,261 |
| 显式零业务工具且无交互、读取适配器、Skill | 2 | 1,561 | 561 |

共同写入说明移至系统约束，画布字段说明由既有 `canvas_list_node_types` 按需给出。只读分镜仍保留 `read` 的筛选和分页校验。`cloud_agent_capability_budget.go` 以 64,000 / 12,000 字节执行上限。Token 沿用 Go 估算，扣除模型输出预留和协议余量后，对工具、初始消息、策略及 Skill 索引求和。供应商实际分词及后续压缩由既有模型链路处理。

错误携带 `reason` 和诊断 ID。`agent_tool_schema_budget_exceeded` 提供 `tool_bytes` 或 `directory_bytes`、实际值和阈值，单工具超限带工具名。`agent_context_budget_exceeded` 另有 `toolTokens`。`agent_model_tools_unsupported` 用于明确标记无需接受工具的模型线路。前三类拒绝不写入 Run、占位任务或积分预留。

## T4 · 执行与 Worker 恢复

`cloud_agent_capability_snapshot.go` 在领取和现行授权检查中核对目录版本、hash、定义、执行器与读取适配器版本。`cloud_agent_pi_model_step.go` 校验 Worker 上送的模型工具与冻结 Schema 一致。`cloud_agent_tool_preflight.go` 及执行前再校验披露、当前权限和参数。`agent/src/capability-directory.ts` 校验在握手后收到的目录和续接快照。收紧权限即时拒绝失权调用，后续模型步撤下相关工具。放宽只能恢复之前已获准的工具。

Go 与 Pi 配套使用 wire v7。缺少可验证目录的 v6 注册式在途 Run 应于升级前排空。领取不兼容目录返回 `agent_capability_directory_unavailable`，占位预留随失败退还，历史可查。对于早期无业务定义的旧记录，v7 显式标识 transport 版本 0 并按旧检查点原样读取目录。

## T5 · 自动化与待验

本地日志根为 `.local/capability-budget/`。Linux 推送前门禁报告位于验证工作树的 `.local/cache/verify/`。

| 检查 | 证据 |
| --- | --- |
| 基线及注册红测 | `t1-red.log` 复现实际准入绕过，`t2-red.log` 复现短剧注册画布写工具 |
| Go 注册、预算、权限、恢复及架构 | 专项 `go test ./internal/app ./internal/architecture -run '^(TestAgent(Capability\|DefinitionRegistry\|ToolAdmission\|ToolSchemaArtifactMatchesRuntime)\|TestCloudAgentPermission\|TestWorkspaceArchitecture)' -count=1 -v` 已通过。媒体分辨率、批量审批、退役工具、压缩续聊回归见 `t5-final-focused.log`。空业务工具集准入见 `t5-zero-tools.log` |
| Go→JSON、Node Harness 与 wire | `TestAgentToolSchemaArtifactMatchesRuntime` 由 Quality 的 `backend:tool-contract` 执行。Windows `npm test` 为 203 通过、1 项平台跳过，见 `t5-node.log` |
| 验证计划 | `node --test scripts/verification-plan.test.mjs scripts/verification-execution.test.mjs web/scripts/run-tests.test.mjs`，26 项通过，见 `t5-plan-tests.log` |
| HTTP/Go/Pi | `go test ./internal/handler -run '^TestAgentSDKRealHTTPWorkerRestart$' -count=1 -v`。隔离 PostgreSQL schema 下两项通过，覆盖两类业务、两名用户、16 个隔离会话、38 次确定性模型调用与账单，以及 Worker 接管。见 `t5-http-postgres.log`。首次 SQLite 并发运行遭写锁冲突，见 `t5-http-worker.log` |
| 文档与推送前门禁 | Windows 文档类型检查通过，Windows Next 构建无法解析工作树外的依赖目录。Linux 构建与完整 affected 检查使用 `bun scripts/verify-before-push.mjs --base 708cc4795d7b37997b708a10c33f5c6e794ce962 --mode affected`，各检查输出见验证工作树 `.local/cache/verify/` |

真实页面手工验收与真实供应商模型待验。本次未部署，未调用付费供应商。
