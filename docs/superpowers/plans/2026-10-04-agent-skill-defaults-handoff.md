# Agent 技能默认配置实施 — Handoff v2（2026-10-05）

> 本文档取代 `e89abfeb` 版 handoff，是当前唯一的完整交接记录。它包含：分支与提交链、
> **子代理规划与实现审查档案**（每任务的简报/实现/审查结论与裁定）、最终整分支审查结果、
> 未提交工作区改动的处置待决、以及下一步指引。
> 计划：`docs/superpowers/plans/2026-10-04-agent-skill-defaults-capacity.md`
> 设计稿：`docs/superpowers/specs/2026-10-04-agent-workspace-crew-skills-design.md`（含文末"补充需求"节）
> SDD 原始工件已归档进仓库：`docs/superpowers/sdd-archive/2026-10-04-agent-skill-defaults/`
>（6 份任务简报、5 份实现报告、5 份任务审查 diff、scoped-review.md、progress.md 台账全文）。

## 1. 分支快照

- 工作分支：`feat/agent-skill-defaults`（源自 main @ `cd13b79b` canary）。**不要在 main 上干活。**
- HEAD：`a1aa503a`。计划内 14 个任务**全部完成**，最终整分支审查与集中修复已完成、scoped 复审通过。
- 本地 git 身份（repo 级）：`AetherNo2332 <130164460+AetherNo2332@users.noreply.github.com>`。
- 未 push、未开 PR——合并方式是用户决策。注意 AGENTS.md：dev PR 必须 bump 根 `VERSION`（格式 `<大版本>(<7位SHA>)`）。

### 提交链（cd13b79b..HEAD，按阶段分组）

| 阶段 | 提交 | 说明 |
| --- | --- | --- |
| Task 1-5（SDD 子代理实现+逐任务审查） | `8d3a6a7d` `be107a4f` `b4cf3b55` `cd922e46`+`a7b7d859` `6c9c3832` | 模型/v51/kernel → repository CAS → 纯函数 → 管理员 service → 管理员接口 |
| Task 6（native，用户指示切换） | `064101fc` | Run 创建接入会话技能解析 |
| 其他会话插入 | `b5e9fab7` `0f34f254` | 首页皮肤/人物装饰、AGENTS.md 前端约定修订（非本计划） |
| handoff v1 | `e89abfeb` | 阶段性交接（已被本文取代） |
| Task 7 | `77693989` | preset 移除 1-8 校验 |
| canary 同步 | `20689871` `2c200c0a` `ff5438af` `050609c1` `7e62d139` | merge origin/canary 及随行提交 |
| Task 8-12（native） | `f100993a` `8e1d1fc9` `fc50c978` `05651dcf` `4b6c2e06` | 用户端点 → 管理分区 → 移除运行状态 → 选择器去 8 → 子智能体头像列表（含 `web/public/icons/subagents/` 5 枚 SVG + LICENSE） |
| Task 13-14 | `ae6d6f16` | OpenAPI + 五个专题文档 |
| 最终审查修复 | `392426c2` | 8 个 Important 集中修复（冻结消费、事务基线、revision CAS 加固等） |
| 审查记录文档 | `2eba9689` `a1aa503a` | 修复与验证记录、canary 同步与 3000 端口快速验收记录 |

## 2. 执行模式与流程档案

- Tasks 1-5：superpowers Subagent-Driven（每任务独立实现子代理 + 独立审查子代理 + 控制器裁定）。
- Task 6 起：用户指示"你后面换native吧"→ native 模式（控制器内联实现），两次 Task 6 子代理派发均被取消未返回。
- 最终审查：独立 branch_review 代理，只读整分支 `cd13b79b..ae6d6f16`：0 Critical / 8 Important；修复后 scoped 复审 `ae6d6f16..392426c2` 全部 ADDRESSED、无新增问题。
- 环境注意：Windows 检出 CRLF 噪音（`gofmt -l` 对未改动文件误报，只保证入库内容 LF）；本机 cgo 链接器 DLL 问题需 `-ldflags=-linkmode=internal` 跑部分 Go 测试（台账有记录）；`.superpowers/` 是 git-ignored 草稿区，本计划的活跃工作区已按裁定归档至 `.superpowers/archive/2026-10-04-agent-skill-defaults-capacity/`（仅本机磁盘），**入库副本在 `docs/superpowers/sdd-archive/2026-10-04-agent-skill-defaults/`**。

## 3. 规划档案：预检扫描（任务冲突表）

| 任务对 | 共享文件/接口 | 结论与裁定 |
| --- | --- | --- |
| Task 5 / Task 8 | handler/api_test.go、handler/agent.go | 顺序执行，无冲突 |
| Task 4 / Task 8 | app/admin_agent_skill_defaults.go | Task 8 在 Task 4 创建的文件上追加方法，顺序执行 |
| Task 9 / Task 10 | agent-settings-page.tsx/.css、agent-settings-admin.test.ts | 裁定：严格 9→10 顺序（Task 10 文案依赖 Task 9 落地） |
| Task 6 / Task 3+2 | skills.ResolveSkillSelection / repo 方法 | 接口与计划一致，干净 |
| Task 12 / Task 11 | canvas-cloud-agent-panel.tsx | 同一 2117 行文件的不同区域，顺序执行 |
| Task 12 自身 | 测试位置 lib/canvas vs web/test 约定 | 裁定：保留计划原文；后因 src 不含 bun:test 类型被 typecheck 拒绝，最终按裁定移至 `web/test/agent-subagent-layout.test.ts` |

## 4. 子代理实现与审查档案（逐任务）

> 原始全文见 sdd-archive；此处是控制器持有的实现报告要点 + 审查代理结论 + 裁定。

### Task 1：数据模型、迁移 v51 与 kernel 错误码（`8d3a6a7d`）
- **实现子代理**：DONE_WITH_CONCERNS。TDD 红绿完整；插入级唯一性测试捕获真实缺陷——单列主键会让 SQLite 隐式唯一索引拒绝同一会话第二行技能，改为 `(conversation_id, skill_id)` 复合主键（照 CloudAgentPiEntry 先例）。主动上报两个关切：① 两表未注册进 `database.Models()`（超出任务文件清单）；② Windows CRLF gofmt 噪音（验证为误报）。
- **审查**：Spec ✅（字段/索引/v51 checksum 序列/三个 kernel 构造器逐项核对；`backend-database.mdx` 正确留给 Task 13）。命名风险核查：`cmd/server/main.go:68` 启动走 `MigrateSchema` 所以生产启动正常，但 `schema.go:16` 声明 Models() 是"唯一清单"，`cmd/migrate-sqlite-postgres` 的 `verifyMigrationCoverage` 与 `TestMigrationListCoversSchemaModels` 会强制工具清单 ⊆/⊇ Models()，只修一半会让跨库迁移静默丢表（sqlite→postgres 数据丢失）。
- **裁定**：复合主键偏差接受；Models()/迁移工具登记缺口属计划范围缺口 → 指派给 Task 2 派发。Minor 遗留：Source 无约束字符串、低选择性二级索引。

### Task 2：repository 与 revision CAS（`be107a4f`）
- **实现子代理**：DONE。6 个测试全绿；额外完成裁定指派的 `schema.go Models()` 与迁移工具登记。诚实报告 gofmt CRLF 系既有噪音（git stash 验证）。
- **审查**：Spec ✅（五个方法签名/语义逐项核对：CAS 冲突先于 DELETE、`COALESCE(MAX(revision),0)`、upsert DoUpdates 列清单精确、跨 conversationID 隔离）。**Important（plan-mandated）**：事务内 max(revision) 读+写在 PostgreSQL READ COMMITTED 下非原子，并发替换可双双成功（PG 禁止 FOR UPDATE 与聚合同用，简报形态无法用锁读封死；降级为良性的 last-writer-wins）。
- **裁定**：CAS 加固带入 Task 4（service 层互斥）；Minor 遗留：repo 原地修改调用方 rows 切片、排序缺 skill_id tiebreak。

### Task 3：技能解析与容量准入纯函数（`b4cf3b55`）
- **实现子代理**：DONE。8 测试 + 3 子测试；全包回归绿；主动披露偏差：SkillID 先 trim 再去重（防御性）；确认与既有未导出 `skillSourceUser=1`（int）无标识符冲突。
- **审查**：Spec ✅（类型/常量/函数逐字核对；原地覆盖去重保住"低层位置+高层内容"语义；边界 恰好=限额通过、+1 拒绝；details 的 int/int64 类型化断言真实）。Minor 遗留：trim 未写进文档注释、空 ID 测试未断言错误类。
- 裁定：通过，无行动。

### Task 4：管理员 service（`cd922e46` + `a7b7d859`）
- **实现子代理**：DONE（两个提交）。8/8 聚焦测试；整包 `internal/app` 绿（488s）。落实两项控制器裁定：`agentSkillDefaultsMu` 互斥（注释注明单进程 v1、多副本需 DB advisory lock）+ 传参前防御性拷贝 rows。第二提交修 GET 视图 revision 取 max 而非末行值。
- **审查**：Spec ✅（互斥覆盖核实——校验在锁外但不触 revision 状态，repo.Replace 无其他调用点；a7b7d859 修复正确：空表→0 与仓储 CAS 基线一致，GET 出的 revision 永远是合法 expected-revision；容量测试用两个 512KB 技能隔离出 context_bytes 预算命中而非笼统报错）。实现者诚实披露：`skill.Status != 1` 分支实际不可达（repo.Skill 只查 status=1）。Minor 遗留：重复 SkillID 撞唯一索引返回原始 DB 错误、清空保存 revision 口径、Enabled `!=0` 强转、boolToInt 单用 helper。

### Task 5：管理员 HTTP 接口（`6c9c3832`）
- **实现子代理**：DONE。路由断言 + api_test.go wanted 表两条目。披露偏差：`service/aliases_types.go` 加一行别名（facade 规则：handler 不 import internal/app）。
- **审查**：Spec ✅（注册签名精确；bind 失败走 `fail(400)`、service 错误走 `failService` 的分离正确；命名风险核查：handler 包 grep `internal/app` 零结果、`writeAppError` 把 409/400 AppError 以真实 HTTP status + reason + details 透出）。Minor 遗留：两个路由测试近乎重复。

### Task 6：Run 创建接入（`064101fc`，native——无子代理，控制器实现）
- 两次子代理派发被取消后按用户指示转 native。实现：`cloudAgentConversationIDFor`（新会话=runID，续聊=父轮 ConversationID）、`resolveRunSkills`（新会话注入 enabled 默认并全量落库；续聊以既有行为基线、不重读默认、迁移前旧会话不注入；用户显式选择升级为 user 来源）、`freezeRunSkillSnapshots`（全局来源直读 repo 版本校验 ContentHash、不要求安装；用户来源沿用 SkillDetail+IsAdded；`AdmitSkillCapacity` 统一准入）；`cloudAgentSkills` 重构为复用 `cloudAgentUserSkillSnapshot`，原行为与既有直测不变。
- 测试种子三陷阱（后续任务都用到）：① `db.AutoMigrate(切片)` 必须展开 `...`（GORM 把 []any 当单 schema 解析→ReorderModels nil panic）；② AutoMigrate 需含 `CloudAgentEventRecord/CloudAgentMessageRecord`（执行记录有关系字段）与 `User/UserIdentity`（SkillDetail 读作者）；③ 技能夹具必须满足 `validateSkillPackageSnapshot`（skill_packages.go:783）：真实 PackageKey ZIP 落盘 + 逐文件 SHA256/Size/版本 FileCount/TotalBytes/ContentHash 全一致——照 `cloud_agent_skill_selection_test.go` 的 `seedSelectionSkillFor`。
- 本任务的独立审查并入最终整分支审查（见 §6）。

### Task 7-13（native，后续会话接棒）
- Task 7 `77693989`：preset 仅拒空集合（本机链接器 DLL 问题用 `-ldflags=-linkmode=internal` 跑通）。
- Task 8 `f100993a`：`GET /agent/skill-defaults` 用户侧摘要。
- Task 9 `8e1d1fc9`：默认技能分区收进 Agent(beta) 页（`web/test/admin-skill-defaults-section.test.ts` PASS）。
- Task 10 `fc50c978`：移除运行状态分区 + 导航/页面描述改"调度配置、默认技能与记忆管理"。
- Task 11 `05651dcf`：选择器去 8 限制 + 来源展示 + 预算错误透出。
- Task 12 `4b6c2e06`：子智能体头像列表。两条裁定：① Spec/AGENTS 只允许 transform/opacity 动效 → 推翻计划里的 width 过渡与持续脉冲：标签淡入+平移到实测自然宽度、running 用稳定主色环、reduced-motion 时标签显示在行下方且头像不动；② 布局测试从 src/lib 移到 web/test（src 无 bun:test 类型会破坏 typecheck）。头像资产 5 枚 SVG + LICENSE 已入库 `web/public/icons/subagents/`。
- Task 13 `ae6d6f16`：OpenAPI 3 操作 + http-api/backend-database/features/code-map/pending-test 五处文档。

## 5. 最终落地语义（含最终审查修复后状态）

- 后端：`agent_skill_defaults` + `agent_conversation_skills`（v51）；管理员 `GET/PUT /api/admin/agent/skill-defaults`（revision CAS）；用户 `GET /agent/skill-defaults`；Run 创建按会话冻结技能来源；容量准入 `agent_skill_budget_exceeded`（details budget/limit/actual，且修复后 details 在标题与聊天文本中存活）。
- **最终审查后推翻/收严的早期裁定（以 `392426c2` 为准）**：
  - CAS 与清空 revision：原"良性 last-writer-wins / 空表 revision 归零可接受"被推翻——平台实际支持多实例，改为仓储写锁串行化 + system_settings 持久 revision 行（清空不丢 revision、消除 ABA）；快照读保证行与 revision 一致。PostgreSQL 实库联调仍未做。
  - 冻结消费：未安装默认技能走"冻结 global/公开读"、用户技能走"已安装读"，ZIP/版本/hash 全校验；全局技能名称/描述读冻结 SKILL.md（不读漂移的库元数据）；用户已选版本持续钉住（重复 ID 不再触发版本升级——浏览器每轮重发全部 ID，仅凭 ID 重复不构成升级意图；global→user 升级与新增仍允许）。
  - 事务基线：会话技能基线落库并入 holding task/积分/Run 的同一事务，续聊失败不丢基线。
  - UI：既有会话用冻结 Run 来源展示、新会话用完整摘要引用。
- 前端：管理分区（Agent(beta) 页锚点 section）、运行状态分区已移除、选择器无 /8、子智能体头像条（数据源为空、P3 Crew 接入，空态零占位）。

## 6. 最终整分支审查（branch_review，`cd13b79b..ae6d6f16`）

- 结论：0 Critical / 8 Important（Task 6 的独立审查含在内）。8 项：未安装默认技能的 Pi 授权、被拒 Run 的基线写入、冻结全局元数据缺失、用户版本漂移、清空 revision/ABA、跨实例 CAS、预算 details 丢失、默认技能 UI 不准。
- RED 证据在归档：`review-red.log`（6 个后端缺陷复现）、`web-review-red.log`（前端两项）。修复 `392426c2` 后 scoped 复审 `ae6d6f16..392426c2`：8/8 ADDRESSED，无新增 Critical/Important。
- 顺带修复的 Minor：调用方 rows 拷贝、skill_id tiebreak、重复 ID 返回类型化 invalid 错误、默认刷新请求时序。

## 7. 验证现状与合并闸门

- 后端：全量 `go test ./...` PASS（需 `-ldflags=-linkmode=internal` 绕过本机 cgo DLL；`internal/app` 568s）。入库 Go 文件 35 个 LF blob 全部 gofmt 干净。
- 前端：全量 2154 PASS / **11 FAIL**——11 个失败在合并进来的 canary（`20689871`）基线上完全复现，属**继承失败，与本计划无关**；隔离五文件基线 58/58、聚焦 16/16、lint PASS、build PASS。
- **合并闸门为红**：因上述继承的 11 个前端失败（需先修复 canary 上那批失败，属独立工作）。
- 未验证项：PostgreSQL 实库集成、真实浏览器 UI 验收（悬停动画/键盘/读屏）、canary 同步与 3000 端口快速验收记录见 `a1aa503a` 的文档。
- 遗留 Minor（最终审查后仍未修）：Enabled `!=0` 强转与 position 未归一、死代码禁用分支、Source 无约束字符串、低选择性索引、trim 文档注释与空 ID 错误类断言、boolToInt 单用 helper、近似重复路由测试、头像展开在普通 Windows 滚动条/reduced-motion 下可能改变行高（数据源为空，随 P3 一并验证）。

## 8. 未提交的工作区改动（接手第一件事：向用户确认处置）

工作区有 11 个文件的未提交改动（+68/−542），来源是另一会话、**未提交未验证**：删除 `use-agent-launcher-position.ts`、`agent-launcher-position.ts`、`agent-launcher-position.test.mjs`，简化 `use-agent-panel-layout.ts`/`agent-panel-layout.ts`，改动 `canvas-cloud-agent-panel.tsx`/`canvas-cloud-agent-settings.tsx`/`canvas-cloud-agent.css`、`agent-model-picker.test.ts`/`agent-panel-layout.test.ts`、features.mdx 一行。看起来是"移除 agent 启动器定位逻辑"的重构收尾。处置选项：跑 `cd web && bun test web/test/agent-panel-layout.test.ts web/test/agent-model-picker.test.ts && bun run typecheck` 验证后提交，或回滚（`git checkout -- <files>` 需用户确认——这些不是本会话产物）。

## 9. 下一步（按序）

1. 处置 §8 的未提交改动（先问用户：验证后提交 or 丢弃）。
2. 决定分支去向：push + 开 PR（PR 前 bump 根 `VERSION`）或继续本地。合并闸门红的说明随 PR 附上（11 个失败为 canary 继承）。
3. 补验证欠账：PostgreSQL 实库集成冒烟、真实浏览器 UI 验收（子智能体条悬停/键盘/reduced-motion、管理分区、选择器、预算错误文案）。
4. 后续阶段：P2 Workspace、P3-P5 Crew/SSE 各自另立计划（设计稿已含分阶段）；子智能体头像数据源随 P3 接入。
5. 若不再需要本机归档，可删除 `.superpowers/archive/2026-10-04-agent-skill-defaults-capacity/`（入库副本已保证记录不丢）。

## 10. 命令速查

```bash
cd backend && go test -ldflags=-linkmode=internal ./...            # 后端全量（本机需 internal linker）
cd backend && go test ./internal/app/ -run "ResolveRunSkills|CloudAgentConversationIDFor" -count=1
cd web && bun test web/test/admin-skill-defaults-section.test.ts web/test/agent-skill-selector-ui.test.ts web/test/agent-subagent-layout.test.ts web/test/canvas-agent-subagent-list.test.tsx
cd web && bun test && bun run build                                 # 前端全量（11 个继承失败）
git log --oneline cd13b79b..HEAD | cat                              # 分支提交链
git diff --stat                                                     # 未提交改动（§8）
```
