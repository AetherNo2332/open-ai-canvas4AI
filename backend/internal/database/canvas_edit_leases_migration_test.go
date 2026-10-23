package database

import (
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestCanvasEditLeaseForwardMigrationFrom62(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.CanvasProject{ID: "keep", UserID: "owner", PayloadJSON: `{"nodes":[]}`, Revision: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Migrator().DropTable(&model.CanvasNodeEditLease{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("DELETE FROM schema_migrations WHERE version > 62").Error; err != nil {
		t.Fatal(err)
	}
	if err = RequireSchemaVersion(db); err == nil {
		t.Fatal("missing migration accepted")
	}
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&model.CanvasNodeEditLease{}) {
		t.Fatal("forward migration did not create lease table")
	}
	if err = RequireSchemaVersion(db); err != nil {
		t.Fatal(err)
	}
	var row model.CanvasProject
	if err = db.First(&row, "id = ?", "keep").Error; err != nil || row.Revision != 7 {
		t.Fatalf("document lost on upgrade: %+v %v", row, err)
	}
}
