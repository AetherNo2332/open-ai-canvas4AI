package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"yingce/backend/internal/model"
)

type storyboardReceiptFixture struct {
	service *Service
	canvas  *model.CanvasProject
	policy  RuntimePolicySetting
	created map[string]any
	rows    []map[string]any
}

func newStoryboardReceiptFixture(t *testing.T, count int) *storyboardReceiptFixture {
	t.Helper()
	s, _, canvas := cloudAgentStoryboardFixture(t)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, count)
	for i := range rows {
		rows[i] = map[string]any{"durationSeconds": 4.0, "plotDescription": fmt.Sprintf("镜头 %d", i+1)}
	}
	call := cloudAgentStoryboardCall(t, "canvas_create_storyboard", "receipt-create", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "receipt-board", "title": "回执测试", "rows": rows,
	})
	result, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.CanvasProjectForUser("user", canvas.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = creationDocument(stored.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	_, _, storedRows, err := storyboardNodeFromDocument(doc, "receipt-board")
	if err != nil {
		t.Fatal(err)
	}
	return &storyboardReceiptFixture{s, stored, policy, cloudAgentModelToolResult(call.Function.Name, result).(map[string]any), storedRows}
}

func (f *storyboardReceiptFixture) edit(t *testing.T, callID string, args map[string]any) map[string]any {
	t.Helper()
	stored, err := f.service.repo.CanvasProjectForUser("user", f.canvas.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(stored.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	args["nodeId"], args["snapshotHash"] = "receipt-board", cloudAgentCanvasHash(doc)
	call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", callID, args)
	result, err := applyCloudAgentStoryboardMutation(f.service.repo, "user", f.canvas.ID, call, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	return cloudAgentModelToolResult(call.Function.Name, result).(map[string]any)
}

func TestStoryboardEditReceiptScalesWithChanges(t *testing.T) {
	var sizes []int
	for _, count := range []int{1, 89} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newStoryboardReceiptFixture(t, count)
			firstID := stringValue(f.rows[0]["id"])
			receipt := f.edit(t, "receipt-edit", map[string]any{
				"action": "update", "rowId": firstID,
				"patch": map[string]any{"dialogue": "新的对白", "durationSeconds": 4.0},
			})
			if receipt["applied"] != true || receipt["changed"] != true || receipt["affectedRows"] != 1 {
				t.Fatalf("missing mutation outcome: %+v", receipt)
			}
			if _, exists := receipt["rows"]; exists {
				t.Fatal("single-row edit returns full row roster")
			}
			changes := creationMaps(receipt["changedRows"])
			if len(changes) != 1 || changes[0]["rowId"] != firstID || !reflect.DeepEqual(changes[0]["changedFields"], []string{"dialogue"}) {
				t.Fatalf("receipt does not identify actual field changes: %+v", changes)
			}
			if receipt["snapshotHash"] == receipt["beforeSnapshotHash"] {
				t.Fatal("content change did not produce a new snapshot")
			}
			body, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			sizes = append(sizes, len(body))
		})
	}
	if len(sizes) == 2 && sizes[0] != sizes[1] {
		t.Fatalf("single-row edit receipt grows with table size: %v", sizes)
	}
}

func TestStoryboardNoopReceipt(t *testing.T) {
	f := newStoryboardReceiptFixture(t, 2)
	args := map[string]any{
		"action": "update", "rowIds": []string{stringValue(f.rows[0]["id"]), stringValue(f.rows[1]["id"])},
		"patch": map[string]any{"durationSeconds": 4.0},
	}
	receipt := f.edit(t, "receipt-noop", args)
	if receipt["applied"] != true || receipt["changed"] != false || receipt["affectedRows"] != 2 || len(creationMaps(receipt["changedRows"])) != 0 {
		t.Fatalf("no-op is not explicit: %+v", receipt)
	}
	// edit 用写入前的画布哈希作为 snapshotHash 参数；无变化时回执里的新哈希必须与它相同。
	if receipt["snapshotHash"] != args["snapshotHash"] {
		t.Fatal("no-op changed content snapshot")
	}
	if receipt["beforeSnapshotHash"] != nil {
		t.Fatalf("model receipt should not carry beforeSnapshotHash: %+v", receipt)
	}
}

func TestStoryboardReceiptCreatedAppendedRemovedRows(t *testing.T) {
	f := newStoryboardReceiptFixture(t, 3)
	created := creationMaps(f.created["createdRows"])
	if len(created) != 3 || f.created["affectedRows"] != 3 {
		t.Fatalf("creation lost allocated row IDs: %+v", f.created)
	}
	for i, row := range created {
		if row["rowId"] != f.rows[i]["id"] || row["shotNumber"] != float64(i+1) {
			t.Fatalf("creation row mapping %d: %+v", i, row)
		}
	}
	appended := f.edit(t, "receipt-append", map[string]any{
		"action": "append", "patch": map[string]any{"durationSeconds": 3.0, "plotDescription": "追加镜头"},
	})
	newRows := creationMaps(appended["createdRows"])
	if len(newRows) != 1 || newRows[0]["shotNumber"] != float64(4) || appended["affectedRows"] != 1 {
		t.Fatalf("append must only return new ID: %+v", appended)
	}
	removedID := stringValue(f.rows[0]["id"])
	removed := f.edit(t, "receipt-remove", map[string]any{"action": "remove", "rowId": removedID})
	if !reflect.DeepEqual(removed["removedRowIds"], []string{removedID}) || removed["affectedRows"] != 1 {
		t.Fatalf("delete lost removed ID: %+v", removed)
	}
	changes := creationMaps(removed["changedRows"])
	if len(changes) != 3 {
		t.Fatalf("renumbering effects must remain visible: %+v", changes)
	}
	for _, row := range changes {
		if !reflect.DeepEqual(row["changedFields"], []string{"shotNumber"}) {
			t.Fatalf("unexpected delete side effects: %+v", row)
		}
	}
}
