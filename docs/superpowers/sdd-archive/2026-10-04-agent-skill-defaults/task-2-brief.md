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

