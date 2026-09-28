package skills

import (
	"testing"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSkillPackageFileAtVersionDoesNotFallBackToCurrentVersion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserIdentity{}, &model.Skill{}, &model.SkillVersion{}, &model.SkillFile{}, &model.UserSkillState{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir(), nil)
	created, err := svc.createSingleMarkdownSkill("user", SkillMutationRequest{
		SkillName:   "版本测试",
		Description: "测试固定版本",
		Instruction: "---\nname: version-test\ndescription: old\n---\nold body",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstVersion := created.VersionID
	firstHash := created.ContentHash
	skill, err := svc.ownedSkill("user", created.SkillID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.updateSingleMarkdownSkill(skill, SkillMutationRequest{Instruction: "---\nname: version-test\ndescription: new\n---\nnew body"}); err != nil {
		t.Fatal(err)
	}

	old, err := svc.SkillPackageFileAtVersion("user", created.SkillID, firstVersion, firstHash, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if old.Content == "" || old.Content == "new body" || old.File.SHA256 == "" {
		t.Fatalf("fixed version returned unexpected content: %#v", old)
	}
	if _, err := svc.SkillPackageFileAtVersion("user", created.SkillID, firstVersion, "wrong-hash", "SKILL.md"); err == nil {
		t.Fatal("expected content hash mismatch to reject the read")
	}
}

func TestFrozenSkillReaderRejectsBinaryBytesWithTextSuffix(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil { t.Fatal(err) }
	if err := db.AutoMigrate(&model.User{}, &model.UserIdentity{}, &model.Skill{}, &model.SkillVersion{}, &model.SkillFile{}, &model.UserSkillState{}); err != nil { t.Fatal(err) }
	svc := New(repository.New(db), t.TempDir(), nil)
	for _, suffix := range []string{"md", "txt"} {
		for _, payload := range []string{"hello\x00world", "hello\xffworld", "hello\x01world", "hello\x7fworld"} {
			archive, err := archiveFromZip(skillZip(t, map[string]string{"SKILL.md":"---\nname: binary-test\ndescription: test\n---\nBody", "references/a."+suffix:payload}), "")
			if err != nil { t.Fatal(err) }
			created, err := svc.createSkillFromArchive("user", archive, SkillInstallRequest{IsPrivate:true}, "zip", "", "", "", "", false)
			if err != nil { t.Fatal(err) }
			page, err := svc.SkillPackageFileAtVersion("user", created.SkillID, created.VersionID, created.ContentHash, "references/a."+suffix)
			if err != nil { t.Fatal(err) }
			if !page.Binary || page.Content != "" { t.Errorf(".%s binary bytes exposed as text: %q", suffix, page.Content) }
		}
	}
}
