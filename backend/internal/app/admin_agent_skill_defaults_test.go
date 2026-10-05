package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newAgentSkillDefaultsService(t *testing.T) (*Service, *gorm.DB) {
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
	if err := db.AutoMigrate(&model.Skill{}, &model.SkillVersion{}, &model.SkillFile{}, &model.UserSkillState{}, &model.AgentSkillDefault{}, &model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}

func adminSkillDefaultsActor() *model.User {
	return &model.User{ID: "admin-1", Username: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
}

func TestAdminAgentSkillDefaultsAdmitsLargePackageWithSmallIndex(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "large-index", 2037941, nil)
	_, err := svc.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, []AgentSkillDefaultItem{{
		SkillID: "large-index", SkillVersionID: "large-index-v1", Enabled: 1,
	}})
	if err != nil {
		t.Fatalf("package bytes incorrectly counted as initial context: %v", err)
	}
	view, err := svc.AdminAgentSkillDefaults(adminSkillDefaultsActor())
	if err != nil {
		t.Fatal(err)
	}
	if view.TotalBytes != 2037941 || view.ContextEstimateBytes <= 0 || view.ContextEstimateBytes > 10000 {
		t.Fatalf("package and index estimates not separated: %+v", view)
	}
}

func TestReviewAdminSkillDefaultsDuplicateIsInvalid(t *testing.T) {
	s, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "duplicate", 128, nil)
	item := AgentSkillDefaultItem{SkillID: "duplicate", SkillVersionID: "duplicate-v1", Enabled: 1}
	_, err := s.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, []AgentSkillDefaultItem{item, item})
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) || appErr.Reason != "agent_skill_defaults_invalid" {
		t.Fatalf("duplicate should be a typed invalid input: %v", err)
	}
}

func TestAgentSkillDefaultsForUserSummary(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	for _, id := range []string{"first", "second", "disabled"} {
		seedAgentSkill(t, db, id, 128, nil)
	}
	_, err := svc.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, []AgentSkillDefaultItem{
		{SkillID: "second", SkillVersionID: "second-v1", Position: 2, Enabled: 1},
		{SkillID: "disabled", SkillVersionID: "disabled-v1", Position: 0, Enabled: 0},
		{SkillID: "first", SkillVersionID: "first-v1", Position: 1, Enabled: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.AgentSkillDefaultsForUser("ordinary-user")
	if err != nil {
		t.Fatal(err)
	}
	if view.Count != 2 || len(view.Skills) != 2 || view.Skills[0].SkillID != "first" || view.Skills[1].SkillID != "second" || view.Skills[0].SkillName != "技能-first" {
		t.Fatalf("unexpected summary: %+v", view)
	}
}

func TestAgentSkillDefaultsForUserEmpty(t *testing.T) {
	svc, _ := newAgentSkillDefaultsService(t)
	view, err := svc.AgentSkillDefaultsForUser("ordinary-user")
	if err != nil || view.Count != 0 || view.Skills == nil {
		t.Fatalf("empty summary must return an empty array: %+v %v", view, err)
	}
}

// seedAgentSkill 通过 CreateSkillWithPackage 造一个带版本包的技能；
// version.TotalBytes 直接决定容量预检与上下文预估的数值。
func seedAgentSkill(t *testing.T, db *gorm.DB, id string, totalBytes int64, mutate func(*model.Skill)) {
	t.Helper()
	now := time.Now()
	skill := model.Skill{
		ID: id, OwnerID: "author-1", Name: "技能-" + id, Description: "测试技能 " + id, Instruction: "# " + id + "\n",
		CurrentVersionID: id + "-v1", VersionLabel: "v1", ContentHash: "hash-" + id,
		FileCount: 2, TotalBytes: totalBytes, SourceType: "upload", SyncStatus: "synced",
		Status: 1, Source: 1, Tag: "others", ShowcaseMediaJSON: "[]", CreatedAt: now, UpdatedAt: now,
	}
	version := model.SkillVersion{
		ID: id + "-v1", SkillID: id, VersionLabel: "v1", ContentHash: "hash-" + id,
		EntryPath: "SKILL.md", FileCount: 2, TotalBytes: totalBytes, CreatedAt: now,
	}
	files := []model.SkillFile{
		{ID: id + "-f1", SkillVersionID: version.ID, Path: "SKILL.md", Kind: "markdown", MimeType: "text/markdown", Size: totalBytes / 2, SHA256: "a-" + id, CreatedAt: now},
		{ID: id + "-f2", SkillVersionID: version.ID, Path: "refs.md", Kind: "markdown", MimeType: "text/markdown", Size: totalBytes - totalBytes/2, SHA256: "b-" + id, CreatedAt: now},
	}
	state := &model.UserSkillState{ID: kernel.NewID(), UserID: "author-1", SkillID: id, Added: true, InstalledVersionID: version.ID, CreatedAt: now, UpdatedAt: now}
	if mutate != nil {
		mutate(&skill)
	}
	if err := repository.New(db).CreateSkillWithPackage(&skill, &version, files, state); err != nil {
		t.Fatal(err)
	}
}

func TestAdminAgentSkillDefaultsRequiresAdmin(t *testing.T) {
	svc, _ := newAgentSkillDefaultsService(t)
	plain := &model.User{ID: "user-1", Username: "user", Role: model.UserRoleUser, Status: model.UserStatusActive}
	items := []AgentSkillDefaultItem{{SkillID: "skill-1", SkillVersionID: "skill-1-v1", Position: 0, Enabled: 1}}

	if _, err := svc.AdminAgentSkillDefaults(plain); err == nil {
		t.Fatal("非管理员 GET 应被拒绝")
	} else {
		var appErr *kernel.AppError
		if !errors.As(err, &appErr) || appErr.Status != 403 {
			t.Fatalf("非管理员 GET 错误 = %v, want 403", err)
		}
	}
	if _, err := svc.ReplaceAgentSkillDefaults(plain, 0, items); err == nil {
		t.Fatal("非管理员 PUT 应被拒绝")
	} else {
		var appErr *kernel.AppError
		if !errors.As(err, &appErr) || appErr.Status != 403 {
			t.Fatalf("非管理员 PUT 错误 = %v, want 403", err)
		}
	}
	if _, err := svc.ReplaceAgentSkillDefaults(nil, 0, items); err == nil {
		t.Fatal("未登录 PUT 应被拒绝")
	} else {
		var appErr *kernel.AppError
		if !errors.As(err, &appErr) || appErr.Status != 401 {
			t.Fatalf("未登录 PUT 错误 = %v, want 401", err)
		}
	}
}

func TestAdminAgentSkillDefaultsRejectsPrivateSkill(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "skill-private", 128, func(skill *model.Skill) { skill.IsPrivate = true })

	items := []AgentSkillDefaultItem{{SkillID: "skill-private", SkillVersionID: "skill-private-v1", Position: 0, Enabled: 1}}
	_, err := svc.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, items)
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("私有技能应返回 *kernel.AppError，实际：%v", err)
	}
	if appErr.Status != 400 || appErr.Reason != kernel.ReasonAgentSkillDefaultsInvalid {
		t.Fatalf("status/reason = %d/%q, want 400/%q", appErr.Status, appErr.Reason, kernel.ReasonAgentSkillDefaultsInvalid)
	}
	if !strings.Contains(appErr.Message, "skill-private") {
		t.Fatalf("拒绝 message 应包含技能 ID，实际：%q", appErr.Message)
	}
}

func TestAdminAgentSkillDefaultsRejectsForeignVersion(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "skill-a", 128, nil)
	seedAgentSkill(t, db, "skill-b", 128, nil)

	items := []AgentSkillDefaultItem{{SkillID: "skill-a", SkillVersionID: "skill-b-v1", Position: 0, Enabled: 1}}
	_, err := svc.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, items)
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("版本不属于该技能应返回 *kernel.AppError，实际：%v", err)
	}
	if appErr.Status != 400 || appErr.Reason != kernel.ReasonAgentSkillDefaultsInvalid {
		t.Fatalf("status/reason = %d/%q, want 400/%q", appErr.Status, appErr.Reason, kernel.ReasonAgentSkillDefaultsInvalid)
	}
	if !strings.Contains(appErr.Message, "skill-a") {
		t.Fatalf("拒绝 message 应包含技能 ID，实际：%q", appErr.Message)
	}
}

func TestAdminAgentSkillDefaultsRejectsDisabledSkill(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "skill-disabled", 128, func(skill *model.Skill) { skill.Status = 0 })

	items := []AgentSkillDefaultItem{{SkillID: "skill-disabled", SkillVersionID: "skill-disabled-v1", Position: 0, Enabled: 1}}
	_, err := svc.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, items)
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("禁用技能应返回 *kernel.AppError，实际：%v", err)
	}
	if appErr.Status != 400 || appErr.Reason != kernel.ReasonAgentSkillDefaultsInvalid {
		t.Fatalf("status/reason = %d/%q, want 400/%q", appErr.Status, appErr.Reason, kernel.ReasonAgentSkillDefaultsInvalid)
	}
	if !strings.Contains(appErr.Message, "skill-disabled") {
		t.Fatalf("拒绝 message 应包含技能 ID，实际：%q", appErr.Message)
	}
}

func TestAdminAgentSkillDefaultsRejectsPackageBudgetOverflow(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "skill-cap-1", 8<<20, nil)
	seedAgentSkill(t, db, "skill-cap-2", (8<<20)+1, nil)

	items := []AgentSkillDefaultItem{
		{SkillID: "skill-cap-1", SkillVersionID: "skill-cap-1-v1", Position: 0, Enabled: 1},
		{SkillID: "skill-cap-2", SkillVersionID: "skill-cap-2-v1", Position: 1, Enabled: 1},
	}
	_, err := svc.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, items)
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("超包体积预算应返回 *kernel.AppError，实际：%v", err)
	}
	if appErr.Reason != kernel.ReasonAgentSkillBudgetExceeded {
		t.Fatalf("reason = %q, want %q", appErr.Reason, kernel.ReasonAgentSkillBudgetExceeded)
	}
	if budget, _ := appErr.Details["budget"].(string); budget != "total_bytes" {
		t.Fatalf("details.budget = %#v, want \"total_bytes\"", appErr.Details["budget"])
	}
}

func TestAdminAgentSkillDefaultsConflictOnStaleRevision(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "skill-a", 128, nil)
	seedAgentSkill(t, db, "skill-b", 128, nil)
	admin := adminSkillDefaultsActor()

	first := []AgentSkillDefaultItem{{SkillID: "skill-a", SkillVersionID: "skill-a-v1", Position: 0, Enabled: 1}}
	revision, err := svc.ReplaceAgentSkillDefaults(admin, 0, first)
	if err != nil {
		t.Fatalf("首次保存失败：%v", err)
	}
	if revision != 1 {
		t.Fatalf("首次保存 revision = %d, want 1", revision)
	}

	stale := []AgentSkillDefaultItem{{SkillID: "skill-b", SkillVersionID: "skill-b-v1", Position: 0, Enabled: 1}}
	_, err = svc.ReplaceAgentSkillDefaults(admin, 0, stale)
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("过期 revision 应返回 *kernel.AppError，实际：%v", err)
	}
	if appErr.Status != 409 || appErr.Reason != kernel.ReasonAgentSkillDefaultsRevisionConflict {
		t.Fatalf("status/reason = %d/%q, want 409/%q", appErr.Status, appErr.Reason, kernel.ReasonAgentSkillDefaultsRevisionConflict)
	}
	if current, _ := appErr.Details["currentRevision"].(int64); current != 1 {
		t.Fatalf("details.currentRevision = %#v, want int64(1)", appErr.Details["currentRevision"])
	}
}

func TestAdminAgentSkillDefaultsSaveAndRoundTrip(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "skill-a", 1000, nil)
	seedAgentSkill(t, db, "skill-b", 2000, nil)
	admin := adminSkillDefaultsActor()

	items := []AgentSkillDefaultItem{
		{SkillID: "skill-b", SkillVersionID: "skill-b-v1", Position: 0, Enabled: 0},
		{SkillID: "skill-a", SkillVersionID: "skill-a-v1", Position: 1, Enabled: 1},
	}
	revision, err := svc.ReplaceAgentSkillDefaults(admin, 0, items)
	if err != nil {
		t.Fatalf("合法保存失败：%v", err)
	}
	if revision != 1 {
		t.Fatalf("revision = %d, want 1", revision)
	}

	view, err := svc.AdminAgentSkillDefaults(admin)
	if err != nil {
		t.Fatalf("GET 失败：%v", err)
	}
	if view.Revision != 1 {
		t.Fatalf("view.Revision = %d, want 1", view.Revision)
	}
	if len(view.Items) != 2 {
		t.Fatalf("items = %d, want 2：%#v", len(view.Items), view.Items)
	}
	if view.Items[0].SkillID != "skill-b" || view.Items[0].Position != 0 || view.Items[0].Enabled != 0 {
		t.Fatalf("items[0] = %#v, want skill-b/pos 0/disabled", view.Items[0])
	}
	second := view.Items[1]
	if second.SkillID != "skill-a" || second.Position != 1 || second.Enabled != 1 {
		t.Fatalf("items[1] = %#v, want skill-a/pos 1/enabled", second)
	}
	if second.SkillName != "技能-skill-a" || second.SkillVersionID != "skill-a-v1" || second.VersionLabel != "v1" {
		t.Fatalf("items[1] 名称/版本 = %q/%q/%q", second.SkillName, second.SkillVersionID, second.VersionLabel)
	}
	if second.Status != 1 || second.FileCount != 2 || second.TotalBytes != 1000 {
		t.Fatalf("items[1] status/fileCount/totalBytes = %d/%d/%d, want 1/2/1000", second.Status, second.FileCount, second.TotalBytes)
	}
	// totals 只统计 enabled 行：skill-b 处于禁用状态，不得计入。
	if view.TotalFiles != 2 || view.TotalBytes != 1000 || view.ContextEstimateBytes <= 0 || view.ContextEstimateBytes > 10000 {
		t.Fatalf("invalid package/index totals: %d/%d/%d", view.TotalFiles, view.TotalBytes, view.ContextEstimateBytes)
	}

	next := []AgentSkillDefaultItem{{SkillID: "skill-b", SkillVersionID: "skill-b-v1", Position: 0, Enabled: 1}}
	revision, err = svc.ReplaceAgentSkillDefaults(admin, 1, next)
	if err != nil {
		t.Fatalf("第二次保存失败：%v", err)
	}
	if revision != 2 {
		t.Fatalf("第二次保存 revision = %d, want 2", revision)
	}
	view, err = svc.AdminAgentSkillDefaults(admin)
	if err != nil {
		t.Fatalf("第二次 GET 失败：%v", err)
	}
	if view.Revision != 2 || len(view.Items) != 1 {
		t.Fatalf("revision/items = %d/%d, want 2/1", view.Revision, len(view.Items))
	}
	if view.TotalFiles != 2 || view.TotalBytes != 2000 || view.ContextEstimateBytes <= 0 || view.ContextEstimateBytes > 10000 {
		t.Fatalf("invalid package/index totals: %d/%d/%d", view.TotalFiles, view.TotalBytes, view.ContextEstimateBytes)
	}
}

// 并发保存必须恰有一个成功、另一个收到冲突：service 层互斥锁把 CAS 的读改写串行化，
// 排除 PostgreSQL READ COMMITTED 下两次 replace 同时通过的可能。
func TestAdminAgentSkillDefaultsConcurrentReplaceSingleWinner(t *testing.T) {
	svc, db := newAgentSkillDefaultsService(t)
	seedAgentSkill(t, db, "skill-c1", 128, nil)
	seedAgentSkill(t, db, "skill-c2", 128, nil)
	admin := adminSkillDefaultsActor()

	type attempt struct {
		revision int64
		err      error
	}
	results := make(chan attempt, 2)
	for _, item := range []AgentSkillDefaultItem{
		{SkillID: "skill-c1", SkillVersionID: "skill-c1-v1", Position: 0, Enabled: 1},
		{SkillID: "skill-c2", SkillVersionID: "skill-c2-v1", Position: 0, Enabled: 1},
	} {
		go func(item AgentSkillDefaultItem) {
			revision, err := svc.ReplaceAgentSkillDefaults(admin, 0, []AgentSkillDefaultItem{item})
			results <- attempt{revision: revision, err: err}
		}(item)
	}
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil {
			successes++
			if result.revision != 1 {
				t.Fatalf("成功者 revision = %d, want 1", result.revision)
			}
			continue
		}
		var appErr *kernel.AppError
		if errors.As(result.err, &appErr) && appErr.Status == 409 {
			conflicts++
			continue
		}
		t.Fatalf("并发保存应要么成功要么冲突，实际：%v", result.err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("并发保存结果 = %d 成功 / %d 冲突, want 1/1", successes, conflicts)
	}
	rows, err := repository.New(db).AgentSkillDefaultRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("并发后应只剩赢家的一行，实际：%#v", rows)
	}
}
