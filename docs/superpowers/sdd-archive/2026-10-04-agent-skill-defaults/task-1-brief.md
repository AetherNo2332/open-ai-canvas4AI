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

