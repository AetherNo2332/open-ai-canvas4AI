package repository

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
)

func TestMediaVersionResourceRootsRetainHistoryWithoutPermanentOrphans(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "roots.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err = db.AutoMigrate(&model.CanvasProject{}, &model.CanvasSnapshot{}, &model.CanvasMediaVersion{}); err != nil {
		t.Fatal(err)
	}
	payload := `{"nodes":[{"id":"node","metadata":{"mediaVersion":{"epoch":"epoch","generation":1,"currentVersionId":"v2"}}}]}`
	if err = db.Create(&model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: payload}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []model.CanvasMediaVersion{
		{ID: "v1", OwnerID: "owner", CanvasID: "canvas", Epoch: "epoch", NodeID: "node", Generation: 1, Sequence: 1, ResourceID: "old", Status: "ready", MediaJSON: `{"storageKey":"resource:old"}`},
		{ID: "v-other-generation", OwnerID: "owner", CanvasID: "canvas", Epoch: "epoch", NodeID: "node", Generation: 2, Sequence: 1, ResourceID: "unrelated", Status: "ready", MediaJSON: `{"storageKey":"resource:unrelated"}`},
	} {
		if err = db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := New(db)
	read := func(owner string) []ResourceDirectReference {
		t.Helper()
		refs, e := repo.MediaVersionResourceReferences(owner, []string{"old", "unrelated"})
		if e != nil {
			t.Fatal(e)
		}
		return refs
	}
	if refs := read("owner"); len(refs) != 1 || refs[0].ResourceID != "old" {
		t.Fatalf("history version not protected or wrong generation retained: %+v", refs)
	}
	if refs := read("other"); len(refs) != 0 {
		t.Fatalf("foreign version exposed: %+v", refs)
	}
	if err = db.Model(&model.CanvasProject{}).Where("id = ?", "canvas").Update("payload_json", `{"nodes":[]}`).Error; err != nil {
		t.Fatal(err)
	}
	if refs := read("owner"); len(refs) != 0 {
		t.Fatalf("unreachable ledger became a permanent resource root: %+v", refs)
	}
	if err = db.Create(&model.CanvasSnapshot{ID: "snapshot", CanvasID: "canvas", UserID: "owner", Revision: 1, PayloadJSON: payload}).Error; err != nil {
		t.Fatal(err)
	}
	if refs := read("owner"); len(refs) != 1 || refs[0].ResourceID != "old" {
		t.Fatalf("recoverable family history lost: %+v", refs)
	}
}

func TestMediaVersionMetadataCountsAgainstTaskStorage(t *testing.T) {
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "quota.db")})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err = database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	before, err := repo.UserStorageUsage("owner")
	if err != nil {
		t.Fatal(err)
	}
	row := model.CanvasMediaVersion{ID: "counted", OwnerID: "owner", CanvasID: "canvas", Epoch: "epoch", NodeID: "node", Sequence: 1, RequestJSON: `{"prompt":"中文"}`, MediaJSON: `{"storageKey":"resource:old"}`, OutputsJSON: `[]`, Error: "失败"}
	if err = repo.InsertCanvasMediaVersion(&row); err != nil {
		t.Fatal(err)
	}
	after, err := repo.UserStorageUsage("owner")
	if err != nil {
		t.Fatal(err)
	}
	if after.TaskBytes-before.TaskBytes != mediaVersionBytes(&row) {
		t.Fatalf("version metadata escaped storage quota: %d => %d", before.TaskBytes, after.TaskBytes)
	}
}
