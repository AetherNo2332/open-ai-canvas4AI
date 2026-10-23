package app

import (
	"fmt"
	"testing"
)

func TestStoryboardFill89RowsInChunks(t *testing.T) {
	f := newStoryboardReceiptFixture(t, 89)
	fields := []string{"imageGenerationPrompt", "videoMotionPrompt"}
	read := func(args map[string]any) map[string]any {
		t.Helper()
		args["nodeId"] = "receipt-board"
		call := cloudAgentStoryboardCall(t, "canvas_read_storyboard", "fill-read", args)
		if err := validateCloudAgentToolArguments(workflowTool(t, call.Function.Name), call.Function.Arguments); err != nil {
			t.Fatal(err)
		}
		result, err := cloudAgentReadStoryboard(f.service.repo, "user", f.canvas.ID, call)
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]any)
	}
	initial := read(map[string]any{"fields": fields, "missingAnyFields": fields})
	if initial["coverage"].(map[string]any)["matchedRows"] != 89 {
		t.Fatal("fixture must start with 89 incomplete rows")
	}
	snapshot := f.created["snapshotHash"]
	expected := map[string]int{}
	for offset := 0; offset < 89; offset += 15 {
		end := min(offset+15, 89)
		updates := []map[string]any{}
		for index := offset; index < end; index++ {
			id := stringValue(f.rows[index]["id"])
			expected[id] = index
			updates = append(updates, map[string]any{"rowId": id, "patch": map[string]any{
				"imageGenerationPrompt": fmt.Sprintf("第 %02d 镜静帧", index+1),
				"videoMotionPrompt":     fmt.Sprintf("第 %02d 镜运动", index+1),
			}})
		}
		call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", fmt.Sprintf("fill-chunk-%d", offset/15), map[string]any{
			"snapshotHash": snapshot, "nodeId": "receipt-board", "action": "batch_update", "updates": updates,
		})
		if err := validateCloudAgentToolArguments(workflowTool(t, call.Function.Name), call.Function.Arguments); err != nil {
			t.Fatal(err)
		}
		result, err := applyCloudAgentStoryboardMutation(f.service.repo, "user", f.canvas.ID, call, f.policy)
		if err != nil {
			t.Fatal(err)
		}
		receipt := cloudAgentModelToolResult(call.Function.Name, result).(map[string]any)
		if receipt["applied"] != true || receipt["affectedRows"] != end-offset || len(creationMaps(receipt["changedRows"])) != end-offset {
			t.Fatalf("chunk %d lost per-row effects: %+v", offset/15, receipt)
		}
		if _, fullRoster := receipt["rows"]; fullRoster {
			t.Fatal("chunk receipt returned full table")
		}
		snapshot = receipt["snapshotHash"]
	}
	missing := read(map[string]any{"fields": fields, "missingAnyFields": fields})
	coverage := missing["coverage"].(map[string]any)
	if coverage["selectionRows"] != 89 || coverage["matchedRows"] != 0 || coverage["selectionComplete"] != true {
		t.Fatalf("fill left missing target fields: %+v", missing)
	}
	result := read(map[string]any{"fields": append(fields, "plotDescription")})
	verified := map[string]bool{}
	for {
		if result["snapshotHash"] != snapshot {
			t.Fatal("readback did not use the final saved content")
		}
		board := result["storyboard"].(map[string]any)
		for _, row := range creationMaps(board["rows"]) {
			id := stringValue(row["rowId"])
			index, exists := expected[id]
			if !exists || verified[id] || row["imageGenerationPrompt"] != fmt.Sprintf("第 %02d 镜静帧", index+1) || row["videoMotionPrompt"] != fmt.Sprintf("第 %02d 镜运动", index+1) || row["plotDescription"] != fmt.Sprintf("镜头 %d", index+1) {
				t.Fatalf("row content/identity was lost: %+v", row)
			}
			verified[id] = true
		}
		if board["hasMore"] != true {
			break
		}
		result = read(board["nextRead"].(map[string]any)["arguments"].(map[string]any))
	}
	if len(verified) != 89 {
		t.Fatalf("readback verified only %d of 89 rows", len(verified))
	}
}
