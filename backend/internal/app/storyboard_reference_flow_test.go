package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/tasks"
	"yingce/backend/internal/database"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// recordingTaskSubmitter is the mock provider boundary for the acceptance flow:
// it records the exact task request the server prepared and never contacts an
// upstream model. Admission, canvas commands, bindings and materialization stay
// real.
type recordingTaskSubmitter struct {
	mu       sync.Mutex
	requests []tasks.TaskRequest
	calls    int
	wakes    int
}

func (r *recordingTaskSubmitter) Prepare(_ context.Context, p identity.Principal, req tasks.TaskRequest) (tasks.PreparedTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.requests = append(r.requests, req)
	raw, err := json.Marshal(req.Input)
	if err != nil {
		return tasks.PreparedTask{}, err
	}
	return tasks.PreparedTask{Task: model.Task{
		ID: fmt.Sprintf("storyboard-flow-task-%d", r.calls), UserID: p.OwnerID,
		Type: "canvas_" + req.Mode, Operation: req.Operation, Prompt: req.Prompt,
		Status: model.TaskStatusQueued, Stage: "排队中", Model: "stub-model", InputJSON: string(raw),
	}}, nil
}

func (r *recordingTaskSubmitter) Commit(_ context.Context, tx *repository.Repository, prepared tasks.PreparedTask) error {
	return tx.InsertPreparedCanvasTask(&prepared.Task)
}

func (r *recordingTaskSubmitter) Wake() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wakes++
}

func (r *recordingTaskSubmitter) onlyRequest(t *testing.T) tasks.TaskRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) != 1 {
		t.Fatalf("provider submissions = %d, want exactly 1", len(r.requests))
	}
	return r.requests[0]
}

func (r *recordingTaskSubmitter) counters(t *testing.T) (int, int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.wakes
}

func flowSnapshot(t *testing.T, cs *commands.Service, p identity.Principal, canvasID string) commands.Snapshot {
	t.Helper()
	snapshot, err := cs.ReadSnapshot(context.Background(), p, canvasID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func flowRow(t *testing.T, doc commands.Document) map[string]any {
	t.Helper()
	board := cloudAgentNodeMetadata(creationNode(t, doc, "board"))
	storyboard, _ := board["storyboard"].(map[string]any)
	rows := creationMaps(storyboard["rows"])
	if len(rows) != 1 {
		t.Fatalf("storyboard rows = %d, want 1", len(rows))
	}
	return rows[0]
}

func creationNode(t *testing.T, doc commands.Document, id string) map[string]any {
	t.Helper()
	nodes, err := creationObjects(doc["nodes"])
	if err != nil {
		t.Fatal(err)
	}
	node := nodes[id]
	if node == nil {
		t.Fatalf("node %s missing from document", id)
	}
	return node
}

// managedSources lists the derived inputs of one storyboard handle, so a test can
// assert field and edge agreement without depending on edge ordering.
func managedSources(doc commands.Document, toHandleID string) []string {
	out := []string{}
	for _, edge := range creationMaps(doc["connections"]) {
		if stringValue(edge["managedBy"]) != "reference-fields-v1" || stringValue(edge["toHandleId"]) != toHandleID {
			continue
		}
		out = append(out, stringValue(edge["fromNodeId"]))
	}
	sort.Strings(out)
	return out
}

func managedEdge(doc commands.Document, toHandleID, fromNodeID string) map[string]any {
	for _, edge := range creationMaps(doc["connections"]) {
		if stringValue(edge["managedBy"]) == "reference-fields-v1" && stringValue(edge["toHandleId"]) == toHandleID && stringValue(edge["fromNodeId"]) == fromNodeID {
			return edge
		}
	}
	return nil
}

// managedOutputEdge finds the system-owned ownership edge of a declared slot. It
// is keyed by fromHandleId/relation, not by an input handle.
func managedOutputEdge(doc commands.Document, fromNodeID, toNodeID string) map[string]any {
	for _, edge := range creationMaps(doc["connections"]) {
		if stringValue(edge["managedBy"]) == "reference-fields-v1" && stringValue(edge["relation"]) == "storyboard-output" && stringValue(edge["fromNodeId"]) == fromNodeID && stringValue(edge["toNodeId"]) == toNodeID {
			return edge
		}
	}
	return nil
}

func mustEqualStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// flowVersion reads a version marker from either an in-memory command document
// (untyped int constants) or a stored JSON document (float64).
func flowVersion(value any) float64 {
	switch n := value.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

// TestStoryboardReferenceFlow is the Task 9 vertical acceptance path, driven only
// through server entries (no browser, no React and no paid provider call):
// canvas commands build the board and real asset nodes, the row declares inputs
// with assetBindings, the server derives managed edges, model_list reads the same
// row inputs, a task binds to the row through Service.Submit, the structured
// materializer writes the artifact back, clearing the row references keeps the
// shared table references, and a repeated operationId stays a single task.
func TestStoryboardReferenceFlow(t *testing.T) {
	ctx := context.Background()
	s, db := newMediaRecoveryTestService(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "flow-owner", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{ID: "flow-owner", OwnerID: "flow-owner", Role: "owner", ClientID: "client", Scopes: identity.OwnerScopes()}
	cs := commands.NewService(s.repo, commands.ServiceOptions{})
	submitter := &recordingTaskSubmitter{}
	// Real canvas task wiring: row input resolution and output preparation come from
	// the application, only the provider boundary is recorded.
	ts := tasks.NewService(s.repo, cs, submitter, s.CanvasTaskOptions())

	// Stage 1: real account resources back two real canvas assets.
	for _, resource := range []model.Resource{
		{ID: "row-reference", UserID: p.OwnerID, Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Width: 640, Height: 480, Size: 50},
		{ID: "shared-reference", UserID: p.OwnerID, Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Width: 640, Height: 480, Size: 50},
	} {
		if err := db.Create(&resource).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, asset := range []model.Asset{
		{ID: "hero-asset", UserID: p.OwnerID, Kind: "image", Status: "ready", Title: "英雄", PayloadJSON: `{"data":{"storageKey":"resource:row-reference","content":"/api/resources/row-reference/file"}}`},
		{ID: "style-asset", UserID: p.OwnerID, Kind: "image", Status: "ready", Title: "风格", PayloadJSON: `{"data":{"storageKey":"resource:shared-reference","content":"/api/resources/shared-reference/file"}}`},
	} {
		if err := db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
	}

	snap, err := cs.Create(ctx, p, commands.CreateCanvasRequest{ClientID: "client", OperationID: "create-flow"})
	if err != nil {
		t.Fatal(err)
	}
	if flowVersion(snap.Document["referenceFieldsVersion"]) != 1 {
		t.Fatalf("new canvas is not a declared-reference document: %+v", snap.Document["referenceFieldsVersion"])
	}
	x, y := 0.0, 0.0
	if _, err = cs.Execute(ctx, p, commands.Command{
		Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "build-flow", BaseRevision: snap.Revision,
		Operations: []commands.Operation{
			{Type: "add_node", ID: "hero", NodeType: "image", X: &x, Y: &y},
			{Type: "add_node", ID: "style", NodeType: "image", X: &x, Y: &y},
			{Type: "add_node", ID: "output", NodeType: "video", X: &x, Y: &y},
			{Type: "add_node", ID: "board", NodeType: "script", X: &x, Y: &y},
			{Type: "bind_asset", ID: "hero", AssetID: "hero-asset"},
			{Type: "bind_asset", ID: "style", AssetID: "style-asset"},
			{Type: "edit_storyboard", ID: "board", Action: "append", RowID: "row", Patch: map[string]any{"durationSeconds": float64(5), "plotDescription": "hero shot", "videoMotionPrompt": "camera moves"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Stage 2: declare the row input with assetBindings and the row output with the
	// semantic binding. No connection operation runs, yet the same commit derives
	// the managed edges.
	built := flowSnapshot(t, cs, p, snap.CanvasID)
	if len(creationMaps(built.Document["connections"])) != 0 {
		t.Fatalf("fixture started with connections: %+v", built.Document["connections"])
	}
	if _, err = cs.Execute(ctx, p, commands.Command{
		Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: built.DocumentEpoch, ClientID: "client", OperationID: "declare-flow", BaseRevision: built.Revision,
		Operations: []commands.Operation{
			{Type: "edit_storyboard", ID: "board", Action: "set_references", Patch: map[string]any{"referenceNodeIds": []any{"style"}}},
			{Type: "edit_storyboard", ID: "board", RowID: "row", Action: "update", Patch: map[string]any{"assetBindings": []any{map[string]any{"nodeId": "hero", "role": "style", "priority": float64(100)}}}},
			{Type: "bind_storyboard_output", ID: "board", RowID: "row", OutputMode: "video", SourceNodeID: "output"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	declared := flowSnapshot(t, cs, p, snap.CanvasID)
	mustEqualStrings(t, "row input edges", managedSources(declared.Document, "row:row"), []string{"hero", "style"})
	mustEqualStrings(t, "shared input edges", managedSources(declared.Document, "storyboard:context"), []string{"style"})
	rowEdge := managedEdge(declared.Document, "row:row", "hero")
	if rowEdge == nil || stringValue(rowEdge["storyboardRowId"]) != "row" || stringValue(rowEdge["referenceOwnerNodeId"]) != "board" || stringValue(rowEdge["referenceBindingId"]) != "node:hero:reference" {
		t.Fatalf("derived row edge does not match the declared field: %+v", rowEdge)
	}
	outputEdge := managedOutputEdge(declared.Document, "board", "output")
	if outputEdge == nil || stringValue(outputEdge["storyboardRowId"]) != "row" || stringValue(outputEdge["fromHandleId"]) != "row:row" || stringValue(outputEdge["referenceBindingId"]) != "output:video" {
		t.Fatalf("derived output edge does not match the declared slot: %+v", outputEdge)
	}
	row := flowRow(t, declared.Document)
	bindings, _ := row["assetBindings"].([]any)
	if len(bindings) != 1 || stringValue(creationMaps(bindings)[0]["nodeId"]) != "hero" || stringValue(creationMaps(bindings)[0]["role"]) != "style" {
		t.Fatalf("declared row input missing: %+v", row)
	}
	if stringValue(row["videoNodeId"]) != "output" {
		t.Fatalf("declared row output missing: %+v", row)
	}
	if len(creationMaps(declared.Document["connections"])) != 4 {
		t.Fatalf("unexpected visual connections: %+v", declared.Document["connections"])
	}

	// Stage 3: the model query resolves the same declared row inputs.
	intent, err := s.cloudAgentModelIntent(p.OwnerID, snap.CanvasID, `{"mode":"video","storyboardNodeId":"board","storyboardRowId":"row"}`)
	if err != nil {
		t.Fatal(err)
	}
	if intent == nil || intent.Capability != "video" || intent.Inputs["image"] != 2 || intent.Operation != "image_to_video" {
		t.Fatalf("model intent ignored row inputs: %+v", intent)
	}

	// Stage 4: the accepted task binds to the row. The stale client payload is
	// replaced by the row's prompt, references and duration.
	req := tasks.SubmitRequest{
		Command:      commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: declared.DocumentEpoch, ClientID: "client", OperationID: "storyboard-submit", BaseRevision: declared.Revision},
		TargetNodeID: "board",
		Consumer:     tasks.Consumer{Kind: "storyboard", RowID: "row", Field: "videoNodeId", OutputNodeID: "output"},
		Task: tasks.TaskRequest{Mode: "video", Prompt: "stale client prompt", Input: map[string]any{
			"config":          map[string]any{"videoSeconds": "99"},
			"referenceImages": []any{map[string]any{"id": "stale"}},
			"metadata":        map[string]any{"referenceNodeIds": []any{"missing-node"}},
		}},
	}
	handle, err := ts.Submit(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := s.repo.Task(handle.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	frozenInput := submitted.InputJSON
	recorded := submitter.onlyRequest(t)
	recordedRaw, err := json.Marshal(recorded)
	if err != nil {
		t.Fatal(err)
	}
	recordedRefs := creationMaps(recorded.Input["referenceImages"])
	recordedConfig, _ := recorded.Input["config"].(map[string]any)
	recordedMeta, _ := recorded.Input["metadata"].(map[string]any)
	if !strings.Contains(recorded.Prompt, "camera moves") || !strings.Contains(frozenInput, "camera moves") {
		t.Fatalf("row prompt was not submitted: %+v %s", recorded.Prompt, frozenInput)
	}
	if stringValue(recordedConfig["videoSeconds"]) != "5" {
		t.Fatalf("row duration was not submitted: %+v", recordedConfig)
	}
	if len(recordedRefs) != 2 || stringValue(recordedRefs[0]["id"]) != "hero" || stringValue(recordedRefs[1]["id"]) != "style" {
		t.Fatalf("row references were not submitted: %+v", recordedRefs)
	}
	if stringValue(recordedMeta["storyboardNodeId"]) != "board" || stringValue(recordedMeta["storyboardRowId"]) != "row" {
		t.Fatalf("row identity was not submitted: %+v", recordedMeta)
	}
	if recorded.Mode != "video" || recorded.Operation != "image_to_video" {
		t.Fatalf("submitted request shape: %+v", recorded)
	}
	for _, stale := range []string{"stale", `"videoSeconds":"99"`, "missing-node"} {
		if strings.Contains(string(recordedRaw), stale) || strings.Contains(frozenInput, stale) {
			t.Fatalf("stale client payload %q reached the task: %s %s", stale, recordedRaw, frozenInput)
		}
	}
	binding, err := s.repo.CanvasTaskBinding(handle.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.TargetNodeID != "board" || binding.Mode != "video" || !strings.Contains(binding.ConsumerJSON, `"rowId":"row"`) || !strings.Contains(binding.ConsumerJSON, `"outputNodeId":"output"`) {
		t.Fatalf("task binding is not row scoped: %+v", binding)
	}
	pending := flowSnapshot(t, cs, p, snap.CanvasID)
	row = flowRow(t, pending.Document)
	if stringValue(row["taskId"]) != handle.TaskID || stringValue(row["status"]) != "loading" || stringValue(row["videoNodeId"]) != "output" {
		t.Fatalf("row did not take the accepted task: %+v", row)
	}
	if stringValue(cloudAgentNodeMetadata(creationNode(t, pending.Document, "output"))["taskId"]) != handle.TaskID {
		t.Fatal("declared output did not take the accepted task")
	}

	// Stage 5: the terminal result is written back through the structured
	// materializer only, without a second provider submission.
	if err := db.Create(&model.Resource{ID: "result-video", UserID: p.OwnerID, Kind: "video", Status: model.ResourceStatusReady, MimeType: "video/mp4", Width: 640, Height: 360, Size: 500, DurationMs: 5000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", handle.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"video":{"resourceId":"result-video"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.EnqueueCanvasMaterialization(handle.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := ts.Materialize(ctx, handle.TaskID); err != nil {
		t.Fatal(err)
	}
	job, err := s.repo.CanvasMaterialization(handle.TaskID)
	if err != nil || job.State != "done" || job.Outcome != "applied" {
		t.Fatalf("materialization did not apply: %v %+v", err, job)
	}
	outputs, err := s.repo.CanvasTaskOutputs(handle.TaskID)
	if err != nil || len(outputs) != 1 || outputs[0].Kind != "video" || outputs[0].ResourceID != "result-video" {
		t.Fatalf("artifact was not materialized: %v %+v", err, outputs)
	}
	delivered := flowSnapshot(t, cs, p, snap.CanvasID)
	row = flowRow(t, delivered.Document)
	if stringValue(row["taskId"]) != handle.TaskID || stringValue(row["status"]) != "success" || stringValue(row["taskStatus"]) != "succeeded" {
		t.Fatalf("row did not receive the artifact state: %+v", row)
	}
	outputMeta := cloudAgentNodeMetadata(creationNode(t, delivered.Document, "output"))
	if stringValue(outputMeta["taskId"]) != handle.TaskID || stringValue(outputMeta["status"]) != "success" || stringValue(outputMeta["assetId"]) == "" || stringValue(outputMeta["content"]) == "" {
		t.Fatalf("output node did not receive the artifact: %+v", outputMeta)
	}
	if current, err := s.repo.Task(handle.TaskID); err != nil || current.InputJSON != frozenInput {
		t.Fatalf("materialization changed the frozen task input: %v %s", err, current.InputJSON)
	}
	if calls, wakes := submitter.counters(t); calls != 1 || wakes != 1 {
		t.Fatalf("writeback resubmitted the provider: prepare=%d wake=%d", calls, wakes)
	}

	// Stage 6: clearing the row binding removes only that derived edge. The
	// table-level shared reference, the asset nodes and the account resources stay.
	if _, err = cs.Execute(ctx, p, commands.Command{
		Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: delivered.DocumentEpoch, ClientID: "client", OperationID: "clear-row-references", BaseRevision: delivered.Revision,
		Operations: []commands.Operation{{Type: "edit_storyboard", ID: "board", RowID: "row", Action: "update", Patch: map[string]any{"assetBindings": []any{}}}},
	}); err != nil {
		t.Fatal(err)
	}
	cleared := flowSnapshot(t, cs, p, snap.CanvasID)
	mustEqualStrings(t, "row input edges after clearing", managedSources(cleared.Document, "row:row"), []string{"style"})
	mustEqualStrings(t, "shared input edges after clearing", managedSources(cleared.Document, "storyboard:context"), []string{"style"})
	if managedEdge(cleared.Document, "row:row", "hero") != nil {
		t.Fatal("cleared asset binding kept its managed edge")
	}
	if creationNode(t, cleared.Document, "hero") == nil || creationNode(t, cleared.Document, "style") == nil {
		t.Fatal("clearing references deleted the asset nodes")
	}
	var resourceCount, assetCount int64
	db.Model(&model.Resource{}).Count(&resourceCount)
	db.Model(&model.Asset{}).Count(&assetCount)
	if resourceCount != 3 || assetCount != 3 {
		t.Fatalf("clearing references removed artifacts: resources=%d assets=%d", resourceCount, assetCount)
	}
	resolved, err := prepareCanvasStoryboardInput(ctx, s.repo, p, cleared.Document, req)
	if err != nil {
		t.Fatal(err)
	}
	clearedRefs := creationMaps(resolved.Input["referenceImages"])
	clearedConfig, _ := resolved.Input["config"].(map[string]any)
	clearedMeta, _ := resolved.Input["metadata"].(map[string]any)
	if len(clearedRefs) != 1 || stringValue(clearedRefs[0]["id"]) != "style" {
		t.Fatalf("cleared row kept old references: %+v", clearedRefs)
	}
	if !strings.Contains(resolved.Prompt, "camera moves") || stringValue(clearedConfig["videoSeconds"]) != "5" {
		t.Fatalf("cleared row lost its declared inputs: %+v %+v", resolved.Prompt, clearedConfig)
	}
	if len(clearedMeta) != 2 || stringValue(clearedMeta["storyboardRowId"]) != "row" {
		t.Fatalf("cleared row metadata leaked stale keys: %+v", clearedMeta)
	}
	clearedRaw, err := json.Marshal(resolved.Input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(clearedRaw), "hero") || strings.Contains(string(clearedRaw), "missing-node") {
		t.Fatalf("cleared preparation still carries old references: %s", clearedRaw)
	}

	// Stage 7: the same operationId is a retry, not a second task, even after the
	// document moved on.
	retry, err := ts.Submit(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	if retry.TaskID != handle.TaskID || !reflect.DeepEqual(retry.Receipt, handle.Receipt) {
		t.Fatalf("retry created a new task: %+v vs %+v", retry, handle)
	}
	var taskCount int64
	db.Model(&model.Task{}).Count(&taskCount)
	if taskCount != 1 {
		t.Fatalf("retry created %d tasks, want 1", taskCount)
	}
	if calls, wakes := submitter.counters(t); calls != 1 || wakes != 1 {
		t.Fatalf("retry reached the provider: prepare=%d wake=%d", calls, wakes)
	}
	if current, err := s.repo.Task(handle.TaskID); err != nil || current.InputJSON != frozenInput {
		t.Fatalf("retry rewrote the frozen task input: %v %s", err, current.InputJSON)
	}
	final := flowSnapshot(t, cs, p, snap.CanvasID)
	if !reflect.DeepEqual(cleared.Document, final.Document) {
		t.Fatal("retry wrote the canvas")
	}
}
