package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// 画布每次保存都会经投影写入 canvas_command_resources(kind=current)。彻底删除不受引用拦截
// （产品约定），但必须同事务解除这些索引，否则物理删除检查永远命中、删除任务无限重试。
func projectedCanvasPurgeFixture(t *testing.T) (*Service, *gorm.DB, string) {
	t.Helper()
	svc, db, dataDir := newResourceDeletionTestService(t)
	objectKey := "users/user-1/image/projected.png"
	path := filepath.Join(dataDir, "resources", filepath.FromSlash(objectKey))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("image"), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Resource{ID: "r1", UserID: "user-1", Provider: "local", ObjectKey: objectKey, Status: model.ResourceStatusReady},
		&model.Asset{ID: "asset-1", UserID: "user-1", Title: "素材", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{"data":{"storageKey":"resource:r1"}}`},
		&model.CanvasTaskResource{TaskID: "task-1", ResourceID: "r1", OwnerID: "user-1"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	canvas := model.CanvasProject{ID: "canvas-1", UserID: "user-1", Title: "使用中的画布", PayloadJSON: `{"nodes":[{"id":"n1","type":"image","metadata":{"storageKey":"resource:r1"}}],"connections":[]}`}
	if err := svc.repo.UpsertCanvasProject(&canvas); err != nil {
		t.Fatal(err)
	}
	var current int64
	if err := db.Model(&model.CanvasCommandResource{}).Where("resource_id = ? AND kind = ?", "r1", "current").Count(&current).Error; err != nil || current != 1 {
		t.Fatalf("projected save must index the current resource: count=%d err=%v", current, err)
	}
	return svc, db, path
}

func TestPurgeProjectedCanvasResourceReleasesCommandIndexes(t *testing.T) {
	svc, db, path := projectedCanvasPurgeFixture(t)
	resource := model.Resource{}
	if err := db.First(&resource, "id = ?", "r1").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.PurgeUserAsset("user-1", "asset-1"); err != nil {
		t.Fatalf("purge must not be blocked by canvas references: %v", err)
	}
	for _, check := range []struct {
		model any
		where string
	}{
		{&model.CanvasCommandResource{}, "resource_id = ?"},
		{&model.CanvasTaskResource{}, "resource_id = ?"},
		{&model.Resource{}, "id = ?"},
	} {
		var count int64
		if err := db.Model(check.model).Where(check.where, "r1").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%T rows remain for purged resource: %d %v", check.model, count, err)
		}
	}
	protected, err := svc.repo.CanvasHistoryReferencesObject(&resource)
	if err != nil || protected {
		t.Fatalf("physical deletion check still blocked: %v %v", protected, err)
	}
	svc.drainResourceDeletionJobs(1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var jobs int64
		db.Model(&model.ResourceDeletionJob{}).Count(&jobs)
		_, statErr := os.Stat(path)
		if jobs == 0 && os.IsNotExist(statErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deletion job did not complete: jobs=%d stat=%v", jobs, statErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDeleteProjectedCanvasResourceNamesCurrentContent(t *testing.T) {
	svc, db, path := projectedCanvasPurgeFixture(t)
	err := svc.DeleteUserAsset("user-1", "asset-1")
	if err == nil || !strings.Contains(err.Error(), "画布当前内容") {
		t.Fatalf("normal delete must refuse and name the current canvas content: %v", err)
	}
	var rows int64
	db.Model(&model.CanvasCommandResource{}).Where("resource_id = ?", "r1").Count(&rows)
	if rows == 0 {
		t.Fatal("refused delete removed command indexes")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("refused delete touched the object: %v", statErr)
	}
}
