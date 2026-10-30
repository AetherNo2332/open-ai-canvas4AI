package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/tasks"
	"yingce/backend/internal/identity"
)

func TestStoryboardStableHandlesFollowIdentityAcrossInputOrder(t *testing.T) {
	s, db, doc, args := storyboardMediaFixture(t)
	_, _, rows, _ := storyboardNodeFromDocument(doc, "board")
	const original = "{{ref:node:hero:reference}} 从 {{ref:node:cat:reference}} 旁经过"
	rows[0]["videoMotionPrompt"] = original
	for _, reordered := range []bool{false, true} {
		if reordered {
			rows[0]["assetBindings"] = []any{map[string]any{"nodeId": "cat", "role": "style", "priority": 200.0}, map[string]any{"nodeId": "hero", "role": "character", "priority": 100.0}}
		}
		raw, _ := json.Marshal(doc)
		if err := db.Table("canvas_projects").Where("id = ?", "agent-canvas").Update("payload_json", string(raw)).Error; err != nil {
			t.Fatal(err)
		}
		args["snapshotHash"] = cloudAgentCanvasHash(doc)
		req, _, err := storyboardMediaPrepare(t, s, args)
		if err != nil {
			t.Fatal(err)
		}
		want := "@图片1 从 @图片2 旁经过"
		if reordered {
			want = "@图片2 从 @图片1 旁经过"
		}
		if !strings.Contains(req.Prompt, want) || strings.Contains(req.Prompt, "{{ref:") {
			t.Fatalf("provider prompt lost stable identity: %s", req.Prompt)
		}
		native, err := prepareCanvasStoryboardInput(context.Background(), s.repo, identity.Principal{ID: "user", OwnerID: "user"}, doc, tasks.SubmitRequest{Command: commands.Command{CanvasID: "agent-canvas"}, TargetNodeID: "board", Consumer: tasks.Consumer{RowID: "row", OutputNodeID: "output"}, Task: tasks.TaskRequest{Mode: "video"}})
		if err != nil || !strings.Contains(native.Prompt, want) {
			t.Fatalf("native generation differs: %s %v", native.Prompt, err)
		}
		if rows[0]["videoMotionPrompt"] != original {
			t.Fatal("generation rewrote stored prompt")
		}
	}
	rows[0]["assetBindings"] = []any{}
	if _, err := prepareCanvasStoryboardInput(context.Background(), s.repo, identity.Principal{ID: "user", OwnerID: "user"}, doc, tasks.SubmitRequest{Command: commands.Command{CanvasID: "agent-canvas"}, TargetNodeID: "board", Consumer: tasks.Consumer{RowID: "row"}, Task: tasks.TaskRequest{Mode: "video"}}); err == nil {
		t.Fatal("missing handle silently rebound to another asset")
	}
}
