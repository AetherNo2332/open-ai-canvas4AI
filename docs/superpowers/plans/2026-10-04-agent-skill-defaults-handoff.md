# Agent 技能默认配置实施 — 阶段性 Handoff（2026-10-04）

> 本文档供下一个 agent 直接接手。接手前请先完整读完本文，再按"下一步"执行。
> 计划：`docs/superpowers/plans/2026-10-04-agent-skill-defaults-capacity.md`
> 设计稿：`docs/superpowers/specs/2026-10-04-agent-workspace-crew-skills-design.md`（含文末"补充需求"节）

## 分支与提交状态

- 工作分支：`feat/agent-skill-defaults`（基于 main @ `cd13b79b`，即 canary 最新）。**不要在 main 上继续干活。**
- 提交链（全部已提交，工作区干净）：
  - `edb348e6` docs(agent): 技能默认配置 - 登记设计稿与 P0+P1 实施计划（`git add -f` 越过 `docs/*` ignore，与仓库既有 superpowers 文件先例一致）
  - `8d3a6a7d` Task 1：模型 + v51 迁移 + kernel 错误码（SDD 审查通过）
  - `be107a4f` Task 2：repository CAS + Models()/migrate-sqlite-postgres 补登记（SDD 审查通过）
  - `b4cf3b55` Task 3：技能解析与容量准入纯函数（SDD 审查通过）
  - `cd922e46` + `a7b7d859` Task 4：管理员 service（SDD 审查通过）
  - `6c9c3832` Task 5：管理员 HTTP 接口与路由（SDD 审查通过）
  - `064101fc` Task 6：Run 创建接入会话技能解析（native 模式实现，聚焦测试全绿；**未经独立审查**）
- 本地 git 身份已配置（repo 级）：`AetherNo2332 <130164460+AetherNo2332@users.noreply.github.com>`（历史提交作者）。

## 执行模式与流程约定

- 执行方式原为 superpowers Subagent-Driven（每任务独立子代理实现+审查），**用户中途改为 native**（"你后面换native吧"，Task 6 起）。裁定已记录在 `.superpowers/sdd/2026-10-04-agent-skill-defaults-capacity/progress.md`（该目录 git-ignored，是本计划的 SDD 台账：预检冲突表、每任务完成行、裁定、遗留 Minor）。
- 接手后继续 native 模式即可；**最终整分支审查仍是必做步骤**（见"下一步"第 3 步）。
- Task 6 尚欠一次独立任务审查；可选：按 superpowers task-reviewer 模板补一次，或并入最终整分支审查（更省，推荐后者）。

## 已完成内容的要点（接手必读）

### 后端合同（Tasks 1-6 已落地）

- 表：`agent_skill_defaults`（scope+skill_id 唯一，revision CAS 整体替换）、`agent_conversation_skills`（复合主键 conversation_id+skill_id，source=global/user）——迁移 v51，`CurrentSchemaVersion=51`。
- repository：`backend/internal/repository/agent_skill_defaults.go`（`AgentSkillDefaultRows` / `EnabledAgentSkillDefaults` / `ReplaceAgentSkillDefaults` / `AgentConversationSkills` / `SaveAgentConversationSkills` upsert）。
- 纯函数：`backend/internal/skills/skill_selection.go`——`ResolveSkillSelection(layers ...[]SkillSelectionItem)`（参数低→高优先级）、`AdmitSkillCapacity`、常量 `SkillRunMaxFiles=1024 / SkillRunMaxTotalBytes=16MB / SkillRunMaxContextBytes=512KB`（固定运行级上下文预算，是对设计稿"按模型输入预算"的 v1 近似，已在注释注明，P2 再补 per-model）。
- kernel：`AgentSkillBudgetExceeded`（400 reason `agent_skill_budget_exceeded`，details budget/limit/actual）、`AgentSkillDefaultsConflict`（409 reason `agent_skill_defaults_revision_conflict`，details currentRevision）、`AgentSkillDefaultsInvalid`（400 reason `agent_skill_defaults_invalid`）。
- 管理员：`backend/internal/app/admin_agent_skill_defaults.go`（GET/PUT service，RequireAdmin → 逐项校验 → 容量预检 → `agentSkillDefaultsMu` 互斥 + CAS；互斥是单进程 v1 方案，多副本需 DB advisory lock，注释已写）+ `backend/internal/handler/admin_agent_skill_defaults.go`（`GET/PUT /api/admin/agent/skill-defaults`）。
- Run 接入：`backend/internal/app/cloud_agent_skill_selection.go`——`cloudAgentConversationIDFor`（新会话=runID；续聊=父轮 ConversationID）、`resolveRunSkills(userID, conversationID, userSkillIDs, isNewConversation)`（新会话注入 enabled 默认并全量落库；续聊以既有行为基线、只追加/升级用户选择、**不重读默认**——迁移前旧会话无行也不注入）；`CreateCloudAgentRun` :572 改调 `resolveRunSkills`，`newCloudAgentExecution` 增加 conversationID 参数。全局来源技能直读 repo 版本并校验 ContentHash，**不要求用户安装**；用户来源沿用 `SkillDetail`+IsAdded 语义。`cloudAgentSkills`（用户库路径）重构为复用 `cloudAgentUserSkillSnapshot`，原行为不变（既有直测保持绿）。
- 别名：`service/aliases_types.go` 增加 `AgentSkillDefaultItem = app.AgentSkillDefaultItem`（handler 不 import app 的 facade 规则要求）。

### 测试与验证现状

- 后端聚焦测试全绿：`go test ./internal/app/ -run "ResolveRunSkills|CloudAgentConversationIDFor"`（9 个场景）+ 既有 `TestCloudAgentSkillsLoadOnDemand|TestNativeSkill|TestAdminAgentSkill` 回归绿。
- **尚未跑过**：`go test ./...`（全仓）——Task 14 负责。
- 测试种子陷阱（重要）：技能夹具必须满足 `validateSkillPackageSnapshot`（`internal/skills/skill_packages.go:783`）：版本行要有 `PackageKey` 且 ZIP 真实落盘在 `{dataDir}/skill-packages/{skillID}/{versionID}.zip`、每文件行 SHA256=hex(sha256(内容))、Size=len(内容)、版本 FileCount/TotalBytes/ContentHash 与 ZIP 一致。照抄 `cloud_agent_skill_selection_test.go` 的 `seedSelectionSkillFor` 即可。另外 AutoMigrate 传切片必须展开 `...`（GORM 把切片当单个 schema 解析会 nil panic），且包含 `CloudAgentEventRecord/CloudAgentMessageRecord`（CloudAgentExecution 有关系字段）与 `User/UserIdentity`（SkillDetail 读作者）。

## 遗留 Minor（记录在 SDD 台账，最终审查时统一裁决）

1. Task 2：Postgres 下 CAS 读+写非原子（已用进程内互斥加固，多副本需 advisory lock）；`AgentConversationSkills` 排序缺 skill_id tiebreak。
2. Task 4：同一 PUT 内重复 SkillID 会撞唯一索引返回原始 DB 错误而非 `AgentSkillDefaultsInvalid`；清空保存返回 revision=current+1 但下次 GET 读到 0（一次伪冲突）；`Enabled != 0` 会把 2/-1 当启用；`skill.Status != 1` 分支实际不可达（repo.Skill 只查 status=1）。
3. Task 3：`ResolveSkillSelection` 未在注释中写明会 trim 空格；空 ID 测试未断言错误类。
4. Task 5：两个路由测试近乎重复（brief 要求的兄弟风格）。

## 下一步（按序执行）

1. **Task 7**：移除场景 preset 1–8 校验。改 `backend/internal/skills/skills_presets.go:84-86`（抽 `validateSkillPresetSkillCount`，只拒绝空集合）+ `skills_presets_test.go:22-24` 移除 >8 断言、新增 12 个 ID 通过的单测。提交信息：`refactor(skills): 技能默认配置 - 场景预设移除 1-8 数量上限改为准入兜底`。
2. **Task 8**：用户侧只读端点 `GET /agent/skill-defaults`。app 加 `AgentSkillDefaultsForUser`（仅 enabled，返回 {count, skills:[{skillId, skillName}]}），`handler/agent.go` 注册，`api_test.go` wanted 表加条目。提交：`feat(agent): 技能默认配置 - 用户侧默认技能摘要接口`。
3. **Task 9-12（前端，逐个提交）**：
   - Task 9：管理分区收进 `/admin/settings/agent` 页（锚点 section `#skill-defaults`，新组件 `web/src/pages/admin/settings/components/agent-skill-defaults-section.tsx` + `web/src/services/api/admin-skill-defaults.ts`；照 `agent-lessons-panel.tsx` 的手写 state+reload 模式）。提交：`feat(web): 技能默认配置 - Agent 设置页收纳默认技能分区`。
   - Task 10：移除运行状态分区（删除 `agent-runtime-status.tsx`、`web/test/agent-runtime-status.test.tsx`、`agent-settings-page.tsx` 的轮询与 nav/section、`agent-settings-page.css` 的 `.agent-runtime-*`、`admin-agent-settings.ts` 的 `listAgentSchedulerStatus`/`AgentRuntimeStatus`；`agent-settings-admin.test.ts:22` 反向断言；`admin-shell.tsx:97` 与页面 description 文案改为"调度配置、默认技能与记忆管理"/"统一管理 Agent 调度、默认技能与用户记忆"）。提交：`refactor(web): Agent 设置页 - 移除运行状态分区并更新导航描述`。
   - Task 11：技能选择器去 8 限制（`canvas-agent-skill-library-modal.tsx` :167-169/:180/:205/:276/:284；panel 传 `globalDefaultSkillIds`；`services/api/agent.ts` 加 `listAgentSkillDefaults`；`web/src/lib/canvas/agent-error-presentation.ts:23` 对 reason `agent_skill_budget_exceeded` 透出后端 msg；`skill-runtime-picker.tsx` 的 maxSkills:4 属另一运行时合同**不动**）。提交：`feat(web): 技能默认配置 - 技能选择器取消 8 个上限并展示默认技能来源`。
   - Task 12：活动子智能体头像列表（`web/src/components/canvas/canvas-agent-subagent-list.tsx` + css；lib 纯函数 `computeSubagentOffsets`；面板 `:1040` 附近 PlanBar 模式插入独立 flex 行；`items.length===0` 返回 null 零占位——当前无子智能体数据源，P3 Crew 才接入；动效只用既有 token `--motion-ease-out/--motion-dur-*`，reduced-motion 直接显隐；头像 SVG 放 `web/public/icons/subagents/`）。提交：`feat(web): Agent 面板 - 活动子智能体圆形头像列表与悬停让位展开`。
   - 前端测试模式：bun test；组件用 `renderToStaticMarkup` 断言 class/aria，或源码文本回归（`web/test/admin-ui-regressions.test.ts` 风格）；lint 只查 antd Empty 与静态 Modal.confirm。
4. **Task 13**：文档同步（`backend/internal/handler/openapi.yaml` 加 3 条路径；`docs/content/docs/backend/http-api.mdx`、`backend-database.mdx`（v51 段+两表）、`overview/features.mdx`、`backend/code-map.mdx`、`progress/pending-test.mdx` 顶部登记验收项）。提交：`docs(agent): 技能默认配置 - 同步默认技能接口、数据表与待测试验收项`。
5. **Task 14 + 最终审查**：`cd backend && gofmt -l .`（Windows 检出会有 CRLF 噪音：未改动文件的 CR 报告可忽略，只保证新改文件入库内容为 LF）+ `go test ./...`；`cd web && bun test && bun run build`。然后做**最终整分支审查**（superpowers requesting-code-review 的 code-reviewer 模板，审查范围 `git diff cd13b79b..HEAD`，指向 SDD 台账的 Minor 列表让它裁决哪些必须合并前修）。审查发现的修复：一次集中修复派发 + 一次 scoped re-review。
6. **收尾**：把 SDD 台账里所有 `Ruling:` 行汇总进交付消息（"Rulings I made"）；按 finishing-a-development-branch 流程问用户合并方式。**不要自行 push / 开 PR / 合并**——那是用户决策。
7. 删除 SDD 工作区 `.superpowers/sdd/2026-10-04-agent-skill-defaults-capacity/`（git-ignored 草稿，git 历史即档案）。

## 命令速查

```bash
cd backend && go test ./internal/app/ -run "ResolveRunSkills|CloudAgentConversationIDFor" -count=1   # Task 6 聚焦
cd backend && go test ./...                                     # 全仓（Task 14）
cd web && bun test web/test/admin-skill-defaults-section.test.ts # Task 9 示例
cd web && bun test && bun run build                              # 前端全量（Task 14）
git log --oneline cd13b79b..HEAD                                 # 分支提交链
```
