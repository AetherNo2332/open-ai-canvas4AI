package canvas

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

func shotMigrationSeed(t *testing.T, svc *Service, id string, revision int64) {
	t.Helper()
	payload := `{"nodes":[
		{"id":"asset-1","type":"image","title":"参考图","position":{"x":0,"y":0},"width":720,"height":405,"metadata":{"assetId":"a1","storageKey":"resource:r1"}},
		{"id":"hero","type":"text","title":"阿岚","position":{"x":0,"y":420},"width":340,"height":240,"metadata":{"workflowKind":"character","characterAssetId":"c1"}},
		{"id":"board","type":"script","title":"分镜","position":{"x":400,"y":0},"width":920,"height":360,"metadata":{"storyboard":{"rows":[
			{"id":"row-1","shotNumber":1,"durationSeconds":4,"plotDescription":"动作","dialogue":"阿岚：走吧","imageGenerationPrompt":"雨夜天台","videoMotionPrompt":"缓慢推近","assetBindings":[{"nodeId":"asset-1","role":"style","priority":80}],"characters":[{"characterAssetId":"c1"}]},
			{"id":"row-2","shotNumber":2,"imageNodeId":"old-image","videoNodeId":"old-video","locked":true}
		]}}},
		{"id":"old-image","type":"image","title":"已出图","position":{"x":1400,"y":0},"width":720,"height":405,"metadata":{}},
		{"id":"old-video","type":"video","title":"已出片","position":{"x":1400,"y":420},"width":720,"height":405,"metadata":{}}
	],"connections":[]}`
	if err := svc.repo.Create(&model.Resource{ID: "r1", UserID: "user", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "r1.png", MimeType: "image/png", Size: 32}); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.Create(&model.Asset{ID: "a1", UserID: "user", Kind: "image", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{"id":"a1","kind":"image","storageKey":"resource:r1"}`}); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.Create(&model.CanvasProject{ID: id, UserID: "user", Title: "旧分镜", PayloadJSON: payload, Revision: revision}); err != nil {
		t.Fatal(err)
	}
}

func TestShotMigrationSwitchesRowsAndRestoresPreimage(t *testing.T) {
	svc := newCanvasOperationTestService(t)
	shotMigrationSeed(t, svc, "legacy", 3)
	preview, err := svc.PreviewCanvasShotMigration("user", "legacy")
	if err != nil || !preview.Required || preview.Rows != 2 || preview.ShotSubmaps != 2 || len(preview.ActiveTasks) != 0 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	stale := preview.Revision + 1
	if _, err := svc.MigrateCanvasShots("user", "legacy", CanvasShotMigrationRequest{Revision: &stale}); err == nil {
		t.Fatal("stale revision was accepted")
	}
	result, err := svc.MigrateCanvasShots("user", "legacy", CanvasShotMigrationRequest{Revision: &preview.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 2 || result.Submaps != 2 || result.ExistingImages != 1 || result.ExistingVideos != 1 || result.Project.Revision != 4 {
		t.Fatalf("migration result = %+v", result)
	}
	afterMigration, err := svc.PreviewCanvasShotMigration("user", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if afterMigration.Required || afterMigration.Rollback == nil || !afterMigration.Rollback.CanRestore || afterMigration.Rollback.CanCopy ||
		afterMigration.Rollback.SnapshotRevision != 3 || afterMigration.Rollback.MigrationRevision != 4 {
		t.Fatalf("rollback preview = %+v, %v", afterMigration, err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(project.PayloadJSON), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["referenceFieldsVersion"] != float64(1) {
		t.Fatalf("referenceFieldsVersion = %#v", doc["referenceFieldsVersion"])
	}
	board := canvasObject(canvasObject(nodeByID(doc)["board"]["metadata"])["storyboard"])
	if rows := canvasMaps(board["rows"]); len(rows) != 0 {
		t.Fatalf("rows not consumed: %v", rows)
	}
	nodes := nodeByID(doc)
	if nodes["submap-1"]["type"] != "submap" || nodes["submap-2"]["type"] != "submap" {
		t.Fatalf("shot submaps missing: %v", nodes)
	}
	video := canvasObject(nodes["submap-1__video-1"]["metadata"])
	if canvasText(video["videoStartFrameNodeId"]) != "submap-1__image-1" || canvasText(video["seconds"]) != "4" {
		t.Fatalf("video first frame/duration lost: %#v", video)
	}
	if nodes["old-image"]["scopeId"] != nil || nodes["old-video"]["scopeId"] != nil {
		t.Fatal("existing result nodes must not move into the submap")
	}
	if _, err := svc.MigrateCanvasShots("user", "legacy", CanvasShotMigrationRequest{Revision: &project.Revision}); referenceMigrationReason(err) != "shot_migration_not_required" {
		t.Fatalf("repeat migration error = %v", err)
	}
	snapshot, err := svc.repo.LatestCanvasSnapshotWithReason("user", "legacy", shotMigrationSnapshotReason)
	if err != nil || snapshot.Revision != 3 {
		t.Fatalf("migration preimage = %+v, %v", snapshot, err)
	}
	rollback, err := svc.RollbackCanvasShots("user", "legacy", CanvasShotMigrationRollbackRequest{Revision: &project.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if !rollback.Restored || rollback.Revision != 5 || rollback.SnapshotID != snapshot.ID {
		t.Fatalf("rollback = %+v", rollback)
	}
	restored, err := svc.repo.CanvasProjectForUser("user", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	var restoredDoc map[string]any
	if err := json.Unmarshal([]byte(restored.PayloadJSON), &restoredDoc); err != nil {
		t.Fatal(err)
	}
	restoredBoard := canvasObject(canvasObject(nodeByID(restoredDoc)["board"]["metadata"])["storyboard"])
	if rows := canvasMaps(restoredBoard["rows"]); len(rows) != 2 {
		t.Fatalf("restored rows = %d, want 2", len(rows))
	}
	if nodeByID(restoredDoc)["submap-1"] != nil {
		t.Fatal("restored preimage still contains generated submaps")
	}
	afterRestore, err := svc.PreviewCanvasShotMigration("user", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !afterRestore.Required || afterRestore.Rollback != nil {
		t.Fatalf("restored preview = %+v, %v", afterRestore, err)
	}
	if _, err := svc.RollbackCanvasShots("user", "legacy", CanvasShotMigrationRollbackRequest{Revision: &afterRestore.Revision}); referenceMigrationReason(err) != "shot_migration_rollback_not_applicable" {
		t.Fatalf("repeated rollback error = %v", err)
	}
}

func TestShotMigrationBlocksActiveTasksAndPreservesNewerEdits(t *testing.T) {
	svc := newCanvasOperationTestService(t)
	shotMigrationSeed(t, svc, "busy", 2)
	if err := svc.repo.Create(&model.Task{ID: "active-task", UserID: "user", ProjectID: "busy", Status: model.TaskStatusRunning}); err != nil {
		t.Fatal(err)
	}
	revision := int64(2)
	if _, err := svc.MigrateCanvasShots("user", "busy", CanvasShotMigrationRequest{Revision: &revision}); referenceMigrationReason(err) != "shot_migration_tasks_active" {
		t.Fatalf("active task migration error = %v", err)
	}
	if err := svc.repo.Delete(&model.Task{ID: "active-task"}, "id = ?", "active-task"); err != nil {
		t.Fatal(err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "busy")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.MigrateCanvasShots("user", "busy", CanvasShotMigrationRequest{Revision: &project.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.Create(&model.Task{ID: "rollback-active-task", UserID: "user", ProjectID: "busy", Status: model.TaskStatusRunning}); err != nil {
		t.Fatal(err)
	}
	rollbackRevision := result.Project.Revision
	if _, err := svc.RollbackCanvasShots("user", "busy", CanvasShotMigrationRollbackRequest{Revision: &rollbackRevision}); referenceMigrationReason(err) != "shot_migration_tasks_active" {
		t.Fatalf("active task rollback error = %v", err)
	}
	if err := svc.repo.Delete(&model.Task{ID: "rollback-active-task"}, "id = ?", "rollback-active-task"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "busy", CanvasOperationsRequest{MutationID: "newer-edit", ClientCapabilities: []string{CanvasClientCapabilityReferenceFields}, Operations: []CanvasOperation{{
		Type: "patch_node", ID: "submap-1", Patch: map[string]json.RawMessage{"title": json.RawMessage(`"镜头 1 新标题"`)},
		Before: map[string]json.RawMessage{"title": json.RawMessage(`"镜头 1"`)},
	}}}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.repo.CanvasProjectForUser("user", "busy")
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != result.Project.Revision+1 {
		t.Fatalf("newer edit revision = %d, want %d", current.Revision, result.Project.Revision+1)
	}
	newerPreview, err := svc.PreviewCanvasShotMigration("user", "busy")
	if err != nil {
		t.Fatal(err)
	}
	if newerPreview.Rollback == nil || newerPreview.Rollback.CanRestore || !newerPreview.Rollback.CanCopy || !newerPreview.Rollback.NewerEdits {
		t.Fatalf("newer edit rollback preview = %+v, %v", newerPreview, err)
	}
	if _, err := svc.RollbackCanvasShots("user", "busy", CanvasShotMigrationRollbackRequest{Revision: &current.Revision}); referenceMigrationReason(err) != "shot_migration_has_newer_edits" {
		t.Fatalf("newer edit rollback error = %v", err)
	}
	copied, err := svc.RollbackCanvasShots("user", "busy", CanvasShotMigrationRollbackRequest{Revision: &current.Revision, Strategy: "new_canvas"})
	if err != nil {
		t.Fatal(err)
	}
	if copied.NewCanvasID == "" {
		t.Fatalf("new canvas copy missing: %+v", copied)
	}
	kept, err := svc.repo.CanvasProjectForUser("user", "busy")
	if err != nil || kept.Revision != current.Revision {
		t.Fatalf("current canvas changed during new_canvas rollback: revision=%d err=%v", kept.Revision, err)
	}
	copyProject, err := svc.repo.CanvasProjectForUser("user", copied.NewCanvasID)
	if err != nil {
		t.Fatal(err)
	}
	var copyDoc map[string]any
	if err := json.Unmarshal([]byte(copyProject.PayloadJSON), &copyDoc); err != nil {
		t.Fatal(err)
	}
	copyBoard := canvasObject(canvasObject(nodeByID(copyDoc)["board"]["metadata"])["storyboard"])
	if rows := canvasMaps(copyBoard["rows"]); len(rows) != 2 {
		t.Fatalf("copied preimage rows = %d, want 2", len(rows))
	}
}

func TestShotMigrationCapturesPreimageAfterRecentAutomaticSnapshot(t *testing.T) {
	svc := newCanvasOperationTestService(t)
	shotMigrationSeed(t, svc, "recent-history", 3)
	if _, err := svc.ApplyUserCanvasOperations("user", "recent-history", CanvasOperationsRequest{
		MutationID: "recent-edit",
		Operations: []CanvasOperation{{
			Type: "patch_node", ID: "old-image",
			Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"已出图 2"`)},
			Before: map[string]json.RawMessage{"title": json.RawMessage(`"已出图"`)},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	snapshots, err := svc.repo.CanvasSnapshots("user", "recent-history", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].Reason != "automatic" || snapshots[0].Revision != 3 {
		t.Fatalf("recent automatic snapshots = %+v", snapshots)
	}
	preview, err := svc.PreviewCanvasShotMigration("user", "recent-history")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Required || preview.Revision != 4 {
		t.Fatalf("preview = %+v", preview)
	}
	result, err := svc.MigrateCanvasShots("user", "recent-history", CanvasShotMigrationRequest{Revision: &preview.Revision})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.repo.LatestCanvasSnapshotWithReason("user", "recent-history", shotMigrationSnapshotReason)
	if err != nil || snapshot.Revision != preview.Revision {
		t.Fatalf("migration preimage = %+v, %v; want revision %d", snapshot, err, preview.Revision)
	}
	rollback, err := svc.PreviewCanvasShotMigration("user", "recent-history")
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Rollback == nil || !rollback.Rollback.CanRestore || result.Project.Revision != preview.Revision+1 {
		t.Fatalf("rollback preview = %+v, result = %+v", rollback, result)
	}
}

func nodeByID(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, node := range canvasMaps(doc["nodes"]) {
		out[canvasText(node["id"])] = node
	}
	return out
}
