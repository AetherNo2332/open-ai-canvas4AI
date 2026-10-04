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

