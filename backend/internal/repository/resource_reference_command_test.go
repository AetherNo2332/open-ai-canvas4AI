package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"yingce/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Agent 画布命令历史（canvas_command_resources）持有撤销所需的资源；物理删除检查已经认它，
// 删除前的引用快照与事务内复查也必须认，否则资源记录被删而对象永远删不掉。
func commandResourceTestDB(t *testing.T) (*gorm.DB, *Repository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "command-resources.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&model.Resource{}, &model.Asset{}, &model.AssetVersion{}, &model.AssetRepresentation{},
		&model.CanvasProject{}, &model.CanvasSnapshot{}, &model.CanvasSnapshotResource{},
		&model.CanvasCommandResource{}, &model.CanvasTaskResource{}, &model.CloudAgentResourceLease{},
		&model.Tool{}, &model.WorkbenchTool{}, &model.Task{}, &model.TaskLog{}, &model.Result{},
		&model.CreationRun{}, &model.CreationSubmission{}, &model.Project{}, &model.StyleProfile{},
		&model.ProjectAssetCandidate{}, &model.WorkflowInstance{}, &model.WorkflowStepInstance{},
		&model.Shot{}, &model.ShotArtifact{}, &model.VoiceProfile{}, &model.Announcement{},
		&model.AnnouncementImageDraft{}, &model.ArkPrivateAssetBinding{}, &model.ResourceDeletionJob{},
	); err != nil {
		t.Fatal(err)
	}
	return db, New(db)
}

func TestResourceReferenceSnapshotIncludesCommandResources(t *testing.T) {
	db, repo := commandResourceTestDB(t)
	for _, row := range []any{
		&model.CanvasProject{ID: "canvas-1", UserID: "user", Title: "分镜画布", PayloadJSON: `{}`},
		&model.CanvasCommandResource{OwnerID: "user", CanvasID: "canvas-1", Epoch: "e1", OperationID: "op-1", Kind: "history", ResourceID: "r1"},
		&model.CanvasCommandResource{OwnerID: "user", CanvasID: "canvas-1", Epoch: "e1", OperationID: "", Kind: "current", ResourceID: "r2"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := repo.ResourceReferenceSnapshotExcludingAssets("user", nil, []string{"r1", "r2"})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]ResourceDirectReference{}
	for _, ref := range snapshot.Direct {
		kinds[ref.ResourceID] = ref
	}
	if ref := kinds["r1"]; ref.Kind != "Agent 画布历史" || ref.ID != "canvas-1" || ref.Title != "分镜画布" {
		t.Fatalf("history row must be labelled Agent 画布历史: %#v", snapshot.Direct)
	}
	if ref := kinds["r2"]; ref.Kind != "画布当前内容" || ref.ID != "canvas-1" {
		t.Fatalf("current row must be labelled 画布当前内容: %#v", snapshot.Direct)
	}
}

func TestResourceReferenceSnapshotHidesOtherOwnersCanvas(t *testing.T) {
	db, repo := commandResourceTestDB(t)
	for _, row := range []any{
		&model.CanvasProject{ID: "canvas-other", UserID: "other", Title: "别人的画布", PayloadJSON: `{}`},
		&model.CanvasCommandResource{OwnerID: "other", CanvasID: "canvas-other", Epoch: "e1", OperationID: "op-1", Kind: "history", ResourceID: "r1"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := repo.ResourceReferenceSnapshotExcludingAssets("user", nil, []string{"r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Direct) != 1 || snapshot.Direct[0].ResourceID != "r1" {
		t.Fatalf("other owner's command reference must still block deletion: %#v", snapshot.Direct)
	}
	if ref := snapshot.Direct[0]; ref.ID != "" || ref.Title == "别人的画布" {
		t.Fatalf("other owner's canvas id/title leaked: %#v", ref)
	}
}

func TestResourceReferenceSnapshotLabelsOwnDeletedCanvas(t *testing.T) {
	db, repo := commandResourceTestDB(t)
	if err := db.Create(&model.CanvasCommandResource{OwnerID: "user", CanvasID: "gone", Epoch: "e1", OperationID: "op-1", Kind: "history", ResourceID: "r1"}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.ResourceReferenceSnapshotExcludingAssets("user", nil, []string{"r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Direct) != 1 || snapshot.Direct[0].ID != "gone" || snapshot.Direct[0].Title != "已删除的画布" {
		t.Fatalf("own deleted canvas must keep its id and be labelled deleted: %#v", snapshot.Direct)
	}
}

func TestDeleteDetachedResourcesKeepsCommandReferenced(t *testing.T) {
	db, repo := commandResourceTestDB(t)
	resource := model.Resource{ID: "r1", UserID: "user", Provider: "local", ObjectKey: "users/user/r1.png", Status: model.ResourceStatusReady}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasCommandResource{OwnerID: "user", CanvasID: "canvas-1", Epoch: "e1", OperationID: "op-1", Kind: "history", ResourceID: "r1"}).Error; err != nil {
		t.Fatal(err)
	}
	job := model.ResourceDeletionJob{ID: "job-1", UserID: "user", ResourceID: "r1", Provider: "local", ObjectKey: resource.ObjectKey, Status: model.ResourceDeletionStatusPending}
	err := repo.DeleteDetachedResources([]model.Resource{resource}, []model.ResourceDeletionJob{job})
	if !errors.Is(err, ErrResourceCleanupStillReferenced) {
		t.Fatalf("DeleteDetachedResources() = %v, want still referenced", err)
	}
	var resources, jobs int64
	db.Model(&model.Resource{}).Count(&resources)
	db.Model(&model.ResourceDeletionJob{}).Count(&jobs)
	if resources != 1 || jobs != 0 {
		t.Fatalf("command-referenced resource was removed: resources=%d jobs=%d", resources, jobs)
	}
}

func TestRequireNoCanvasHistoryReferencesIncludesCommandResources(t *testing.T) {
	db, _ := commandResourceTestDB(t)
	if err := db.Create(&model.Resource{ID: "r1", UserID: "user", Status: model.ResourceStatusReady}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasCommandResource{OwnerID: "user", CanvasID: "canvas-1", Epoch: "e1", OperationID: "op-1", Kind: "history", ResourceID: "r1"}).Error; err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(func(tx *gorm.DB) error { return New(tx).RequireNoCanvasHistoryReferences([]string{"r1"}) })
	if !errors.Is(err, ErrCanvasHistoryResourceReferenced) {
		t.Fatalf("RequireNoCanvasHistoryReferences() = %v, want referenced", err)
	}
}
