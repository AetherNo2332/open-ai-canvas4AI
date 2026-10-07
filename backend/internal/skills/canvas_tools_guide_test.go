package skills

import (
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// References are read lazily by the worker at the version frozen for its run.
func TestCanvasToolsGuideFrozenPackageReads(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "guide.db")), &gorm.Config{})
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
	const id = "yingce-canvas-tools-guide"
	var skill model.Skill
	if err := db.First(&skill, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	var version model.SkillVersion
	if err := db.First(&version, "id = ?", skill.CurrentVersionID).Error; err != nil {
		t.Fatal(err)
	}
	files := []string{"SKILL.md", "references/structure-and-connections.md", "references/generation.md", "references/recovery.md"}
	if skill.FileCount != len(files) {
		t.Fatalf("guide file count = %d, want %d", skill.FileCount, len(files))
	}
	for _, name := range files {
		page, err := svc.GlobalSkillPackageFileAtVersion(id, version.ID, version.ContentHash, name)
		if err != nil {
			t.Fatalf("frozen read %s: %v", name, err)
		}
		embedded, err := builtinSkillFiles.ReadFile("canvas-tools-guide/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if page.Binary || page.Content != string(embedded) {
			t.Fatalf("frozen read changed guide file %s", name)
		}
	}
}
