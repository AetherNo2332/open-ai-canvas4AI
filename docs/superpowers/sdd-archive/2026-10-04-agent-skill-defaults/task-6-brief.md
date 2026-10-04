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

