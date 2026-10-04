package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestReviewSkillDefaultsCASAcrossIndependentConnections(t *testing.T) {
	dsn := filepath.ToSlash(filepath.Join(t.TempDir(), "defaults.db")) + "?_journal_mode=WAL&_busy_timeout=5000"
	repos := make([]*Repository, 2)
	for i := range repos {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		if err := db.AutoMigrate(&model.AgentSkillDefault{}, &model.SystemSetting{}); err != nil {
			t.Fatal(err)
		}
		repos[i] = New(db)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, repo := range repos {
		go func(repo *Repository) {
			<-start
			_, err := repo.ReplaceAgentSkillDefaults(0, []model.AgentSkillDefault{{ID: kernel.NewID(), SkillID: kernel.NewID(), Enabled: true}}, "admin")
			results <- err
		}(repo)
	}
	close(start)
	successes, conflicts := 0, 0
	for range repos {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		var appErr *kernel.AppError
		if errors.As(err, &appErr) && appErr.Reason == kernel.ReasonAgentSkillDefaultsRevisionConflict {
			conflicts++
			continue
		}
		t.Fatalf("unexpected CAS error: %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS: %d successes, %d conflicts", successes, conflicts)
	}
	rows, revision, err := repos[0].AgentSkillDefaultsSnapshot()
	if err != nil || revision != 1 || len(rows) != 1 {
		t.Fatalf("incoherent snapshot: %v %d %v", rows, revision, err)
	}
}

func newAgentSkillDefaultsTestDB(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.AgentSkillDefault{}, &model.AgentConversationSkill{}, &model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestReviewSkillDefaultsClearPreservesRevision(t *testing.T) {
	r := newAgentSkillDefaultsTestDB(t)
	rows := []model.AgentSkillDefault{{ID: "first", SkillID: "skill", SkillVersionID: "v1", Enabled: true}}
	if _, err := r.ReplaceAgentSkillDefaults(0, rows, "admin"); err != nil {
		t.Fatal(err)
	}
	if rev, err := r.ReplaceAgentSkillDefaults(1, nil, "admin"); err != nil || rev != 2 {
		t.Fatalf("clear: %d %v", rev, err)
	}
	if rev, err := r.ReplaceAgentSkillDefaults(2, []model.AgentSkillDefault{{ID: "next", SkillID: "next", SkillVersionID: "v2", Enabled: true}}, "admin"); err != nil || rev != 3 {
		t.Fatalf("revision must survive empty list: %d %v", rev, err)
	}
	if _, err := r.ReplaceAgentSkillDefaults(1, rows, "stale-admin"); err == nil {
		t.Fatal("stale revision must not become valid again")
	}
}

func TestReplaceAgentSkillDefaultsCASChain(t *testing.T) {
	repo := newAgentSkillDefaultsTestDB(t)

	first := []model.AgentSkillDefault{
		{ID: "asd-1", Scope: "global", SkillID: "skill-1", SkillVersionID: "sv-1", Position: 0, Enabled: true},
		{ID: "asd-2", Scope: "global", SkillID: "skill-2", SkillVersionID: "sv-2", Position: 1, Enabled: true},
	}
	revision, err := repo.ReplaceAgentSkillDefaults(0, first, "admin-1")
	if err != nil {
		t.Fatalf("空表 expected=0 保存失败：%v", err)
	}
	if revision != 1 {
		t.Fatalf("首次保存 revision = %d, want 1", revision)
	}

	second := []model.AgentSkillDefault{
		{ID: "asd-3", Scope: "global", SkillID: "skill-2", SkillVersionID: "sv-2b", Position: 0, Enabled: true},
	}
	revision, err = repo.ReplaceAgentSkillDefaults(1, second, "admin-2")
	if err != nil {
		t.Fatalf("expected=1 保存失败：%v", err)
	}
	if revision != 2 {
		t.Fatalf("第二次保存 revision = %d, want 2", revision)
	}

	rows, err := repo.AgentSkillDefaultRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SkillID != "skill-2" || rows[0].SkillVersionID != "sv-2b" {
		t.Fatalf("replace 未清掉旧行或写错新行：%#v", rows)
	}
	if rows[0].Revision != 2 || rows[0].UpdatedBy != "admin-2" {
		t.Fatalf("新行 revision/updatedBy = %d/%q, want 2/admin-2", rows[0].Revision, rows[0].UpdatedBy)
	}
}

func TestReplaceAgentSkillDefaultsStaleRevisionConflict(t *testing.T) {
	repo := newAgentSkillDefaultsTestDB(t)

	if _, err := repo.ReplaceAgentSkillDefaults(0, []model.AgentSkillDefault{{ID: "asd-1", Scope: "global", SkillID: "skill-1", Enabled: true}}, "admin-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReplaceAgentSkillDefaults(1, []model.AgentSkillDefault{{ID: "asd-2", Scope: "global", SkillID: "skill-2", Enabled: true}}, "admin-1"); err != nil {
		t.Fatal(err)
	}

	rows := []model.AgentSkillDefault{{ID: "asd-3", Scope: "global", SkillID: "skill-3", Enabled: true}}
	_, err := repo.ReplaceAgentSkillDefaults(1, rows, "admin-2")
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("过期 revision 应返回 *kernel.AppError，实际：%v", err)
	}
	if appErr.Status != 409 || appErr.Reason != kernel.ReasonAgentSkillDefaultsRevisionConflict {
		t.Fatalf("conflict status/reason = %d/%q, want 409/%q", appErr.Status, appErr.Reason, kernel.ReasonAgentSkillDefaultsRevisionConflict)
	}
	current, ok := appErr.Details["currentRevision"].(int64)
	if !ok || current != 2 {
		t.Fatalf("details currentRevision = %#v, want int64(2)", appErr.Details["currentRevision"])
	}

	remaining, err := repo.AgentSkillDefaultRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].SkillID != "skill-2" || remaining[0].Revision != 2 {
		t.Fatalf("冲突时不得改动现有行，剩余：%#v", remaining)
	}
}

func TestReplaceAgentSkillDefaultsEmptyTableExpectedZero(t *testing.T) {
	repo := newAgentSkillDefaultsTestDB(t)

	revision, err := repo.ReplaceAgentSkillDefaults(0, nil, "admin-1")
	if err != nil {
		t.Fatalf("空表空集合保存失败：%v", err)
	}
	if revision != 1 {
		t.Fatalf("revision = %d, want 1", revision)
	}
	rows, err := repo.AgentSkillDefaultRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("空集合应清空表，剩余：%#v", rows)
	}
}

func TestAgentSkillDefaultRowsOrderedByPositionAndSkillID(t *testing.T) {
	repo := newAgentSkillDefaultsTestDB(t)

	rows := []model.AgentSkillDefault{
		{ID: "asd-b", Scope: "global", SkillID: "skill-b", Position: 1, Enabled: true},
		{ID: "asd-c", Scope: "global", SkillID: "skill-c", Position: 1, Enabled: true},
		{ID: "asd-a", Scope: "global", SkillID: "skill-a", Position: 0, Enabled: true},
		{ID: "asd-d", Scope: "global", SkillID: "skill-d", Position: 2, Enabled: false},
	}
	if _, err := repo.ReplaceAgentSkillDefaults(0, rows, "admin-1"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.AgentSkillDefaultRows()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"skill-a", "skill-b", "skill-c", "skill-d"}
	for i, id := range want {
		if got[i].SkillID != id {
			t.Fatalf("rows[%d].SkillID = %q, want %q（应按 position, skill_id 排序）", i, got[i].SkillID, id)
		}
	}

	enabled, err := repo.EnabledAgentSkillDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 3 {
		t.Fatalf("enabled rows = %d, want 3", len(enabled))
	}
	if enabled[0].SkillID != "skill-a" || enabled[2].SkillID != "skill-c" {
		t.Fatalf("enabled 排序错误：%#v", enabled)
	}
}

func TestSaveAgentConversationSkillsUpsertWithoutDuplicates(t *testing.T) {
	repo := newAgentSkillDefaultsTestDB(t)

	initial := []model.AgentConversationSkill{
		{ConversationID: "conv-1", SkillID: "skill-1", SkillVersionID: "sv-1", ContentHash: "hash-1", Source: "global", Position: 0},
		{ConversationID: "conv-1", SkillID: "skill-2", SkillVersionID: "sv-2", ContentHash: "hash-2", Source: "user", Position: 1},
	}
	if err := repo.SaveAgentConversationSkills(initial); err != nil {
		t.Fatal(err)
	}

	updated := []model.AgentConversationSkill{
		{ConversationID: "conv-1", SkillID: "skill-1", SkillVersionID: "sv-1b", ContentHash: "hash-1b", Source: "user", Position: 1},
		{ConversationID: "conv-1", SkillID: "skill-3", SkillVersionID: "sv-3", ContentHash: "hash-3", Source: "global", Position: 0},
	}
	if err := repo.SaveAgentConversationSkills(updated); err != nil {
		t.Fatal(err)
	}

	got, err := repo.AgentConversationSkills("conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("upsert 后应共 3 行，实际 %d 行：%#v", len(got), got)
	}
	if got[0].SkillID != "skill-3" || got[1].SkillID != "skill-1" || got[2].SkillID != "skill-2" {
		t.Fatalf("会话技能应按 position 排序：%#v", got)
	}
	if got[1].SkillVersionID != "sv-1b" || got[1].ContentHash != "hash-1b" || got[1].Source != "user" {
		t.Fatalf("skill-1 未被 upsert 更新：%#v", got[1])
	}
	if got[1].Position != 1 || got[2].Position != 1 {
		t.Fatalf("skill-1/skill-2 position = %d/%d, want 1/1", got[1].Position, got[2].Position)
	}
}

func TestAgentConversationSkillsIsolatedByConversationID(t *testing.T) {
	repo := newAgentSkillDefaultsTestDB(t)

	rows := []model.AgentConversationSkill{
		{ConversationID: "conv-alice", SkillID: "skill-1", SkillVersionID: "sv-1", ContentHash: "hash-1", Source: "global", Position: 0},
		{ConversationID: "conv-alice", SkillID: "skill-2", SkillVersionID: "sv-2", ContentHash: "hash-2", Source: "user", Position: 1},
	}
	if err := repo.SaveAgentConversationSkills(rows); err != nil {
		t.Fatal(err)
	}

	other, err := repo.AgentConversationSkills("conv-bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("其他会话的查询应返回空，实际：%#v", other)
	}

	alice, err := repo.AgentConversationSkills("conv-alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(alice) != 2 {
		t.Fatalf("own 会话应返回 2 行，实际 %d 行", len(alice))
	}
}
