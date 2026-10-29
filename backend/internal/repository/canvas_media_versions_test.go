package repository

import (
	"testing"

	"yingce/backend/internal/model"
)

const versionedCanvas = `{"nodes":[{"id":"hero","type":"image","title":"主角","metadata":{"storageKey":"resource:current","mediaVersions":[{"id":"v1","number":1,"createdAt":"x","source":"generate","media":{"storageKey":"resource:old-version","content":"/api/resources/old-version/file"}},{"id":"v2","number":2,"createdAt":"x","source":"generate","media":{"storageKey":"resource:current"}}],"currentMediaVersionId":"v2"}}]}`

// 版本历史在节点 metadata 中：受保护删除要报告持有资源的画布节点；物理删除检查不扫描画布正文，
// 彻底删除后对象删除任务可以完成（断链由画布修复处理，与节点当前媒体同一规则）。
func TestMediaVersionReferencesScanNodeMetadata(t *testing.T) {
	db, repo := commandResourceTestDB(t)
	for _, row := range []any{
		&model.CanvasProject{ID: "canvas-1", UserID: "user", Title: "分镜画布", PayloadJSON: versionedCanvas},
		&model.CanvasProject{ID: "canvas-2", UserID: "other", Title: "他人画布", PayloadJSON: versionedCanvas},
		&model.Resource{ID: "old-version", UserID: "user", ObjectKey: "u/old-version.png", Status: model.ResourceStatusReady},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	refs, err := repo.MediaVersionResourceReferences("user", []string{"old-version", "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ResourceID != "old-version" || refs[0].ID != "canvas-1:hero" || refs[0].Kind != "画布媒体版本" || refs[0].Title != "分镜画布 / 主角" {
		t.Fatalf("version references: %#v", refs)
	}
	snapshot, err := repo.ResourceReferenceSnapshotExcludingAssets("user", nil, []string{"old-version"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range snapshot.Direct {
		found = found || ref.Kind == "画布媒体版本" && ref.ResourceID == "old-version"
	}
	if !found {
		t.Fatalf("guarded deletion must see the version reference: %#v", snapshot.Direct)
	}
	resource := &model.Resource{ID: "old-version", UserID: "user", ObjectKey: "u/old-version.png"}
	protected, err := repo.CanvasHistoryReferencesObject(resource)
	if err != nil || protected {
		t.Fatalf("purged version media must not keep the deletion job retrying: %v %v", protected, err)
	}
}
