package skills

import (
	"errors"
	"testing"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSkillDetailReturnsNotFoundForMissingSkill(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Skill{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir(), nil)

	_, err = svc.SkillDetail("user", "missing-skill")
	if err == nil {
		t.Fatal("expected missing skill error")
	}
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected structured application error, got %T: %v", err, err)
	}
	if appErr.Status != 404 {
		t.Fatalf("status = %d, want 404 (error: %v)", appErr.Status, err)
	}
}
