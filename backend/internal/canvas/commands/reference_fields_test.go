package commands

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func referenceDocument(t *testing.T) Document {
	return fixture(t, `{"referenceFieldsVersion":1,"nodes":[{"id":"asset","type":"image","metadata":{}},{"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":[],"rows":[{"id":"row","shotNumber":1,"durationSeconds":5,"plotDescription":"test","assetBindings":[]}]}}}],"connections":[]}`)
}
func rowReferenceOp() Operation {
	return Operation{Type: "edit_storyboard", ID: "board", RowID: "row", Action: "update", Patch: map[string]any{"assetBindings": []any{map[string]any{"nodeId": "asset", "role": "style", "priority": float64(80)}}}}
}
func TestReferenceFieldsCommitAtomically(t *testing.T) {
	before := referenceDocument(t)
	got, err := Plan(before, command(rowReferenceOp()), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(maps(got.Document["connections"])) != 1 {
		t.Fatal("field update did not create its reference edge")
	}
	restored, err := ApplyChanges(got.Document, got.Inverse)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, before) {
		t.Fatal("inverse did not restore fields and edges")
	}
	bad := rowReferenceOp()
	bad.Patch = map[string]any{"assetBindings": []any{map[string]any{"nodeId": "missing", "role": "style", "priority": float64(80)}}}
	if _, err = Plan(before, command(rowReferenceOp(), bad), ownerContext()); err == nil {
		t.Fatal("bad reference committed partial batch")
	}
	if len(maps(before["connections"])) != 0 {
		t.Fatal("input changed on failure")
	}
}
func TestReferenceFieldsTrackReferencedVersions(t *testing.T) {
	got, err := Plan(referenceDocument(t), command(rowReferenceOp()), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReadSet["node:asset"] {
		t.Fatal("referenced source missing from read set")
	}
}
func TestReferenceFieldsSemanticOutput(t *testing.T) {
	var op Operation
	_ = json.Unmarshal([]byte(`{"type":"bind_storyboard_output","id":"board","rowId":"row","outputMode":"image","sourceNodeId":"asset"}`), &op)
	got, err := Plan(referenceDocument(t), command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	row := maps(entity(got.Document, "node:board")["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"])[0]
	if row["imageNodeId"] != "asset" || len(maps(got.Document["connections"])) != 1 {
		t.Fatal("output identity and connection did not bind together")
	}
}
func TestReferenceFieldsUndoRedo(t *testing.T) {
	svc, _, _, snap := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	req := command(Operation{Type: "add_node", ID: "image", NodeType: "image", X: num(0), Y: num(0)}, Operation{Type: "add_node", ID: "board", NodeType: "script", X: num(500), Y: num(0)}, Operation{Type: "edit_storyboard", ID: "board", Action: "append", RowID: "row", Patch: map[string]any{"durationSeconds": float64(5), "plotDescription": "test"}})
	req.CanvasID, req.DocumentEpoch, req.BaseRevision = snap.CanvasID, snap.DocumentEpoch, snap.Revision
	req.OperationID = "setup-refs"
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	op := rowReferenceOp()
	op.Patch["assetBindings"].([]any)[0].(map[string]any)["nodeId"] = "image"
	req = command(op)
	req.CanvasID, req.DocumentEpoch, req.BaseRevision = current.CanvasID, current.DocumentEpoch, current.Revision
	req.OperationID = "edit-refs"
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	current, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if len(maps(current.Document["connections"])) != 1 {
		t.Fatal("reference not persisted")
	}
	if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "undo-refs"); err != nil {
		t.Fatal(err)
	}
	current, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if len(maps(current.Document["connections"])) != 0 {
		t.Fatal("undo left a reference edge")
	}
	if _, err := svc.Redo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "redo-refs"); err != nil {
		t.Fatal(err)
	}
	current, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if len(maps(current.Document["connections"])) != 1 {
		t.Fatal("redo did not restore reference edge")
	}
}
