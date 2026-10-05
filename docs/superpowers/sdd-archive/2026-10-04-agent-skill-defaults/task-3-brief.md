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

