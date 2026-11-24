package skills

import (
	"path/filepath"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 世界引擎说明书必须作为独立内置 Skill 版本冻结；主题参考由本轮受限 read 按需读取。
func TestPrevisWorldGuideFrozenPackageReads(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "previs-guide.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Skill{}, &model.SkillVersion{}, &model.SkillFile{}, &model.BuiltinSkillTombstone{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir(), nil)
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	const id = "yingce-previs-world-guide"
	var skill model.Skill
	if err := db.First(&skill, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	var version model.SkillVersion
	if err := db.First(&version, "id = ?", skill.CurrentVersionID).Error; err != nil {
		t.Fatal(err)
	}
	files := []string{
		"SKILL.md",
		"references/rules.md",
		"references/setup.md",
		"references/actors.md",
		"references/move.md",
		"references/paths.md",
		"references/camera.md",
		"references/moves.md",
		"references/edit.md",
		"references/output.md",
		"references/recipes.md",
		"references/recipes_action.md",
		"references/recipes_epic.md",
		"references/shots.md",
	}
	if skill.FileCount != len(files) {
		t.Fatalf("guide file count = %d, want %d", skill.FileCount, len(files))
	}
	for _, name := range files {
		page, err := svc.GlobalSkillPackageFileAtVersion(id, version.ID, version.ContentHash, name)
		if err != nil {
			t.Fatalf("frozen read %s: %v", name, err)
		}
		embedded, err := builtinSkillFiles.ReadFile("previs-world-guide/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if page.Binary || page.Content != string(embedded) {
			t.Fatalf("frozen read changed guide file %s", name)
		}
	}
}
