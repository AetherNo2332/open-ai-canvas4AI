package app

import (
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

func TestPurgeAssetsBatchSharedResourcesAndHistory(t *testing.T) {
	svc, db, _ := newResourceDeletionTestService(t)
	for _, id := range []string{"batch-shared", "keep-shared", "same-object", "foreign-alias"} {
		owner, objectKey := "user-1", id+".png"
		if id == "foreign-alias" {
			owner, objectKey = "user-2", "same-object.png"
		}
		if err := db.Create(&model.Resource{ID: id, UserID: owner, Provider: "unsupported-test-provider", ObjectKey: objectKey, Status: model.ResourceStatusReady}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []any{
		&model.Asset{ID: "first", UserID: "user-1", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{"resourceIds":["batch-shared","keep-shared","same-object"]}`},
		&model.Asset{ID: "second", UserID: "user-1", Status: model.AssetVersionStatusArchived, PayloadJSON: `{}`},
		&model.AssetVersion{ID: "second-version", AssetID: "second", DefinitionJSON: `{"resourceId":"batch-shared"}`},
		&model.AssetRepresentation{ID: "second-representation", AssetVersionID: "second-version", Role: "image", ResourceID: "batch-shared", MetadataJSON: `{}`},
		&model.Asset{ID: "unselected", UserID: "user-1", PayloadJSON: `{}`},
		&model.AssetVersion{ID: "unselected-version", AssetID: "unselected", DefinitionJSON: `{}`},
		&model.AssetRepresentation{ID: "unselected-representation", AssetVersionID: "unselected-version", Role: "video", MetadataJSON: `{"resourceId":"keep-shared"}`},
		&model.Task{ID: "batch-task", UserID: "user-1", Status: model.TaskStatusRunning, InputJSON: `{"resourceId":"batch-shared"}`},
		&model.CanvasProject{ID: "batch-canvas", UserID: "user-1", PayloadJSON: `{"resourceId":"batch-shared"}`},
		&model.CanvasSnapshot{ID: "batch-snapshot", CanvasID: "batch-canvas", UserID: "user-1", Revision: 1, PayloadJSON: `{}`, CreatedAt: time.Now()},
		&model.CanvasSnapshotResource{SnapshotID: "batch-snapshot", ResourceID: "batch-shared"},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 上游 5b382726 起，purge 不再绕过引用校验：仍被运行中任务 / 画布 / 画布历史版本引用的素材
	// 一律拒绝删除（同一改动把上游自己的同类用例改名为 ...PreservesTaskReferences）。先断言这条保护，
	// 再解除这些业务引用，继续验证"批量 purge + 共享资源 + 事务回滚"这条原路径。
	if err := svc.PurgeUserAssets("user-1", []string{"first", "second"}); err == nil ||
		!strings.Contains(err.Error(), "任务") || !strings.Contains(err.Error(), "画布") {
		t.Fatalf("purge 应拒绝仍被任务与画布引用的素材: %v", err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", "batch-task").Update("input_json", "{}").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "batch-canvas").Update("payload_json", "{}").Error; err != nil {
		t.Fatal(err)
	}
	// 画布历史版本的引用同样受保护（上面的拒绝信息已覆盖），解除后批量 purge 才可执行。
	if err := db.Where("snapshot_id = ? AND resource_id = ?", "batch-snapshot", "batch-shared").
		Delete(&model.CanvasSnapshotResource{}).Error; err != nil {
		t.Fatal(err)
	}
	// Force an error late in the resource transaction, after asset and outbox writes.
	if err := db.Exec("CREATE TRIGGER fail_batch_resource_delete BEFORE DELETE ON resources BEGIN SELECT RAISE(ABORT, 'forced failure'); END;").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.PurgeUserAssets("user-1", []string{"first", "second"}); err == nil {
		t.Fatal("expected rollback")
	}
	assertCount := func(record any, want int64) {
		t.Helper()
		var count int64
		if err := db.Model(record).Count(&count).Error; err != nil || count != want {
			t.Fatalf("%T count=%d want=%d err=%v", record, count, want, err)
		}
	}
	assertCount(&model.Asset{}, 3)
	assertCount(&model.Resource{}, 4)
	assertCount(&model.ResourceDeletionJob{}, 0)
	// 画布历史版本那条引用已在上面解除（拒绝信息已覆盖它的保护），所以这里是 0。
	assertCount(&model.CanvasSnapshotResource{}, 0)
	assertCount(&model.AssetVersion{}, 2)
	assertCount(&model.AssetRepresentation{}, 2)
	if err := db.Exec("DROP TRIGGER fail_batch_resource_delete").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.PurgeUserAssets("user-1", []string{"first", " second ", "first"}); err != nil {
		t.Fatal(err)
	}
	// 新引用语义（上游 5b382726 的 `ResourceReferenceSnapshotExcludingAssets`）只按"本次删除之外的
	// 引用"判断保护范围：`keep-shared` 与 `same-object` 仅被本次要删的素材（含它们的未选中版本/表现）
	// 引用，因此随素材一并移除；只有另一个用户的 `foreign-alias` 留下。
	assertCount(&model.Asset{}, 1)
	assertCount(&model.Resource{}, 1)
	// 少了"被其他素材引用"这层保护后，随素材移除的物理对象从一个变成两个，删表也相应多一条。
	assertCount(&model.ResourceDeletionJob{}, 2)
	assertCount(&model.CanvasSnapshotResource{}, 0)
	assertCount(&model.CanvasSnapshot{}, 1)
	assertCount(&model.Task{}, 1)
	assertCount(&model.CanvasProject{}, 1)
	assertCount(&model.AssetVersion{}, 1)
	assertCount(&model.AssetRepresentation{}, 1)
	var sharedJob int64
	if err := db.Model(&model.ResourceDeletionJob{}).Where("resource_id = ?", "batch-shared").Count(&sharedJob).Error; err != nil {
		t.Fatal(err)
	}
	if sharedJob != 1 {
		t.Fatalf("batch-shared 的物理删除任务数 = %d, want 1", sharedJob)
	}
	for _, id := range []string{"foreign-alias"} {
		var resource model.Resource
		if err := db.First(&resource, "id = ?", id).Error; err != nil {
			t.Fatalf("shared resource %s lost: %v", id, err)
		}
	}
}
