package canvas

import (
	"encoding/json"
	"errors"
	"testing"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func referenceMigrationReason(err error) string {
	var appErr *kernel.AppError
	if errors.As(err, &appErr) {
		return string(appErr.Reason)
	}
	return ""
}

func seedLegacyCanvas(t *testing.T, svc *Service, id, payload string) {
	t.Helper()
	if err := svc.repo.Create(&model.CanvasProject{ID: id, UserID: "user", Title: "旧画布", PayloadJSON: payload}); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceMigrationConvertsLegacyEdgesWithHistory(t *testing.T) {
	svc := newCanvasOperationTestService(t)
	seedLegacyCanvas(t, svc, "legacy", `{"nodes":[{"id":"img","type":"image","title":"参考图","metadata":{}},{"id":"clip","type":"video","title":"视频","metadata":{}}],"connections":[{"id":"old","fromNodeId":"img","toNodeId":"clip"}]}`)
	preview, err := svc.PreviewCanvasReferenceMigration("user", "legacy")
	if err != nil || !preview.Required || len(preview.Conflicts) != 0 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, err := svc.PreviewCanvasReferenceMigration("other", "legacy"); err == nil {
		t.Fatal("another user could preview the canvas")
	}
	stale := preview.Revision + 1
	if _, err := svc.MigrateCanvasReferences("user", "legacy", CanvasReferenceMigrationRequest{Revision: &stale}); err == nil {
		t.Fatal("stale revision was accepted")
	}
	if _, err := svc.MigrateCanvasReferences("user", "legacy", CanvasReferenceMigrationRequest{Revision: &preview.Revision}); err != nil {
		t.Fatal(err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(project.PayloadJSON), &saved); err != nil {
		t.Fatal(err)
	}
	if saved["referenceFieldsVersion"] != float64(1) {
		t.Fatalf("referenceFieldsVersion = %#v", saved["referenceFieldsVersion"])
	}
	history, err := svc.CanvasHistory("user", "legacy")
	if err != nil || len(history.Snapshots) == 0 {
		t.Fatalf("migration left no restorable snapshot: %+v %v", history, err)
	}
	if _, err := svc.MigrateCanvasReferences("user", "legacy", CanvasReferenceMigrationRequest{Revision: &project.Revision}); referenceMigrationReason(err) != "reference_migration_not_required" {
		t.Fatalf("second migration error = %v", err)
	}
}

func TestReferenceMigrationRequiresChoicesForConflicts(t *testing.T) {
	svc := newCanvasOperationTestService(t)
	seedLegacyCanvas(t, svc, "conflict", `{"nodes":[
		{"id":"hero","type":"text","metadata":{"workflowKind":"character","characterAssetId":"hero-asset"}},
		{"id":"scene","type":"image","metadata":{}},
		{"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":[],"rows":[{"id":"shot","assetBindings":[{"nodeId":"hero","role":"character","priority":100}],"imageNodeId":"old-product","videoNodeId":"old-clip"}]}}},
		{"id":"old-product","type":"image","metadata":{}},
		{"id":"old-clip","type":"video","metadata":{}},
		{"id":"new-product","type":"image","metadata":{}}],
		"connections":[{"id":"row-scene","fromNodeId":"scene","toNodeId":"board","toHandleId":"row:shot"},{"id":"output-new","fromNodeId":"board","toNodeId":"new-product","fromHandleId":"row:shot"}]}`)
	preview, err := svc.PreviewCanvasReferenceMigration("user", "conflict")
	if err != nil || len(preview.Conflicts) != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, err := svc.MigrateCanvasReferences("user", "conflict", CanvasReferenceMigrationRequest{Revision: &preview.Revision}); referenceMigrationReason(err) != "reference_migration_conflict" {
		t.Fatalf("migration without choices error = %v", err)
	}
	choices := map[string]string{preview.Conflicts[0].ScopeKey: "connections"}
	if _, err := svc.MigrateCanvasReferences("user", "conflict", CanvasReferenceMigrationRequest{Revision: &preview.Revision, Choices: choices}); err != nil {
		t.Fatal(err)
	}
}
