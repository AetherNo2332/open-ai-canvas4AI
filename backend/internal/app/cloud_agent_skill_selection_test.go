package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/skills"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func timeNow() time.Time { return time.Now() }

func errorsAs(err error, target *(*AppError)) bool { return errors.As(err, target) }

// selectionDataDir 是本测试文件共享的 dataDir：Service 与 ZIP 落盘都必须用同一个。
var selectionDataDir string

func conversationItems(rows []model.AgentConversationSkill) []skills.SkillSelectionItem {
	items := make([]skills.SkillSelectionItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, skills.SkillSelectionItem{SkillID: row.SkillID, VersionID: row.SkillVersionID, ContentHash: row.ContentHash, Source: row.Source})
	}
	return items
}

// newRunSkillSelectionService 复用 Task 4 的种子手法：内存库 + AutoMigrate 相关表 + 直接构造 Service。
// 不走 creationTestService（那套要渠道/积分/创建运行），本文件只测技能解析层。
// CloudAgentExecution 带 Journal/Transcript 关系，AutoMigrate 必须包含事件与消息记录表，
// 否则 GORM ReorderModels 解析关系时会 panic。
func newRunSkillSelectionService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(databaseModelsForSkillSelection()...); err != nil {
		t.Fatal(err)
	}
	if selectionDataDir == "" {
		selectionDataDir = t.TempDir()
		t.Cleanup(func() { selectionDataDir = "" })
	}
	return New(repository.New(db), selectionDataDir), db
}

func databaseModelsForSkillSelection() []any {
	return []any{
		&model.Skill{}, &model.SkillVersion{}, &model.SkillFile{}, &model.UserSkillState{},
		&model.AgentSkillDefault{}, &model.AgentConversationSkill{},
		&model.CloudAgentExecution{}, &model.CloudAgentEventRecord{}, &model.CloudAgentMessageRecord{},
		// SkillDetail → skillItems 查询技能作者（users）与作者头像（user_identities）。
		&model.User{}, &model.UserIdentity{},
	}
}

// seedSelectionSkillFor 直接落库一个确定性 ID 的技能（不走 CreateSkill：那会生成随机
// ID 且对指令有 1-100000 字符限制，容量测试需要更大体积）。同时按
// validateSkillPackageSnapshot 的合同落一份真实 ZIP：PackageKey 指向 dataDir 内的
// skill-packages/{skillID}/{versionID}.zip，文件行携带真实 SHA256 与字节数，
// 版本行的 FileCount/TotalBytes/ContentHash 与 ZIP 完全一致。
func seedSelectionSkillFor(t *testing.T, s *Service, db *gorm.DB, ownerID, id string, approxBytes int64, private bool, status int) {
	t.Helper()
	now := timeNow()
	skillBody := "---\nname: " + id + "\ndescription: 测试技能 " + id + "\n---\n\n" + strings.Repeat("内容。", int(approxBytes)/9)
	refsBody := "# refs\n"
	contents := map[string][]byte{"SKILL.md": []byte(skillBody), "refs.md": []byte(refsBody)}
	contentHash, totalBytes := selectionContentHash(contents)
	versionID := id + "-v1"
	packageKey := id + "/" + versionID + ".zip"
	skill := model.Skill{
		ID: id, OwnerID: ownerID, Name: "技能-" + id, Description: "测试技能 " + id, Instruction: skillBody,
		CurrentVersionID: versionID, VersionLabel: "v1", ContentHash: contentHash,
		FileCount: len(contents), TotalBytes: totalBytes, SourceType: "upload", SyncStatus: "synced",
		Status: status, Source: 1, Tag: "others", IsPrivate: private, ShowcaseMediaJSON: "[]", CreatedAt: now, UpdatedAt: now,
	}
	version := model.SkillVersion{
		ID: versionID, SkillID: id, VersionLabel: "v1", ContentHash: contentHash,
		EntryPath: "SKILL.md", PackageKey: packageKey, FileCount: len(contents), TotalBytes: totalBytes, CreatedAt: now,
	}
	files := make([]model.SkillFile, 0, len(contents))
	for _, path := range []string{"SKILL.md", "refs.md"} {
		content := contents[path]
		digest := sha256.Sum256(content)
		files = append(files, model.SkillFile{ID: id + "-f-" + path, SkillVersionID: versionID, Path: path,
			Kind: "markdown", MimeType: "text/markdown", Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), CreatedAt: now})
	}
	ownerState := &model.UserSkillState{ID: kernel.NewID(), UserID: ownerID, SkillID: id, Added: true, InstalledVersionID: versionID, CreatedAt: now, UpdatedAt: now}
	if err := repository.New(db).CreateSkillWithPackage(&skill, &version, files, ownerState); err != nil {
		t.Fatal(err)
	}
	if err := writeSelectionPackageZip(t, packageKey, contents); err != nil {
		t.Fatal(err)
	}
}

func seedSelectionSkill(t *testing.T, s *Service, db *gorm.DB, id string, approxBytes int64, private bool, status int) {
	t.Helper()
	seedSelectionSkillFor(t, s, db, "author-1", id, approxBytes, private, status)
}

// selectionContentHash 复刻 skills 包的 contentHashAndSize：hash.Write(path)+0+content+0，按路径排序。
func selectionContentHash(files map[string][]byte) (string, int64) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var total int64
	hash := sha256.New()
	for _, p := range paths {
		hash.Write([]byte(p))
		hash.Write([]byte{0})
		hash.Write(files[p])
		hash.Write([]byte{0})
		total += int64(len(files[p]))
	}
	return hex.EncodeToString(hash.Sum(nil)), total
}

// writeSelectionPackageZip 把 ZIP 写到共享 dataDir 的 skill-packages/{packageKey}。
func writeSelectionPackageZip(t *testing.T, packageKey string, files map[string][]byte) error {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		entry, err := writer.Create(p)
		if err != nil {
			return err
		}
		if _, err := entry.Write(files[p]); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	target := filepath.Join(selectionDataDir, "skill-packages", filepath.FromSlash(packageKey))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, buffer.Bytes(), 0o600)
}

// seedDefault 以管理员身份把一个已存在的技能设为 enabled 全局默认。
// CreateSkill 生成的版本 ID 是随机 ID，这里回读当前版本而不是假设 {id}-v1。
func seedDefault(t *testing.T, db *gorm.DB, skillID string, position int) {
	t.Helper()
	var skill model.Skill
	if err := db.First(&skill, "id = ?", skillID).Error; err != nil {
		t.Fatal(err)
	}
	row := model.AgentSkillDefault{ID: kernel.NewID(), Scope: "global", SkillID: skillID, SkillVersionID: skill.CurrentVersionID, Position: position, Enabled: true, Revision: 1, UpdatedBy: "admin-1"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

func conversationRows(t *testing.T, db *gorm.DB, conversationID string) []model.AgentConversationSkill {
	t.Helper()
	var rows []model.AgentConversationSkill
	if err := db.Where("conversation_id = ?", conversationID).Order("position asc").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func skillIDs(items []skills.SkillSelectionItem) string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.SkillID)
	}
	return strings.Join(ids, ",")
}

func TestResolveRunSkillsNewConversationInjectsDefaults(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	for i := 0; i < 20; i++ {
		seedSelectionSkill(t, s, db, fmt.Sprintf("d-%02d", i), 1024, false, 1)
		seedDefault(t, db, fmt.Sprintf("d-%02d", i), i)
	}
	// user 自己的技能，未被设为默认。
	seedSelectionSkillFor(t, s, db, "user", "own-1", 1024, true, 1)

	snapshots, err := s.resolveRunSkills("user", "conv-new", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 20 {
		t.Fatalf("快照数 = %d, want 20（20 个默认技能完整继承）", len(snapshots))
	}
	rows := conversationRows(t, db, "conv-new")
	if len(rows) != 20 {
		t.Fatalf("会话技能行数 = %d, want 20", len(rows))
	}
	for _, row := range rows {
		if row.Source != skills.SkillSourceGlobal {
			t.Fatalf("技能 %s 来源 = %q, want global", row.SkillID, row.Source)
		}
	}
	// 用户追加自己的私有技能：并集语义，默认仍在。
	snapshots, err = s.resolveRunSkills("user", "conv-new2", []string{"own-1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 21 {
		t.Fatalf("快照数 = %d, want 21（20 默认 + 1 用户）", len(snapshots))
	}
	rows = conversationRows(t, db, "conv-new2")
	if rows[len(rows)-1].SkillID != "own-1" || rows[len(rows)-1].Source != skills.SkillSourceUser {
		t.Fatalf("用户技能应排在其后且 source=user: %+v", rows[len(rows)-1])
	}
}

func TestResolveRunSkillsContinuationKeepsFrozenSet(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	for i := 0; i < 3; i++ {
		seedSelectionSkill(t, s, db, fmt.Sprintf("k-%d", i), 1024, false, 1)
		seedDefault(t, db, fmt.Sprintf("k-%d", i), i)
	}
	// 第一轮：3 个默认技能。
	if _, err := s.resolveRunSkills("user", "conv-a", nil, true); err != nil {
		t.Fatal(err)
	}
	// 管理员替换默认集合（清空 k-2、新增 k-3）：模拟第二轮创建前发生的变更。
	if err := db.Where("skill_id = ?", "k-2").Delete(&model.AgentSkillDefault{}).Error; err != nil {
		t.Fatal(err)
	}
	seedSelectionSkill(t, s, db, "k-3", 1024, false, 1)
	seedDefault(t, db, "k-3", 2)

	// 第二轮（续聊）：技能集与第一轮一致，不含 k-3。
	if _, err := s.resolveRunSkills("user", "conv-a", nil, false); err != nil {
		t.Fatal(err)
	}
	rows := conversationRows(t, db, "conv-a")
	if got := skillIDs(conversationItems(rows)); got != "k-0,k-1,k-2" {
		t.Fatalf("续聊技能集漂移: %q", got)
	}
}

func TestResolveRunSkillsContinuationAppendsUserPick(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	seedSelectionSkill(t, s, db, "d-1", 1024, false, 1)
	seedSelectionSkill(t, s, db, "d-2", 1024, false, 1)
	seedSelectionSkillFor(t, s, db, "user", "own-x", 1024, true, 1)
	seedDefault(t, db, "d-1", 0)
	seedDefault(t, db, "d-2", 1)
	if _, err := s.resolveRunSkills("user", "conv-b", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveRunSkills("user", "conv-b", []string{"own-x"}, false); err != nil {
		t.Fatal(err)
	}
	rows := conversationRows(t, db, "conv-b")
	if got := skillIDs(conversationItems(rows)); got != "d-1,d-2,own-x" {
		t.Fatalf("用户追加失败: %q", got)
	}
}

func TestResolveRunSkillsUserPickUpgradesSource(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	seedSelectionSkillFor(t, s, db, "user", "d-1", 1024, false, 1)
	seedDefault(t, db, "d-1", 0)
	if _, err := s.resolveRunSkills("user", "conv-c", nil, true); err != nil {
		t.Fatal(err)
	}
	rows := conversationRows(t, db, "conv-c")
	if rows[0].Source != skills.SkillSourceGlobal {
		t.Fatalf("初始来源 = %q, want global", rows[0].Source)
	}
	// 用户对同一默认技能显式选择：升级为 user 来源并跟踪其安装版本。
	if _, err := s.resolveRunSkills("user", "conv-c", []string{"d-1"}, false); err != nil {
		t.Fatal(err)
	}
	rows = conversationRows(t, db, "conv-c")
	if len(rows) != 1 || rows[0].Source != skills.SkillSourceUser {
		t.Fatalf("升级失败: %+v", rows)
	}
}

func TestResolveRunSkillsDefaultNotInstalledStillLoads(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	// d-1 归属 author-1，user 未安装（无 UserSkillState.Added）。
	seedSelectionSkill(t, s, db, "d-1", 1024, false, 1)
	seedDefault(t, db, "d-1", 0)
	snapshots, err := s.resolveRunSkills("user", "conv-d", nil, true)
	if err != nil {
		t.Fatalf("未安装的默认技能不应阻塞装配: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != "d-1" {
		t.Fatalf("快照缺失: %+v", snapshots)
	}
}

func TestResolveRunSkillsRejectsDisabledDefault(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	seedSelectionSkill(t, s, db, "d-bad", 1024, false, 1)
	seedDefault(t, db, "d-bad", 0)
	// 禁用技能：repo.Skill 只查 status=1，读默认层时会 NotFound。
	if err := db.Model(&model.Skill{}).Where("id = ?", "d-bad").Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	_, err := s.resolveRunSkills("user", "conv-e", nil, true)
	if err == nil {
		t.Fatal("引用禁用技能的默认集合应让建 run 失败")
	}
	if !strings.Contains(err.Error(), "d-bad") {
		t.Fatalf("错误信息应携带技能 ID: %v", err)
	}
}

func TestResolveRunSkillsCapacityExceeded(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	// 两个 300KB 的技能合计 600KB > 512KB 上下文预算。
	seedSelectionSkill(t, s, db, "big-1", 300*1024, false, 1)
	seedSelectionSkill(t, s, db, "big-2", 300*1024, false, 1)
	seedDefault(t, db, "big-1", 0)
	seedDefault(t, db, "big-2", 1)
	_, err := s.resolveRunSkills("user", "conv-f", nil, true)
	if err == nil {
		t.Fatal("超过上下文预算应被拒绝")
	}
	var appErr *AppError
	if !errorsAs(err, &appErr) {
		t.Fatalf("应返回 AppError: %v", err)
	}
	if appErr.Reason != "agent_skill_budget_exceeded" {
		t.Fatalf("reason = %q, want agent_skill_budget_exceeded", appErr.Reason)
	}
}

func TestResolveRunSkillsCrossAccountIsolation(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	seedSelectionSkillFor(t, s, db, "user", "d-1", 1024, false, 1)
	seedDefault(t, db, "d-1", 0)
	if _, err := s.resolveRunSkills("user", "conv-user-a", []string{"d-1"}, true); err != nil {
		t.Fatal(err)
	}
	// user B 的会话解析不到 user A 的会话行（conversationID 不同，天然隔离）；
	// 这里验证 user B 的新会话不继承 A 的集合。
	if _, err := s.resolveRunSkills("user-b", "conv-user-a", nil, true); err != nil {
		t.Fatalf("user B 会话解析不应读到 user A 的默认注入: %v", err)
	}
}

func TestResolveRunSkillsLegacyConversationNoDefaults(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	seedSelectionSkill(t, s, db, "d-1", 1024, false, 1)
	seedSelectionSkillFor(t, s, db, "user", "own-legacy", 1024, true, 1)
	seedDefault(t, db, "d-1", 0)
	// 迁移前的旧会话：有既有运行但会话技能表没有行，续聊不得注入默认。
	if _, err := s.resolveRunSkills("user", "conv-legacy", []string{"own-legacy"}, false); err != nil {
		t.Fatal(err)
	}
	rows := conversationRows(t, db, "conv-legacy")
	if got := skillIDs(conversationItems(rows)); got != "own-legacy" {
		t.Fatalf("旧会话被误注入默认技能: %q", got)
	}
}

func TestCloudAgentConversationIDFor(t *testing.T) {
	s, db := newRunSkillSelectionService(t)
	if got := s.cloudAgentConversationIDFor("user", "run-1", ""); got != "run-1" {
		t.Fatalf("新会话 ID = %q, want run-1", got)
	}
	parent := &model.CloudAgentExecution{ID: "run-parent", UserID: "user", Status: "completed", Engine: "pi", ConversationID: "conv-parent"}
	if err := db.Create(parent).Error; err != nil {
		t.Fatal(err)
	}
	if got := s.cloudAgentConversationIDFor("user", "run-2", "run-parent"); got != "conv-parent" {
		t.Fatalf("续聊会话 ID = %q, want conv-parent", got)
	}
	if got := s.cloudAgentConversationIDFor("user", "run-3", "run-missing"); got != "run-missing" {
		t.Fatalf("父轮缺失时回退 parentID: %q", got)
	}
}
