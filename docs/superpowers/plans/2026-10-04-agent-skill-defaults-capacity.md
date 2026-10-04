# Agent 技能默认配置、容量准入与面板子智能体列表（P0+P1）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 管理员可配置全局默认技能并注入每个新建 Agent 会话；用户、会话可在默认之上继续追加技能，取消前端与场景 preset 的固定 8 个数量上限，改为按文件数、总字节与上下文预算做容量准入；管理入口收纳进管理员 Agent（beta）设置页（并移除其"运行状态"分区）；画布 Agent 面板新增活动子智能体头像列表。

**Architecture:** 新增 `agent_skill_defaults` 与 `agent_conversation_skills` 两张表（迁移 v51）；技能集合在 Run 创建时由"全局默认 + 会话既有 + 本轮用户追加"三层按 `skillId` 去重解析（用户优先），经容量准入后冻结快照；会话技能来源与版本持久化到 `agent_conversation_skills`，管理员后续修改默认不影响已创建会话。管理员通过 `PUT /api/admin/agent/skill-defaults` 以 revision CAS 保存；前端不新增一级导航，管理分区收纳进 `/admin/settings/agent`（Agent（beta）页）并移除该页"运行状态"分区；技能选择器取消 8 个上限并展示默认来源；画布 Agent 面板新增数据驱动的"活动子智能体头像列表"（悬停展开名称、右侧头像让位平移的非线性微交互，空态零占位）。

**Tech Stack:** Go 1.25、Gin、GORM/SQLite、React 19、TypeScript、Ant Design、Bun test。

**Spec:** docs/superpowers/specs/2026-10-04-agent-workspace-crew-skills-design.md（本计划实现其 P0 与 P1 两节及文末"补充需求"A/B 两项；P2 Workspace、P3–P4 Crew、P5 观测灰度按范围检查结论另立计划）

## Global Constraints

- 不设固定技能数量上限；取消的仅是"最多 8 个"，既有预算全部保留：单文件 8MB、整包 20MB、单包 512 文件（`backend/internal/skills/skill_packages.go:36-42`）、积分/生成任务/视频秒数/Agent step 预算不变。
- 任何超限返回机器可读 reason 与 details（预算名、上限、实际值），不静默截断技能内容。
- 全局默认技能不能引用用户私有技能（`IsPrivate=true` 拒绝）；全局默认始终注入新会话，用户显式选择只能追加、不能移除默认技能。
- 管理员修改默认只影响新会话；已创建会话与已运行任务的技能快照、hash、prompt cache identity 不漂移。
- 管理员权限在 service 层首行校验（`RequireAdmin`，`backend/internal/app/admin.go:140`）；`internal/skills`、`internal/repository` 不得 import `internal/service`/`internal/app`。
- handler 只做入参解析与统一响应（`ok`/`failService`，`backend/internal/handler/response.go`）；失败时 HTTP status 与业务 code 表达真实失败。
- 提交说明格式 `<type>(<scope>): <业务模块> - <变更摘要>`；数据库表变化同步 `docs/content/docs/backend/backend-database.mdx`。
- 验证命令：后端 `cd backend && go test ./...`（CI 另查 gofmt）；前端 `cd web && bun test`、`bun run build`；UI 退场规则 `bun run lint`。

## Review Focus

- 管理员保存引用已删除/禁用技能或失效版本的默认集 → 保存失败并指明技能 ID，不静默剔除（Task 4 测试钉住）。
- 管理员修改默认后，旧会话的后续轮次技能集与快照保持不变（Task 6 测试钉住）。
- 用户技能库未安装某全局默认技能 → 该 Run 仍正常装配该技能，不要求 `IsAdded`（Task 6 测试钉住）。
- 技能集合逼近预算边界 → 恰好在限额上可通过，超 1 字节/1 文件即拒绝且 details 可读（Task 3、Task 6 测试钉住）。
- 双管理员并发保存 → 后者收到 revision 冲突（含当前 revision），先保存者结果不丢（Task 2、Task 4 测试钉住）。

---

### Task 1: 数据模型、迁移 v51 与 kernel 错误码

**Files:**
- Create: `backend/internal/model/agent_skills_config.go`
- Create: `backend/internal/database/agent_skills_config_test.go`
- Modify: `backend/internal/database/migrations.go`（`schemaMigrations` 追加 v51，`CurrentSchemaVersion` 50→51，新增 checksum 常量）
- Modify: `backend/internal/kernel/error_codes.go`、`backend/internal/kernel/errors.go`

**Interfaces:**
- Produces: `model.AgentSkillDefault{ID, Scope("global"), SkillID, SkillVersionID, Position, Enabled, Revision int64, UpdatedBy, CreatedAt, UpdatedAt}`，唯一索引 `(scope, skill_id)`；`model.AgentConversationSkill{ConversationID, SkillID, SkillVersionID, ContentHash, Source, Position, CreatedAt}`，唯一索引 `(conversation_id, skill_id)`；`Source` 取值 `global`/`user`。
- Produces: `kernel.AgentSkillBudgetExceeded(details map[string]any) *AppError`（status 400，reason `agent_skill_budget_exceeded`）；`kernel.AgentSkillDefaultsConflict(currentRevision int64) *AppError`（status 409，reason `agent_skill_defaults_revision_conflict`，details 含 `currentRevision`）；`kernel.AgentSkillDefaultsInvalid(message string, details map[string]any) *AppError`（status 400，reason `agent_skill_defaults_invalid`）。reason 常量加入 `error_codes.go`。

- [ ] **Step 1: 写失败测试** — `database/agent_skills_config_test.go`：`gorm.Open(sqlite)` + `database.MigrateSchema(db)` 后断言 `agent_skill_defaults`、`agent_conversation_skills` 两表存在且 `RequireSchemaVersion` 接受 51（照 `database/cloud_agent_recovery_test.go` 的全量迁移写法）。kernel 侧断言三个构造器的 Status/Code/Reason/Details 字段。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/database/ ./internal/kernel/`，期望编译失败/表不存在。
- [ ] **Step 3: 实现** — 按上面 Interfaces 建 model；v51 迁移用 `tx.AutoMigrate(&model.AgentSkillDefault{}, &model.AgentConversationSkill{})`（照 v42 写法，`migrations.go:151`），checksum 常量按 `migrations.go:15-35` 命名惯例；kernel 构造器照 `kernel/errors.go` 既有 `QuotaExceeded` 风格。
- [ ] **Step 4: 运行确认通过** — 同 Step 2 命令，期望 PASS。
- [ ] **Step 5: Commit** — `feat(agent): 技能默认配置 - 新增默认技能与会话技能模型、v51 迁移和预算错误码`

### Task 2: 默认技能与会话技能 repository（含 revision CAS）

**Files:**
- Create: `backend/internal/repository/agent_skill_defaults.go`
- Create: `backend/internal/repository/agent_skill_defaults_test.go`

**Interfaces:**
- Consumes: Task 1 的两个 model 与 `kernel.AgentSkillDefaultsConflict`。
- Produces（receiver 均为 `*Repository`）:
  - `AgentSkillDefaultRows() ([]model.AgentSkillDefault, error)`（全部，按 `position, skill_id` 排序）
  - `EnabledAgentSkillDefaults() ([]model.AgentSkillDefault, error)`（仅 enabled）
  - `ReplaceAgentSkillDefaults(expectedRevision int64, rows []model.AgentSkillDefault, updatedBy string) (int64, error)` — 事务内：当前 revision = 现有行 max(revision)，空表为 0；`current != expectedRevision` 时返回 `kernel.AgentSkillDefaultsConflict(current)`；否则清空 scope=global 行，插入新行（Revision=current+1，带 UpdatedBy），返回新 revision。
  - `AgentConversationSkills(conversationID string) ([]model.AgentConversationSkill, error)`（按 position 排序）
  - `SaveAgentConversationSkills(rows []model.AgentConversationSkill) error` — 按 `(conversation_id, skill_id)` upsert，更新 `skill_version_id/content_hash/source/position`。

- [ ] **Step 1: 写失败测试** — sqlite 内存库 `AutoMigrate` 两表（照 `repository/skills_test.go:15` 写法）。用例：CAS 0→1→2 成功链；expected=1 而 current=2 时报错且 `errors.As` 取到 details `currentRevision=2`；replace 清掉旧行；空表 expected=0 可保存；排序稳定；会话技能 upsert 覆盖版本与来源、不产生重复行；查他人 conversationID 返回空（跨账号隔离）。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/repository/ -run AgentSkill`，期望编译失败。
- [ ] **Step 3: 实现** — 事务用 `r.db.Transaction(func(tx *gorm.DB) error)`；upsert 用 `clause.OnConflict{Columns: ..., DoUpdates: ...}`（照 `repository/skills.go:178` SetUserSkillAdded 写法）。
- [ ] **Step 4: 运行确认通过** — 同 Step 2。
- [ ] **Step 5: Commit** — `feat(agent): 技能默认配置 - 默认技能与会话技能仓储及 revision CAS`

### Task 3: 技能选择解析与容量准入（skills 域纯函数）

**Files:**
- Create: `backend/internal/skills/skill_selection.go`
- Create: `backend/internal/skills/skill_selection_test.go`

**Interfaces:**
- Consumes: `kernel.AgentSkillBudgetExceeded`。
- Produces:
  - `type SkillSelectionItem struct { SkillID, VersionID, ContentHash, Source string }`；常量 `SkillSourceGlobal = "global"`、`SkillSourceUser = "user"`。
  - `func ResolveSkillSelection(layers ...[]SkillSelectionItem) ([]SkillSelectionItem, error)` — 参数按优先级从低到高传入（global → 后续层 → user）；同一 `skillId` 保留最高优先层条目；输出顺序 = 最低优先层（global）的原始顺序在前、各高层新增条目按出现顺序在后；空 ID 报错。
  - `type SkillCapacityFacts struct { SkillID string; FileCount int; TotalBytes int64; ContextBytes int64 }`
  - `type SkillCapacityBudgets struct { MaxFiles int; MaxTotalBytes, MaxContextBytes int64 }`
  - 常量 `SkillRunMaxFiles = 1024`、`SkillRunMaxTotalBytes = 16 << 20`、`SkillRunMaxContextBytes = 512 << 10`（选择理由：单包上限 512 文件/20MB 的整数倍余量；上下文按 `piNativeSkillReadMaxRunes=12000` rune ≈ 48KB/技能 × 约 10 技能取整）。
  - 对 spec 的显式近似：设计要求"按当前模型的可用输入预算检查"，v1 以固定运行级上下文预算（`SkillRunMaxContextBytes`）替代按模型窗口的动态检查，per-model 预算随 Workspace（P2）计划补齐；此近似已在 `SkillRunMaxContextBytes` 注释中注明。
  - `func AdmitSkillCapacity(facts []SkillCapacityFacts, budgets SkillCapacityBudgets) error` — 超限返回 `kernel.AgentSkillBudgetExceeded(details)`，details 含 `budget`("files"/"total_bytes"/"context_bytes")、`limit`、`actual`；恰好等于限额通过。

- [ ] **Step 1: 写失败测试** — 用例：三层去重且 user 版本覆盖 global 版本；输出顺序稳定（global 在前、用户新增在后）；空 ID 报错；三项预算各自恰好通过/超 1 拒绝；details 的 budget/limit/actual 值正确；空集合通过。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/skills/ -run Skill`，期望编译失败。
- [ ] **Step 3: 实现** — 单遍 map 去重 + 有序切片；不引入新依赖。
- [ ] **Step 4: 运行确认通过** — 同 Step 2。
- [ ] **Step 5: Commit** — `feat(skills): 技能默认配置 - 多层技能去重解析与容量准入纯函数`

### Task 4: 管理员默认技能 service（校验、容量预检、CAS 保存）

**Files:**
- Create: `backend/internal/app/admin_agent_skill_defaults.go`
- Create: `backend/internal/app/admin_agent_skill_defaults_test.go`

**Interfaces:**
- Consumes: Task 2 repository 方法、Task 3 纯函数、`s.RequireAdmin`（`app/admin.go:140`）、`repository.CreateSkillWithPackage`（造测试数据）。
- Produces:
  - `type AgentSkillDefaultItem struct { SkillID, SkillVersionID string; Position, Enabled int }`（Enabled 用 0/1 便于 JSON；GET/PUT 同构）
  - `type AgentSkillDefaultsViewItem struct { SkillID, SkillName, SkillVersionID, VersionLabel string; Position, Enabled, Status, FileCount int; TotalBytes int64 }`
  - `type AgentSkillDefaultsView struct { Revision int64; Items []AgentSkillDefaultsViewItem; TotalFiles int; TotalBytes, ContextEstimateBytes int64 }`（totals 只统计 enabled 行；ContextEstimateBytes = Σ enabled 技能的 `SkillVersion.TotalBytes`，作为保守上限口径并在字段注释注明）
  - `func (s *Service) AdminAgentSkillDefaults(actor *model.User) (*AgentSkillDefaultsView, error)`
  - `func (s *Service) ReplaceAgentSkillDefaults(actor *model.User, revision int64, items []AgentSkillDefaultItem) (int64, error)` — 校验顺序：RequireAdmin → 每个 item：`repo.Skill(skillID)` 存在且 Status=1、`repo.SkillVersion(versionID).SkillID == skillID`、`!skill.IsPrivate`（违规即 `kernel.AgentSkillDefaultsInvalid`，message 含技能 ID）→ enabled 集合经 `skills.AdmitSkillCapacity` 预检 → `repo.ReplaceAgentSkillDefaults`。

- [ ] **Step 1: 写失败测试** — `service.New(repository.New(db), t.TempDir())` + `repo.CreateSkillWithPackage` 造 3 个技能（其中一个 `IsPrivate`、一个禁用版本）。用例：非管理员 Forbidden；私有技能被拒且 message 含该技能 ID；versionID 不属于该技能被拒；enabled 总量超 `SkillRunMaxContextBytes` 被拒（details.budget="context_bytes"）；expected revision 过期返回冲突；合法保存返回新 revision，GET 回读顺序/总数/预估一致；禁用技能（Status≠1）引用被拒（Review Focus 第 1 条）。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/app/ -run AdminAgentSkill`，期望编译失败。
- [ ] **Step 3: 实现** — 文件内只做编排，不放 SQL；`Status` 常量沿用 `model` 现有技能状态约定（1=启用）。
- [ ] **Step 4: 运行确认通过** — 同 Step 2。
- [ ] **Step 5: Commit** — `feat(agent): 技能默认配置 - 管理员默认技能服务与容量预检`

### Task 5: 管理员 HTTP 接口与路由注册

**Files:**
- Create: `backend/internal/handler/admin_agent_skill_defaults.go`
- Create: `backend/internal/handler/admin_agent_skill_defaults_test.go`
- Modify: `backend/internal/handler/api.go`（在 `RegisterAdminRoutes` 等注册处旁追加 `RegisterAdminAgentSkillDefaultsRoutes`）
- Modify: `backend/internal/handler/api_test.go`（`wanted` 表追加两条）

**Interfaces:**
- Consumes: Task 4 的两个 service 方法。
- Produces: `GET /api/admin/agent/skill-defaults` → `ok(c, AgentSkillDefaultsView)`；`PUT /api/admin/agent/skill-defaults`，body `{ revision int64, items []AgentSkillDefaultItem }`，成功 `ok(c, { revision })`，失败 `failService`（409/400 + reason + details）。注册函数签名照 `handler/admin_analytics.go:13`：`func RegisterAdminAgentSkillDefaultsRoutes(r *gin.RouterGroup, svc *service.Service)`。

- [ ] **Step 1: 写失败测试** — 照 `handler/admin_observability_test.go`：注册后断言 `GET|PUT /api/admin/agent/skill-defaults` 两条路由存在；`api_test.go` 的 `wanted` map 追加 `"GET /api/admin/agent/skill-defaults": false`、`"PUT /api/admin/agent/skill-defaults": false`。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/handler/ -run "AdminAgentSkill|RegisterCanvasAPI"`。
- [ ] **Step 3: 实现** — handler 内 `currentUser` + 直接调 service，无业务判断；PUT 用 `c.ShouldBindJSON`。
- [ ] **Step 4: 运行确认通过** — 同 Step 2。
- [ ] **Step 5: Commit** — `feat(agent): 技能默认配置 - 管理员默认技能查询与保存接口`

### Task 6: Run 创建接入会话技能解析（全局注入、来源冻结、容量拦截）

**Files:**
- Create: `backend/internal/app/cloud_agent_skill_selection.go`
- Create: `backend/internal/app/cloud_agent_skill_selection_test.go`
- Modify: `backend/internal/app/cloud_agent.go`（:572 调用点替换；新增会话 ID 提前解析；`newCloudAgentExecution` 签名加 conversationID 参数）
- Modify: `backend/internal/app/cloud_agent_tools.go`（从 `cloudAgentSkills` :310-358 抽出单技能冻结 helper，供两种来源复用）

**Interfaces:**
- Consumes: Task 2/3 全部产出、现有 `s.SkillDetail`（用户技能语义）、`repo.Skill`/`repo.SkillVersion`/`repo.SkillFiles`（全局来源直读，不要求 `IsAdded`）。
- Produces:
  - `func (s *Service) cloudAgentConversationIDFor(userID, runID, parentID string) string` — 语义与 `newCloudAgentExecution` :723-730 完全一致（新会话=runID；续聊=父轮 ConversationID，父轮查不到回退 parentID）。
  - `func (s *Service) resolveRunSkills(userID, conversationID string, userSkillIDs []string, isNewConversation bool) ([]cloudAgentSkill, error)` — 新会话判定用 `parentID == ""`（而非"会话表无行"），保证迁移前的旧会话续聊不会被误注入默认技能（spec 迁移兼容第 2 条）。流程：
    - 新会话（`isNewConversation=true`）：`repo.EnabledAgentSkillDefaults()` 为 global 层、请求 skillIds 为 user 层，`skills.ResolveSkillSelection` 解析后全量 `SaveAgentConversationSkills` 落库。
    - 既有会话：以既有行为基线（不重读全局默认，管理员修改不影响既有会话）；无行的迁移前旧会话基线为空，行为与现状一致，仅落本轮 user 行；本轮新增 skillId 追加 user 行；某技能既有行非 user 来源而本轮被用户显式选择 → 该行升级为 user 来源与其当前已装版本（用户只能追加，不改其他技能的注入）。
    - 两种路径最终都逐技能冻结快照（复用抽出的 helper），并以 `skills.AdmitSkillCapacity`（Task 3 常量）准入，失败即 400 `agent_skill_budget_exceeded`。
  - `CreateCloudAgentRun` 中 :572 改为 `conversationID := s.cloudAgentConversationIDFor(userID, id, parentID)` + `s.resolveRunSkills(userID, conversationID, req.SkillIDs, parentID == "")`；`newCloudAgentExecution` 改为接收该 conversationID 并删除内部重复查询（行为不变）。

- [ ] **Step 1: 写失败测试** — 用 `repo.CreateSkillWithPackage` 造库（含用户未安装的技能、私有技能）。核心用例：① 管理员配 20 个默认技能后 `CreateCloudAgentRun`（无 skillIds）→ run 快照含 20 个技能，会话表 20 行 source=global（设计验收"20 个默认技能完整继承"）；② 同会话续轮（parentID 指向首轮）期间管理员替换默认 → 第二轮技能集与首轮一致（Review Focus 第 2 条）；③ 续轮带 skillIds=[X] → X 以 user 来源追加、默认技能仍在；④ 用户选择某默认技能的已装版本 → 该行升级为 user 版本；⑤ 默认技能未安装仍成功（Review Focus 第 3 条）；⑥ 默认引用禁用技能 → 创建失败且 message 含技能 ID；⑦ 20 个技能把 ContextBytes 推过 `SkillRunMaxContextBytes` → 400 reason `agent_skill_budget_exceeded`（Review Focus 第 4 条的超限语义）；⑧ user B 的会话查不到 user A 的会话技能行；⑨ 迁移前旧会话（有父轮、会话技能表无行）续聊不注入默认技能，技能集仅含本轮 user 选择（spec 迁移兼容第 2 条）。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/app/ -run ResolveRunSkills`，期望编译失败。
- [ ] **Step 3: 实现** — 抽 helper 时保持 `cloudAgentSkills` 原行为（现有直测 `cloud_agent_context_test.go:175`、`cloud_agent_native_skill_test.go:27` 不改仍须绿）；全局来源冻结走 `repo.Skill` + `repo.SkillVersion` + `repo.SkillFiles` 并校验 ContentHash 一致（照 :326 既有 hash 校验语义）。
- [ ] **Step 4: 运行确认通过** — `go test ./internal/app/`（整包，含既有 Agent 用例回归）。
- [ ] **Step 5: Commit** — `feat(agent): 技能默认配置 - Run 创建注入全局默认技能并按会话冻结来源`

### Task 7: 移除场景 preset 的 1–8 数量校验

**Files:**
- Modify: `backend/internal/skills/skills_presets.go`（:84-86 改为仅拒绝空集合）
- Modify: `backend/internal/skills/skills_presets_test.go`（:22-24 移除 >8 断言，新增直接单测）

**Interfaces:**
- Produces: `func validateSkillPresetSkillCount(presetID string, skillIDs []string) error` — 仅当 `len(skillIDs) == 0` 报错；数量上限交给 Run 创建时的 `AdmitSkillCapacity`（preset 技能每轮仍经 Run 准入，满足设计"preset 内容仍需经过容量准入"）。

- [ ] **Step 1: 写失败测试** — 单测：12 个合法 ID 通过；0 个报错且文案含 presetID；保留既有"必须引用种子技能/去重"断言。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/skills/ -run Preset`。
- [ ] **Step 3: 实现** — `SkillPresets()` 循环内改为调用该函数；`skills_presets_test.go` 删除 >8 Fatal。
- [ ] **Step 4: 运行确认通过** — 同 Step 2。
- [ ] **Step 5: Commit** — `refactor(skills): 技能默认配置 - 场景预设移除 1-8 数量上限改为准入兜底`

### Task 8: 用户侧默认技能只读端点

**Files:**
- Modify: `backend/internal/app/admin_agent_skill_defaults.go`（追加用户侧读方法）
- Modify: `backend/internal/handler/agent.go`（注册 `GET /agent/skill-defaults`）
- Modify: `backend/internal/handler/api_test.go`（wanted 追加 `"GET /api/agent/skill-defaults": false`）

**Interfaces:**
- Produces: `func (s *Service) AgentSkillDefaultsForUser(userID string) (*AgentSkillDefaultsSummary, error)`；`type AgentSkillDefaultsSummary struct { Count int; Skills []struct{ SkillID, SkillName string } }`（仅 enabled，按 position）。handler 照 `agent.go:118` 现有闭包风格，登录即可（非管理员）。

- [ ] **Step 1: 写失败测试** — app 测试：enabled 行数=Count 且顺序一致、disabled 不计入；handler 路由注册断言并入 Task 5 的测试文件风格。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/app/ ./internal/handler/ -run AgentSkillDefaults`。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 运行确认通过** — 同 Step 2。
- [ ] **Step 5: Commit** — `feat(agent): 技能默认配置 - 用户侧默认技能摘要接口`

### Task 9: 前端"Agent 默认技能"分区（收纳进 Agent（beta）设置页）

**Files:**
- Create: `web/src/services/api/admin-skill-defaults.ts`
- Create: `web/src/pages/admin/settings/components/agent-skill-defaults-section.tsx`
- Create: `web/test/admin-skill-defaults-section.test.ts`
- Modify: `web/src/pages/admin/settings/agent-settings-page.tsx`（nav 锚点加"默认技能"；`#config` 与 `#memory` 之间插入 `<section id="skill-defaults">`；页首 description 文案同步）
- Modify: `web/src/pages/admin/settings/agent-settings-page.css`（分区所需局部样式，只引用既有 token）

**Interfaces:**
- Consumes: `http`（`@/services/api/request`）、`listSkills`/`getSkill`（`@/services/api/skills.ts`）、Agent(beta) 页既有锚点分区模式（`agent-settings-page.tsx:70-88` 的 nav + section）、页面手写 `useState`+reload 数据流惯例。
- Produces: `listAdminAgentSkillDefaults(): Promise<AgentSkillDefaultsView>`、`updateAdminAgentSkillDefaults(input: { revision: number; items: AgentSkillDefaultItemInput[] }): Promise<{ revision: number }>`；类型 `AgentSkillDefaultsView`/`AgentSkillDefaultsViewItem`/`AgentSkillDefaultItemInput` 从该模块导出。分区行为：搜索技能 → 加入有序列表（上移/下移/移除、启用开关）→ 显示每项版本、文件数、大小与合计/上下文预估 → 保存携带加载时的 revision，冲突时提示重新加载；无效/超限原因展示后端 message 与 details。样式只用 `var(--token)`（间距/圆角/色彩/动效 token，参照页内 `agent-settings-field` 卡片写法），不新增全局 `.ant-*` 覆盖。
- 不新增一级导航：`admin-shell.tsx` 不改；`router.tsx` 不加新路由（挂在现有 `/admin/settings/agent` 页内）。

- [ ] **Step 1: 写失败测试** — 源码文本回归（照 `web/test/admin-ui-regressions.test.ts` 模式）：API 模块导出两个函数且 PUT path 为 `/admin/agent/skill-defaults`、保存 payload 含 `revision` 与 `items`；`agent-settings-page.tsx` 源码含 `<section id="skill-defaults"` 与 `<a href="#skill-defaults">`，且 description 含"默认技能"；分区组件文件引用 `AdminPageFrame` 之外还渲染合计/预估字段、冲突分支调用 `updateAdminAgentSkillDefaults` 后提示。
- [ ] **Step 2: 运行确认失败** — `cd web && bun test web/test/admin-skill-defaults-section.test.ts`。
- [ ] **Step 3: 实现** — 分区组件照 `agent-lessons-panel.tsx` 的自包含面板模式（防抖搜索可用 `useDebouncedValue`）；不引入新依赖。
- [ ] **Step 4: 运行确认通过** — 同 Step 2，另跑 `bun run lint`（Empty/Modal.confirm 退场规则）。
- [ ] **Step 5: Commit** — `feat(web): 技能默认配置 - Agent 设置页收纳默认技能分区`

### Task 10: 移除 Agent（beta）设置页"运行状态"分区

**Files:**
- Modify: `web/src/pages/admin/settings/agent-settings-page.tsx`（删 runtime 区：import `:7`、`AgentRuntimeStatus`/`listAgentSchedulerStatus` import `:9`、`instances`/`statusError`/`statusInFlight` 状态 `:25-29`、`loadStatus` `:37-49`、5 秒轮询 `:52`、保存后联动 `:64` 的 `void loadStatus()`、nav 的 `<a href="#runtime">` `:70`、section `:83-87`；`AdminPageFrame` description "统一管理 Agent 调度、执行器状态与用户记忆" → "统一管理 Agent 调度、默认技能与用户记忆"）
- Modify: `web/src/pages/admin/settings/agent-settings-page.css`（删除 `.agent-runtime-*` 全部规则 `:10-27` 及媒体查询中 `.agent-runtime` 部分）
- Delete: `web/src/pages/admin/settings/components/agent-runtime-status.tsx`
- Delete: `web/test/agent-runtime-status.test.tsx`
- Modify: `web/test/agent-settings-admin.test.ts`（`:22` 的 `expect(page).toContain("listAgentSchedulerStatus")` 改为断言页面**不再**包含 `listAgentSchedulerStatus` 与 `#runtime`；保留 `getAgentSchedulerSetting` 断言）

**Interfaces:**
- 保留不动：`web/src/services/api/admin-agent-settings.ts` 的 `getAgentSchedulerSetting`/`updateAgentSchedulerSetting`/`AgentSchedulerSetting`/`AgentSchedulerDraft`（调度配置区仍用）；后端 `/admin/settings/agent-scheduler/status` 接口保留（不属前端信息架构范围）；`admin-shell.tsx` 导航描述 "调度配置、运行状态与记忆管理" → "调度配置、默认技能与记忆管理"。

- [ ] **Step 1: 写失败测试** — 修改 `agent-settings-admin.test.ts`：断言页面源码不含 `listAgentSchedulerStatus`、`#runtime`、`AgentRuntimeStatusView`；仍含 `getAgentSchedulerSetting` 与 `#memory`。
- [ ] **Step 2: 运行确认失败** — `cd web && bun test web/test/agent-settings-admin.test.ts`（此时页面仍含 runtime 区，反向断言失败）。
- [ ] **Step 3: 实现** — 按 Files 清单删代码；确认 `agent-runtime-status.tsx` 无其他 import 方（`rg "agent-runtime-status" web/src`）后删除文件。
- [ ] **Step 4: 运行确认通过** — 同 Step 2 + `cd web && bun run typecheck`（捕捉 dead import/类型残留）+ `bun run lint`。
- [ ] **Step 5: Commit** — `refactor(web): Agent 设置页 - 移除运行状态分区并更新导航描述`

### Task 11: 前端技能选择器去 8 限制、来源展示与预算错误透出

**Files:**
- Modify: `web/src/components/canvas/canvas-agent-skill-library-modal.tsx`（:167-169 计数、:180 canSelect、:205 提示、:276/:284 禁用与 title）
- Modify: `web/src/components/canvas/canvas-cloud-agent-panel.tsx`（拉取默认摘要并传给弹窗）
- Modify: `web/src/services/api/agent.ts`（追加 `listAgentSkillDefaults(): Promise<AgentSkillDefaultsSummary>`，GET `/agent/skill-defaults`，复用 Task 8 的响应结构）
- Modify: `web/src/lib/canvas/agent-error-presentation.ts`（`agentSubmissionErrorTitle`，:23）
- Create: `web/test/agent-skill-selector-ui.test.ts`

**Interfaces:**
- Consumes: Task 8 端点、现有 `ApiError.reason`（`services/api/request.ts:18`）。
- Produces: 弹窗新增可选 prop `globalDefaultSkillIds?: string[]`；计数行显示 `全局默认 N 个 · 本会话追加 K 个`（K=selectedSkillIds.length），不再出现 `/ 8`，不因数量禁用选择（`canSelect` 恒可切换，含取消选择）；默认技能在列表中带"全局默认"标记且不可被用户移除；`agentSubmissionErrorTitle` 对 `reason === "agent_skill_budget_exceeded"` 的 ApiError 优先展示后端 msg（含预算名/上限/实际值）。提交路径 `:684` 与会话持久化 `:716` 不变（仍只存用户选择）。

- [ ] **Step 1: 写失败测试** — 源码文本回归：modal 源码不含 `/ 8`、`本轮最多启用 8 个`、`canSelect={selectedCount < 8`；含 `全局默认`；panel 传 `globalDefaultSkillIds`；`agent-error-presentation.ts` 构造 `{ reason: "agent_skill_budget_exceeded", message: "上下文预算超限…" }` 假错误断言标题取后端 message。
- [ ] **Step 2: 运行确认失败** — `cd web && bun test web/test/agent-skill-selector-ui.test.ts`。
- [ ] **Step 3: 实现** — 仅改列出的位置；`skill-runtime-picker.tsx` 的 profile `maxSkills: 4` 属另一运行时合同，设计未取消，不动。
- [ ] **Step 4: 运行确认通过** — 同 Step 2 + `bun run lint`。
- [ ] **Step 5: Commit** — `feat(web): 技能默认配置 - 技能选择器取消 8 个上限并展示默认技能来源`

### Task 12: 活动子智能体头像列表（悬停展开名称、让位平移）

**Files:**
- Create: `web/src/components/canvas/canvas-agent-subagent-list.tsx`
- Create: `web/src/components/canvas/canvas-agent-subagent-list.css`
- Create: `web/src/lib/canvas/agent-subagent-layout.ts`
- Create: `web/src/lib/canvas/agent-subagent-layout.test.ts`（并入 Step 1 一并创建）
- Create: `web/test/canvas-agent-subagent-list.test.tsx`
- Modify: `web/src/components/canvas/canvas-cloud-agent-panel.tsx`（在 `AgentConversation` 之后、`AgentPlanBar`/`AgentChatComposer` 之前的独立 flex 行位置——`:1040` 附近插入渲染，模式照 PlanBar 的 `shrink-0` 独立行）

**Interfaces:**
- Consumes: 既有动效 token（`web/src/styles/globals.css:256-276`：`--motion-dur-fast/base`、`--motion-ease-out = cubic-bezier(0.22,1,0.36,1)`、`--motion-ease-overshoot`）；圆形底模式（`grid place-items-center rounded-full`，panel.tsx:1387 先例）；`useReducedMotion`（`motion/react`）。
- Produces:
  - `export type AgentSubagentAvatarItem = { id: string; name: string; avatarUrl: string; state: "running" | "waiting" | "done" | "failed" }`（P3 Crew Run 落地前的最小合同；数据源未来由 SSE 投影填充）。
  - `export function computeSubagentOffsets(count: number, avatarSize: number, gap: number, expandedIndex: number, nameWidth: (index: number) => number): number[]` — 纯函数：返回每个头像的横向偏移；`expandedIndex < 0` 时全 0；展开头像右侧的所有头像右移 `nameWidth(expandedIndex) + gap`。放 lib 层便于单测与 P3 复用。
  - `export function AgentSubagentList({ items, theme }: { items: AgentSubagentAvatarItem[]; theme: CanvasTheme })`（`CanvasTheme` 自 `@/lib/canvas-theme` 导入，`canvas-agent-skill-library-modal.tsx:6` 先例）— 组件：`items.length === 0` 时返回 `null`（零占位，面板外观与现状一致）；头像为 `<img src={avatarUrl}>` 圆形（`rounded-full` + `object-cover`），外层 `grid place-items-center rounded-full` 容器；`state` 用 2px 环形色点表达（running 主题色脉冲、failed 语义红、done 语义绿，颜色引用语义 token）。悬停/`:focus-visible` 时：名称标签在头像右侧淡入展开（`opacity + width` 由 0 → `max-content` 实测宽度），位于其右侧的头像经 `transform: translateX(偏移)` 让位；动画 `transition: transform var(--motion-dur-base) var(--motion-ease-out), opacity var(--motion-dur-fast) var(--motion-ease-out)`（非线性缓出，非 linear）；`prefers-reduced-motion` 下不做位移，名称直接显隐；键盘 Tab 聚焦头像触发与 hover 同等展开（`onFocus`/`onBlur` 与 hover 状态合并），名称标签容器 `aria-label={name}`，头像按钮有 `:focus-visible` 环。
  - 头像资产：联网获取开源 SVG 存入 `web/public/icons/subagents/`（`<id>.svg`，命名与 `AgentSubagentAvatarItem.id` 对齐；选型优先开放许可的开源图标集，如 Iconoir / Tabler / Lucide 品牌区之外的角色头像资产；每枚 ≤2KB，手动整理入库，不引入运行时远程加载）。第一版内置一组与后续 Crew 角色规划对应的占位头像（导演、编剧、分镜、剪辑、审片 5 枚）。
- 面板接线：`canvas-cloud-agent-panel.tsx` 新增 `const [activeSubagents] = useState<AgentSubagentAvatarItem[]>([])`（本任务不接事件流，始终为空数组 → 组件渲染 `null`，面板与现状一致；P3 接入 SSE 后由投影 set）。插入行 `className="mx-3 mb-2 shrink-0"`（照 PlanBar 容器模式）。

- [ ] **Step 1: 写失败测试** — 纯函数单测（lib 层）：`computeSubagentOffsets` 在 expandedIndex=-1 全 0；expandedIndex=1 时 index≥2 偏移为 nameWidth(1)+gap、index 0/1 为 0；单元素展开返回全 0。组件测试（`renderToStaticMarkup`）：空 items 渲染空串；非空渲染 `agent-subagent-list` 容器、N 个 `rounded-full` 头像 `img`、`aria-label` 为名称；CSS 源码文本回归：`canvas-agent-subagent-list.css` 含 `var(--motion-ease-out)`、`@media (prefers-reduced-motion: reduce)` 位移禁用块、`:focus-visible` 规则；不含 `linear` 缓动。
- [ ] **Step 2: 运行确认失败** — `cd web && bun test web/src/lib/canvas/agent-subagent-layout.test.ts web/test/canvas-agent-subagent-list.test.ts`。
- [ ] **Step 3: 实现** — 按Interfaces实现；展开宽度用 `ref` 实测名称标签 `scrollWidth`（避免硬编码像素），位移经内联 `style={{ transform }}` 应用。
- [ ] **Step 4: 运行确认通过** — 同 Step 2 + `bun run lint`。
- [ ] **Step 5: Commit** — `feat(web): Agent 面板 - 活动子智能体圆形头像列表与悬停让位展开`

### Task 13: OpenAPI 与专题文档同步

**Files:**
- Modify: `backend/internal/handler/openapi.yaml`（`/admin/agent/skill-defaults` GET/PUT、`/agent/skill-defaults` GET）
- Modify: `docs/content/docs/backend/http-api.mdx`（新增"管理员 Agent 默认技能"小节 + 用户端点条目；"错误码"节登记 `agent_skill_budget_exceeded`、`agent_skill_defaults_revision_conflict`、`agent_skill_defaults_invalid`）
- Modify: `docs/content/docs/backend/backend-database.mdx`（`## 结构迁移与版本` 加 v51 段；技能表区加两表行，照 `skills` 行格式）
- Modify: `docs/content/docs/overview/features.mdx`（顶部新 `##` 条目：全局默认技能与容量准入、活动子智能体头像列表）
- Modify: `docs/content/docs/backend/code-map.mdx`（Agent 域小节补 `agent_skill_defaults` 相关路径与前端 subagent-list 组件）
- Modify: `docs/content/docs/progress/pending-test.mdx`（顶部登记验收项：20 技能继承、旧会话不漂移、未安装默认技能可运行、超限拒绝、双管理员冲突、Agent(beta) 页默认技能分区可用、运行状态分区已移除、子智能体头像悬停让位动画与 reduced-motion 降级、空态零占位）

- [ ] **Step 1: 按 AGENTS.md 第 9 节核对每个文件的落点与格式（docx 无构建校验，docs/ 不参与镜像与格式 CI）**。
- [ ] **Step 2: Commit** — `docs(agent): 技能默认配置 - 同步默认技能接口、数据表与待测试验收项`

### Task 14: 全量验证

- [ ] **Step 1:** `cd backend && gofmt -l .`（期望无输出）+ `go test ./...`（期望全绿；如有既有红用例与本计划相关则修复，无关则如实记录）。
- [ ] **Step 2:** `cd web && bun test`（全量）+ `bun run build`（含 `tsc --noEmit`）。
- [ ] **Step 3:** 若有修复，按模块补提交；在交付说明中如实记录验证结果与未验证项。
