# Task 4 Report: 管理员默认技能 service（校验、容量预检、CAS 保存）

## Status: DONE

## What Was Implemented

### `backend/internal/app/admin_agent_skill_defaults.go` (new)

Types per brief, verbatim names:

- `AgentSkillDefaultItem{SkillID, SkillVersionID string; Position, Enabled int}` — Enabled 用 0/1 便于 JSON；GET/PUT 同构。
- `AgentSkillDefaultsViewItem{SkillID, SkillName, SkillVersionID, VersionLabel string; Position, Enabled, Status, FileCount int; TotalBytes int64}`
- `AgentSkillDefaultsView{Revision int64; Items []AgentSkillDefaultsViewItem; TotalFiles int; TotalBytes, ContextEstimateBytes int64}` — totals 只统计 enabled 行；`ContextEstimateBytes = Σ enabled SkillVersion.TotalBytes`，字段注释注明是保守上限代理口径（假设整个技能包被读入上下文）。
- `func (s *Service) AdminAgentSkillDefaults(actor *model.User) (*AgentSkillDefaultsView, error)` — actor-first-param + `RequireAdmin` first line（对齐 `AdminAnalytics` 风格）。按 position 排序回读，每行附带技能/版本快照；引用已删除技能/版本的行保留展示但快照留空且不计入 totals（暴露悬空引用而不是悄悄吞掉）。
- `func (s *Service) ReplaceAgentSkillDefaults(actor *model.User, revision int64, items []AgentSkillDefaultItem) (int64, error)` — 校验顺序：RequireAdmin → 逐项校验 → `skills.AdmitSkillCapacity` 容量预检 → 互斥锁 → `repo.ReplaceAgentSkillDefaults`。

Per-item validation (`validateAgentSkillDefaultItem`):

- `repo.Skill(skillID)` 不存在 → `kernel.AgentSkillDefaultsInvalid`，message 含技能 ID
- `skill.Status != 1` → invalid，message 含技能 ID（显式区分"已禁用"与"不存在"，因为 `repo.Skill` 只查 status=1）
- `skill.IsPrivate` → invalid，message 含技能 ID
- `repo.SkillVersion(versionID).SkillID != skillID` → invalid，message 含技能 ID
- 容量预检只对 enabled 行构造 `SkillCapacityFacts`（FileCount/TotalBytes 来自 SkillVersion，ContextBytes = TotalBytes），budgets 用 Task 3 常量 `SkillRunMaxFiles/SkillRunMaxTotalBytes/SkillRunMaxContextBytes`。

Controller ruling implementation（Task 2 review）:

- `Service` 新增 `agentSkillDefaultsMu sync.Mutex`（`backend/internal/app/service.go`），包裹"读 revision → 删旧 → 写新"的 CAS 序列。中文注释说明：仓储层 CAS 在 PostgreSQL READ COMMITTED 下不是原子的，两个并发 replace 可能都通过；后端当前单 Go 进程，进程内互斥即可串行化；若未来多副本部署必须改数据库层 advisory lock。
- 防御性拷贝：service 层新建 `rows` 切片（`make([]model.AgentSkillDefault, 0, len(items))`）再传给 repo，不把调用方 items 直接透传，规避 repo 方法原地改写调用方 slice 的问题。

### `backend/internal/app/admin_agent_skill_defaults_test.go` (new)

- `newAgentSkillDefaultsService(t)`：sqlite in-memory（`file:<uuid>?mode=memory&cache=shared`），AutoMigrate `Skill, SkillVersion, SkillFile, UserSkillState, AgentSkillDefault`，`New(repository.New(db), t.TempDir())`。
- `seedAgentSkill`：经 `repository.CreateSkillWithPackage` 真实造数据（skill + version + 2 个 SkillFile + UserSkillState）；version `FileCount=2`、`TotalBytes` 可调，直接驱动容量数学。
- 8 个测试用例覆盖 brief 全部清单：
  1. `TestAdminAgentSkillDefaultsRequiresAdmin` — 非管理员 GET/PUT → 403；未登录 PUT → 401
  2. `TestAdminAgentSkillDefaultsRejectsPrivateSkill` — IsPrivate=true 拒绝，message 含 skill ID，reason `agent_skill_defaults_invalid`
  3. `TestAdminAgentSkillDefaultsRejectsForeignVersion` — versionID 属于别的技能 → 拒绝，message 含 skill ID
  4. `TestAdminAgentSkillDefaultsRejectsDisabledSkill` — Status=0 拒绝，message 含 skill ID（Review Focus 第 1 条）
  5. `TestAdminAgentSkillDefaultsRejectsContextBudgetOverflow` — 两个各占 512KB 的技能 → `details.budget == "context_bytes"`（文件数/整包字节仍在预算内，确保命中 context_bytes 项）
  6. `TestAdminAgentSkillDefaultsConflictOnStaleRevision` — 过期 expected revision → 409，details.currentRevision 存在且正确
  7. `TestAdminAgentSkillDefaultsSaveAndRoundTrip` — 合法保存返回新 revision；GET 回读顺序（position, skill_id）、SkillName/VersionLabel/Status/FileCount/TotalBytes、totals 只算 enabled 行；第二次保存后 revision 递增、totals 更新
  8. `TestAdminAgentSkillDefaultsConcurrentReplaceSingleWinner` — 两个 goroutine 以相同 expectedRevision 并发保存，恰好一成功一冲突，表内只剩赢家一行（controller ruling 的回归测试）

## TDD Evidence

RED（先写测试，实现尚未存在）：

```
$ go test ./internal/app/ -run "AdminAgentSkill"
internal\app\admin_agent_skill_defaults_test.go:71:13: undefined: AgentSkillDefaultItem
internal\app\admin_agent_skill_defaults_test.go:73:19: svc.AdminAgentSkillDefaults undefined ...
FAIL infinite-canvas/backend/internal/app [build failed]
```

GREEN（实现后）：

```
$ go test ./internal/app/ -run "AdminAgentSkill" -v
--- PASS: TestAdminAgentSkillDefaultsRequiresAdmin (9.73s)
--- PASS: TestAdminAgentSkillDefaultsRejectsPrivateSkill (8.89s)
--- PASS: TestAdminAgentSkillDefaultsRejectsForeignVersion (8.46s)
--- PASS: TestAdminAgentSkillDefaultsRejectsDisabledSkill (6.99s)
--- PASS: TestAdminAgentSkillDefaultsRejectsContextBudgetOverflow (5.21s)
--- PASS: TestAdminAgentSkillDefaultsConflictOnStaleRevision (3.86s)
--- PASS: TestAdminAgentSkillDefaultsSaveAndRoundTrip (3.60s)
--- PASS: TestAdminAgentSkillDefaultsConcurrentReplaceSingleWinner (3.61s)
ok  infinite-canvas/backend/internal/app 50.657s
```

修复 view revision 读取后复跑：`ok infinite-canvas/backend/internal/app 25.680s`。

## Whole-Package Verification

```
$ go test ./internal/app/
ok  infinite-canvas/backend/internal/app 488.598s
```

`go vet ./internal/app/` 无输出；`gofmt -l` 对两个新文件为空。

## Files Changed

- `backend/internal/app/admin_agent_skill_defaults.go` (new, ~170 lines)
- `backend/internal/app/admin_agent_skill_defaults_test.go` (new, ~330 lines)
- `backend/internal/app/service.go` (+1 line: `agentSkillDefaultsMu sync.Mutex` field)

## Commits

- `cd922e46` feat(agent): 技能默认配置 - 管理员默认技能服务与容量预检
- `a7b7d859` fix(agent): 技能默认配置 - 视图 revision 取最大值而非末行值

## Self-Review Findings

1. **Revision 读取修正（已修）**：初版 `AdminAgentSkillDefaults` 取循环内最后一次 `row.Revision` 赋值。行按 position 排序后末行 revision 不保证最大（管理员改一次 position 顺序就会出现），改为取 `max(row.Revision)`。
2. **Status 检查是显式的**：`repo.Skill` 只查 `status=1`，禁用技能会表现为"不存在"。brief 要求禁用技能被拒且 message 含 ID，故保留独立的 `skill.Status != 1` 分支给出"已被禁用"文案（该分支当前实际不可达，仅在 repo.Skill 语义变化时兜底；测试通过先把 Status 建成 0 再走禁用路径不可行——repo.Skill 会直接 NotFound——因此 TestRejectsDisabledSkill 实际命中的是 NotFound 分支，但 message 同样含技能 ID、reason 同为 invalid，行为契约满足）。
3. **并发测试的真实性**：互斥锁存在时并发测试恒为一胜一负；它同时保护未来锁被移除时的回归（sqlite 下 repo CAS 自身大概率也能挡住，但 PostgreSQL 下挡不住——这正是 controller ruling 的场景，进程内锁是唯一防线）。
4. **gofmt/vet**：新文件干净。`service.go` 的 gofmt 报告是仓库既有状态（整个文件换行符/历史格式问题，stash 验证过与本次改动无关），未触碰。

## Concerns

- 单进程 mutex 是 v1 约束：多副本部署（如未来水平扩容 backend）必须换 DB advisory lock。已按要求写进代码中文注释，建议在部署文档或 todo 中留一条记录（本任务范围未动 docs，避免越界）。
- `ContextEstimateBytes` 按"整包进上下文"计，实际运行时只读入 SKILL.md 与被引用文件，估值显著偏高是设计意图（保守上限），字段注释已注明。
