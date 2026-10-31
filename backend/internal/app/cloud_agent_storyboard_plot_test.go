package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentStoryboardPlotToolRoundTrip(t *testing.T) {
	doc := parityDocument(t, `{"nodes":[],"connections":[]}`)
	if err := parityOps(t, doc, `[{"type":"add_node","id":"board","nodeType":"script","content":"雨夜，她推门。"}]`); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"", "content", "composerContent"} {
		text, canonical, err := cloudAgentTextSource(doc, cloudAgentTextReadArgs{NodeID: "board", Field: field})
		if err != nil || text != "雨夜，她推门。" || canonical != "composerContent" {
			t.Fatalf("plot lost or unreadable via %q: %q %q %v", field, text, canonical, err)
		}
	}
	for _, op := range []string{
		`[{"type":"update_node","id":"board","patch":{"composerContent":"雨夜，她读信。"}}]`,
		`[{"type":"update_node","id":"board","patch":{"content":"雨夜，她读信。"}}]`,
		`[{"type":"replace_text","id":"board","match":"读信","replacement":"回头"}]`,
	} {
		if err := parityOps(t, doc, op); err != nil {
			t.Fatal(err)
		}
	}
	text, _, err := cloudAgentTextSource(doc, cloudAgentTextReadArgs{NodeID: "board"})
	if err != nil || text != "雨夜，她回头。" {
		t.Fatalf("plot edit lost: %q %v", text, err)
	}
	before, _ := json.Marshal(doc)
	for _, patch := range []string{`{"status":"success"}`, `{"content":"one","composerContent":"two"}`} {
		if err := parityOps(t, doc, `[{"type":"update_node","id":"board","patch":`+patch+`}]`); err == nil {
			t.Fatalf("unsafe or conflicting patch accepted: %s", patch)
		}
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("rejected patch changed plot")
	}
}

func TestCloudAgentStoryboardPlotCreatedThroughToolsCanSplit(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	doc["referenceFieldsVersion"] = 1
	raw, _ := json.Marshal(doc)
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	call := agentCanvasOpsCall(t, "create-plot", cloudAgentCanvasHash(doc), []map[string]any{
		{"type": "add_node", "id": "board", "nodeType": "script", "content": "雨夜，她推开书店的门。"},
	})
	if _, err := applyCloudAgentCanvas(s.repo, "user", canvas.ID, call, policy); err != nil {
		t.Fatal(err)
	}
	read := piAgentTestCall("read-plot", "canvas_read_content", `{"nodeId":"board","field":"composerContent"}`)
	result, err := cloudAgentContentRead(s.repo, "user", canvas.ID, read)
	if err != nil || result.(map[string]any)["content"] != "雨夜，她推开书店的门。" {
		t.Fatalf("persisted plot unreadable: %+v %v", result, err)
	}
	req := agentTestRequest()
	req.CanvasID = canvas.ID
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks = 1
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	split := piAgentTestCall("split-plot", "storyboard", `{"action":"split","nodeId":"board","channelId":"channel","channelModelKey":"text-test"}`)
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{split})
	if err := agentPiExecuteCallForTest(t, s, run.ID, split.ID); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 1 {
		t.Fatalf("tool-created plot did not submit: %s", cloudAgentToolMessageForTest(t, state, split.ID))
	}
	var task model.Task
	if err := db.Where("id = ?", state.PendingGenerations[0].TaskID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Prompt, "雨夜，她推开书店的门。") {
		t.Fatalf("server prompt lost tool-created plot: %s", task.Prompt)
	}
}
