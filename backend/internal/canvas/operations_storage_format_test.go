package canvas

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCanvasOperationFormatRequiresClientCapability(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	original := `{"id":"canvas","referenceFieldsVersion":1,"title":"旧标题","nodes":[{"id":"node","title":"旧"}],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", Title: "旧标题", Revision: 1, PayloadJSON: original}); err != nil {
		t.Fatal(err)
	}
	staleRequest := CanvasOperationsRequest{
		MutationID: "old-client",
		Operations: []CanvasOperation{{
			Type: "patch_node", ID: "node",
			Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"新标题"`)},
			Before: map[string]json.RawMessage{"title": json.RawMessage(`"旧"`)},
		}},
	}
	_, err := svc.ApplyUserCanvasOperations("user", "canvas", staleRequest)
	var appError *kernel.AppError
	if !errors.As(err, &appError) || appError.Status != http.StatusConflict || appError.Reason != "canvas_client_capability_required" {
		t.Fatalf("old client error = %v", err)
	}
	assertCanvasStorageUnchanged(t, svc, "user", "canvas", original, 1, "old-client")

	staleRequest.ClientCapabilities = []string{CanvasClientCapabilityReferenceFields}
	result, err := svc.ApplyUserCanvasOperations("user", "canvas", staleRequest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 2 {
		t.Fatalf("capable client revision = %d, want 2", result.Revision)
	}
}

func TestCanvasOperationCapabilityDoesNotChangeMutationIdentity(t *testing.T) {
	request := CanvasOperationsRequest{
		MutationID: "same-body",
		Operations: []CanvasOperation{{
			Type: "create_canvas", ID: "canvas",
			Patch: map[string]json.RawMessage{"title": json.RawMessage(`"新画布"`)},
		}},
	}
	without := CanvasOperationsDigest(request)
	request.ClientCapabilities = []string{CanvasClientCapabilityReferenceFields}
	if with := CanvasOperationsDigest(request); with != without {
		t.Fatalf("capability changed mutation digest: %s != %s", with, without)
	}
}

func TestCanvasOperationWithoutCapabilityCreatesLegacyDocument(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	request := CanvasOperationsRequest{
		MutationID: "legacy-client-create",
		Operations: []CanvasOperation{
			{Type: "create_canvas", ID: "canvas", Patch: map[string]json.RawMessage{"title": json.RawMessage(`"旧客户端"`)}},
			{Type: "create_node", Node: json.RawMessage(`{"id":"a","type":"image","metadata":{}}`)},
			{Type: "create_node", Node: json.RawMessage(`{"id":"b","type":"video","metadata":{}}`)},
			{Type: "create_connection", Connection: json.RawMessage(`{"id":"edge","fromNodeId":"a","toNodeId":"b"}`)},
		},
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "canvas", request); err != nil {
		t.Fatal(err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(project.PayloadJSON), &saved); err != nil {
		t.Fatal(err)
	}
	if _, marked := saved["referenceFieldsVersion"]; marked {
		t.Fatalf("old client was silently upgraded: %s", project.PayloadJSON)
	}
	if edges, _ := saved["connections"].([]any); len(edges) != 1 {
		t.Fatalf("legacy connection was not preserved: %s", project.PayloadJSON)
	}
}

func TestCanvasOperationCreateAcceptsReferenceFieldsVersion(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	request := CanvasOperationsRequest{
		MutationID:         "capable-client-create",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{
			{Type: "create_canvas", ID: "canvas", Patch: map[string]json.RawMessage{
				"title":                  json.RawMessage(`"新格式"`),
				"referenceFieldsVersion": json.RawMessage(`1`),
			}},
			{Type: "create_node", Node: json.RawMessage(`{"id":"a","type":"image","metadata":{}}`)},
		},
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "canvas", request); err != nil {
		t.Fatal(err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(project.PayloadJSON), &saved); err != nil {
		t.Fatal(err)
	}
	if saved["referenceFieldsVersion"] != float64(1) {
		t.Fatalf("referenceFieldsVersion = %#v, want 1: %s", saved["referenceFieldsVersion"], project.PayloadJSON)
	}
}

func TestCanvasOperationCreateNormalizesReferenceFieldsImport(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	request := CanvasOperationsRequest{
		MutationID:         "capable-import",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{
			{Type: "create_canvas", ID: "canvas", Patch: map[string]json.RawMessage{
				"title":                  json.RawMessage(`"导入画布"`),
				"referenceFieldsVersion": json.RawMessage(`1`),
			}},
			{Type: "create_node", Node: json.RawMessage(`{"id":"source","type":"image","metadata":{}}`)},
			{Type: "create_node", Node: json.RawMessage(`{"id":"target","type":"image","metadata":{}}`)},
			{Type: "create_connection", Connection: json.RawMessage(`{"id":"legacy","fromNodeId":"source","toNodeId":"target"}`)},
		},
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "canvas", request); err != nil {
		t.Fatal(err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(project.PayloadJSON), &saved); err != nil {
		t.Fatal(err)
	}
	if saved["referenceFieldsVersion"] != float64(1) {
		t.Fatalf("referenceFieldsVersion = %#v, want 1: %s", saved["referenceFieldsVersion"], project.PayloadJSON)
	}
	edges, _ := saved["connections"].([]any)
	if len(edges) != 1 {
		t.Fatalf("normalized connection count = %d, want 1: %s", len(edges), project.PayloadJSON)
	}
	edge, _ := edges[0].(map[string]any)
	if edge["managedBy"] != "reference-fields-v1" || edge["id"] == "legacy" || edge["referenceOwnerNodeId"] != "target" {
		t.Fatalf("legacy connection was not normalized: %#v", edge)
	}
}

func TestCanvasOperationCreateRejectsUnauthorizedReferenceFormat(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capabilities []string
		version      string
		reason       string
	}{
		{name: "old client", version: `1`, reason: "canvas_client_capability_required"},
		{name: "unknown version", capabilities: []string{CanvasClientCapabilityReferenceFields}, version: `2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newCanvasStorageFormatTestService(t)
			request := CanvasOperationsRequest{
				MutationID:         "invalid-format-create",
				ClientCapabilities: tc.capabilities,
				Operations: []CanvasOperation{{
					Type: "create_canvas", ID: "canvas",
					Patch: map[string]json.RawMessage{
						"title":                  json.RawMessage(`"非法格式"`),
						"referenceFieldsVersion": json.RawMessage(tc.version),
					},
				}},
			}
			_, err := svc.ApplyUserCanvasOperations("user", "canvas", request)
			if err == nil {
				t.Fatal("invalid reference format creation was accepted")
			}
			if tc.reason != "" {
				var appError *kernel.AppError
				if !errors.As(err, &appError) || appError.Reason != kernel.ErrorReason(tc.reason) {
					t.Fatalf("error = %v, want reason %s", err, tc.reason)
				}
			}
			if _, err := svc.repo.CanvasProjectForUser("user", "canvas"); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("rejected creation persisted a canvas: %v", err)
			}
			if _, err := svc.repo.CanvasMutationReceiptForUser("user", "canvas", request.MutationID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("rejected creation persisted a receipt: %v", err)
			}
		})
	}
}

func TestCanvasOperationCreateRejectsUnresolvedReferenceFormat(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	request := CanvasOperationsRequest{
		MutationID:         "capable-conflict",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{
			{Type: "create_canvas", ID: "canvas", Patch: map[string]json.RawMessage{
				"title":                  json.RawMessage(`"冲突画布"`),
				"referenceFieldsVersion": json.RawMessage(`1`),
			}},
			{Type: "create_node", Node: json.RawMessage(`{"id":"a","type":"image","metadata":{}}`)},
			{Type: "create_node", Node: json.RawMessage(`{"id":"b","type":"image","metadata":{}}`)},
			{Type: "create_connection", Connection: json.RawMessage(`{"id":"ab","fromNodeId":"a","toNodeId":"b"}`)},
			{Type: "create_connection", Connection: json.RawMessage(`{"id":"ba","fromNodeId":"b","toNodeId":"a"}`)},
		},
	}
	_, err := svc.ApplyUserCanvasOperations("user", "canvas", request)
	var appError *kernel.AppError
	if !errors.As(err, &appError) || appError.Reason != "canvas_document_format_conflict" {
		t.Fatalf("unresolved reference format error = %v", err)
	}
	if _, err := svc.repo.CanvasProjectForUser("user", "canvas"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("rejected reference import persisted a canvas: %v", err)
	}
	if _, err := svc.repo.CanvasMutationReceiptForUser("user", "canvas", request.MutationID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("rejected reference import persisted a receipt: %v", err)
	}
}

func TestCanvasOperationFormatVersionIsCreateOnly(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	original := `{"id":"canvas","referenceFieldsVersion":1,"nodes":[],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", Revision: 1, PayloadJSON: original}); err != nil {
		t.Fatal(err)
	}
	request := CanvasOperationsRequest{
		MutationID:         "rewrite-version",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{{
			Type: "set_field", Field: "referenceFieldsVersion",
			Value: json.RawMessage(`2`), BeforeValue: json.RawMessage(`1`),
		}},
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "canvas", request); err == nil {
		t.Fatal("document format version was mutable after creation")
	}
	assertCanvasStorageUnchanged(t, svc, "user", "canvas", original, 1, request.MutationID)
}

func TestCanvasOperationFormatRejectsConnectionWrites(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	original := `{"id":"canvas","referenceFieldsVersion":1,"nodes":[{"id":"a"},{"id":"b"}],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", Revision: 1, PayloadJSON: original}); err != nil {
		t.Fatal(err)
	}
	request := CanvasOperationsRequest{
		MutationID:         "legacy-connection-write",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{{
			Type:       "create_connection",
			Connection: json.RawMessage(`{"id":"edge","fromNodeId":"a","toNodeId":"b"}`),
		}},
	}
	_, err := svc.ApplyUserCanvasOperations("user", "canvas", request)
	var appError *kernel.AppError
	if !errors.As(err, &appError) || appError.Reason != "canvas_connection_operations_retired" {
		t.Fatalf("connection write error = %v", err)
	}
	assertCanvasStorageUnchanged(t, svc, "user", "canvas", original, 1, request.MutationID)
}

func TestCanvasOperationFormatPersistsBookkeepingFields(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	original := `{"id":"canvas","referenceFieldsVersion":1,"nodes":[],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", Revision: 1, PayloadJSON: original}); err != nil {
		t.Fatal(err)
	}
	request := CanvasOperationsRequest{
		MutationID:         "bookkeeping",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{
			{Type: "set_field", Field: "idCounters", Value: json.RawMessage(`{"image":2}`), BeforeValue: json.RawMessage(`null`)},
			{Type: "set_field", Field: "edgePresentation", Value: json.RawMessage(`{"conn:r1:b:a":{"toAnchorRatio":0.25}}`), BeforeValue: json.RawMessage(`null`)},
		},
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "canvas", request); err != nil {
		t.Fatal(err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(project.PayloadJSON), &saved); err != nil {
		t.Fatal(err)
	}
	counters, _ := saved["idCounters"].(map[string]any)
	presentation, _ := saved["edgePresentation"].(map[string]any)
	if counters["image"] != float64(2) || presentation["conn:r1:b:a"] == nil {
		t.Fatalf("bookkeeping fields missing: %s", project.PayloadJSON)
	}
}

func TestCanvasOperationReferenceFormatReplaysReceiptAndRejectsChangedPayload(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	original := `{"id":"canvas","referenceFieldsVersion":1,"nodes":[{"id":"node","title":"v1"}],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", Revision: 1, PayloadJSON: original}); err != nil {
		t.Fatal(err)
	}
	request := CanvasOperationsRequest{
		MutationID:         "reference-replay",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{{
			Type: "patch_node", ID: "node",
			Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"v2"`)},
			Before: map[string]json.RawMessage{"title": json.RawMessage(`"v1"`)},
		}},
	}
	first, err := svc.ApplyUserCanvasOperations("user", "canvas", request)
	if err != nil {
		t.Fatal(err)
	}
	replay := request
	replay.ClientCapabilities = nil
	replayed, err := svc.ApplyUserCanvasOperations("user", "canvas", replay)
	if err != nil {
		t.Fatalf("receipt replay rejected: %v", err)
	}
	if replayed.ID != first.ID || replayed.Title != first.Title || replayed.Revision != first.Revision {
		t.Fatalf("replayed summary = %#v, want identity matching %#v", replayed, first)
	}
	changed := request
	changed.Operations = []CanvasOperation{{
		Type: "patch_node", ID: "node",
		Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"v3"`)},
		Before: map[string]json.RawMessage{"title": json.RawMessage(`"v2"`)},
	}}
	_, err = svc.ApplyUserCanvasOperations("user", "canvas", changed)
	var appError *kernel.AppError
	if !errors.As(err, &appError) || appError.Reason != "canvas_mutation_id_reused" {
		t.Fatalf("changed replay error = %v, want canvas_mutation_id_reused", err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	if project.Revision != 2 || !strings.Contains(project.PayloadJSON, `"title":"v2"`) {
		t.Fatalf("changed replay mutated document: revision=%d payload=%s", project.Revision, project.PayloadJSON)
	}
	receipt, err := svc.repo.CanvasMutationReceiptForUser("user", "canvas", request.MutationID)
	if err != nil || receipt.Revision != 2 {
		t.Fatalf("original receipt changed: %#v %v", receipt, err)
	}
}

func TestCanvasSaveReferenceFormatRejectsStaleRevisionWithoutChange(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	initial := json.RawMessage(`{"id":"canvas","revision":0,"referenceFieldsVersion":1,"title":"v1","nodes":[],"connections":[]}`)
	if _, err := svc.UpsertUserCanvasProject("user", initial); err != nil {
		t.Fatal(err)
	}
	current := json.RawMessage(`{"id":"canvas","revision":1,"referenceFieldsVersion":1,"title":"v2","nodes":[],"connections":[]}`)
	if _, err := svc.UpsertUserCanvasProject("user", current); err != nil {
		t.Fatal(err)
	}
	_, err := svc.UpsertUserCanvasProject("user", current)
	var appError *kernel.AppError
	if !errors.As(err, &appError) || appError.Status != http.StatusConflict {
		t.Fatalf("stale full save error = %v, want conflict", err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	if project.Revision != 2 || !strings.Contains(project.PayloadJSON, `"title":"v2"`) {
		t.Fatalf("stale save changed document: revision=%d payload=%s", project.Revision, project.PayloadJSON)
	}
}

func TestCanvasOperationFormatDiffSkipsProjectedConnections(t *testing.T) {
	before := map[string]any{
		"referenceFieldsVersion": float64(1),
		"nodes":                  []any{},
		"connections":            []any{},
	}
	after := map[string]any{
		"referenceFieldsVersion": float64(1),
		"nodes":                  []any{},
		"connections":            []any{map[string]any{"id": "cache", "fromNodeId": "a", "toNodeId": "b"}},
		"edgePresentation":       map[string]any{"conn:r1:b:a": map[string]any{"toAnchorRatio": 0.25}},
	}
	operations := DiffCanvasDocuments(before, after)
	if len(operations) != 1 || operations[0].Type != "set_field" || operations[0].Field != "edgePresentation" {
		t.Fatalf("operations = %#v, want only edgePresentation", operations)
	}
}

func TestCanvasOperationTransactionFailureRollsBackDocumentAndReceipt(t *testing.T) {
	svc, db := newCanvasStorageFormatTestService(t)
	original := `{"id":"canvas","referenceFieldsVersion":1,"nodes":[{"id":"node","title":"旧"}],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", Revision: 1, PayloadJSON: original}); err != nil {
		t.Fatal(err)
	}
	const callbackName = "test:fail-canvas-receipt-finalize"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "canvas_mutation_receipts" {
			tx.AddError(errors.New("forced receipt failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })
	request := CanvasOperationsRequest{
		MutationID:         "rollback",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{{
			Type: "patch_node", ID: "node",
			Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"新"`)},
			Before: map[string]json.RawMessage{"title": json.RawMessage(`"旧"`)},
		}},
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "canvas", request); err == nil {
		t.Fatal("forced receipt failure was ignored")
	}
	assertCanvasStorageUnchanged(t, svc, "user", "canvas", original, 1, request.MutationID)
}

func TestCanvasOperationRejectsOtherUserWithoutSideEffects(t *testing.T) {
	svc, _ := newCanvasStorageFormatTestService(t)
	original := `{"id":"canvas","referenceFieldsVersion":1,"nodes":[{"id":"node","title":"旧"}],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "owner", Revision: 1, PayloadJSON: original}); err != nil {
		t.Fatal(err)
	}
	request := CanvasOperationsRequest{
		MutationID:         "intruder",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{{
			Type: "patch_node", ID: "node",
			Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"越权"`)},
			Before: map[string]json.RawMessage{"title": json.RawMessage(`"旧"`)},
		}},
	}
	if _, err := svc.ApplyUserCanvasOperations("intruder", "canvas", request); err == nil {
		t.Fatal("another user could write the canvas")
	}
	assertCanvasStorageUnchanged(t, svc, "owner", "canvas", original, 1, request.MutationID)
}

func assertCanvasStorageUnchanged(t *testing.T, svc *Service, userID, canvasID, payload string, revision int64, mutationID string) {
	t.Helper()
	project, err := svc.repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		t.Fatal(err)
	}
	if project.PayloadJSON != payload || project.Revision != revision {
		t.Fatalf("canvas changed after rejected write: revision=%d payload=%s", project.Revision, project.PayloadJSON)
	}
	if _, err := svc.repo.CanvasMutationReceiptForUser(userID, canvasID, mutationID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("rejected write retained a receipt: %v", err)
	}
}

func newCanvasStorageFormatTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "canvas-storage-format.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CanvasEditLease{}, &model.CanvasProject{}, &model.CanvasSnapshot{}, &model.CanvasSnapshotResource{}, &model.CanvasMutationReceipt{}, &model.CanvasShare{}, &model.CanvasUnitLink{}, &model.Task{}, &model.CloudAgentExecution{}, &model.Resource{}, &model.Asset{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return New(repository.New(db), nil), db
}
