package database

import (
	"testing"
	"yingce/backend/internal/model"
)

func TestCanvasMediaVersionMigrationPreservesExistingData(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:media-version-upgrade?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.Task{ID: "preserved", UserID: "owner", Prompt: "original", Status: model.TaskStatusSucceeded}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Migrator().DropTable(&model.CanvasMediaVersion{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("version = ?", 66).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&model.CanvasMediaVersion{}) || !db.Migrator().HasIndex(&model.CanvasMediaVersion{}, "idx_canvas_media_sequence") {
		t.Fatal("version table or scope uniqueness missing")
	}
	var old model.Task
	if err = db.First(&old, "id = ?", "preserved").Error; err != nil || old.Prompt != "original" || old.Status != model.TaskStatusSucceeded {
		t.Fatalf("migration changed task: %+v %v", old, err)
	}
}
