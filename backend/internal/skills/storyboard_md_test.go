package skills

import (
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 分镜表工具关闭时的 Markdown 分镜技能：随内置技能目录自动发现并可冻结读取。
func TestStoryboardMdFrozenPackageReads(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sb-md.db")), &gorm.Config{})
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
	const id = "yingce-storyboard-md"
	var skill model.Skill
	if err := db.First(&skill, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	var version model.SkillVersion
	if err := db.First(&version, "id = ?", skill.CurrentVersionID).Error; err != nil {
		t.Fatal(err)
	}
	files := []string{"SKILL.md"}
	if skill.FileCount != len(files) {
		t.Fatalf("guide file count = %d, want %d", skill.FileCount, len(files))
	}
	for _, name := range files {
		page, err := svc.GlobalSkillPackageFileAtVersion(id, version.ID, version.ContentHash, name)
		if err != nil {
			t.Fatalf("frozen read %s: %v", name, err)
		}
		embedded, err := builtinSkillFiles.ReadFile("storyboard-md/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if page.Binary || page.Content != string(embedded) {
			t.Fatalf("frozen read changed skill file %s", name)
		}
	}
	for _, name := range files {
		raw, err := builtinSkillFiles.ReadFile("storyboard-md/" + name)
		if err != nil {
			t.Fatal(err)
		}
		// 技能定位声明：触发词覆盖"分镜表关闭"场景，且明确不伪装结构化分镜。
		for _, keyword := range []string{"分镜表工具", "Markdown", "不要把 Markdown 节点伪装成分镜表", "continuityOut"} {
			if !strings.Contains(string(raw), keyword) {
				t.Fatalf("skill body missing keyword %s", keyword)
			}
		}
	}
}
