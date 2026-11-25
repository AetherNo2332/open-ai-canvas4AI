package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestPiVisionOriginalsAreRequestOnly(t *testing.T) {
	s, db, _ := cloudAgentVisionFixture(t)
	root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("inspect-cat", "canvas_inspect_image", `{"nodeId":"cat"}`)
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	receipt := cloudAgentVisionAdvanceReceipt(t, s, root.ID, call.ID)
	if receipt["imagePending"] != true || receipt["imageAttached"] == true || stringField(receipt, "sha256") == "" {
		t.Fatalf("expected a stable text receipt for a prepared picture: %+v", receipt)
	}
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cloudAgentJSON(snapshot), `"image_url"`) || strings.Contains(cloudAgentJSON(snapshot.PiMessages), "resource:ref-one") {
		t.Fatal("the inspected image leaked into the worker transcript")
	}
	run, stepState := agentStartPiModelStep(t, s, root.ID)
	projected := piVisionTaskCanonical(t, s, run, stepState.ActiveTaskID)
	indexes := cloudAgentImageMessageIndexes(projected.Messages)
	if len(indexes) != 1 || !deliveredImageNodeIDs(projected)["cat"] {
		t.Fatal("the next model request after reloading the run did not deliver the staged picture")
	}
	parts, _ := projected.Messages[indexes[0]]["content"].([]any)
	caption, _ := parts[0].(map[string]any)
	attachment := cloudAgentImageReceipt(stringField(caption, "text"))
	if attachment["imageAttached"] != true || attachment["imagePending"] != nil || attachment["sha256"] != receipt["sha256"] {
		t.Fatalf("request caption must attest this attachment independently of the stable tool receipt: %+v", attachment)
	}
	assertCloudAgentToolCallPairing(t, projected.Messages)
	for _, message := range projected.Messages[indexes[0]+1:] {
		if stringField(message, cloudAgentContextSourceKey) != "runtime" {
			t.Fatal("the request-only image must follow the complete transcript")
		}
	}
	if stepState.DeliveredImageSHAs["cat"] != "" {
		t.Fatal("admission alone cannot attest that the model received the image")
	}
	summaryArgs, _ := json.Marshal(map[string]any{"nodeId": "cat", "sha256": receipt["sha256"], "summary": map[string]any{"short": "红色", "detailed": map[string]any{"color": "red"}}})
	save := piAgentTestCall("save-cat", "canvas_inspect_image", string(summaryArgs))
	agentStagePiOutputForTest(t, s, db, root.ID, "cat：红色。", []cloudAgentCall{save})
	if saved := cloudAgentVisionAdvanceReceipt(t, s, root.ID, save.ID); saved["summarySaved"] != true {
		t.Fatalf("expected the delivered SHA to be eligible for a saved summary: %+v", saved)
	}
	run, stepState = agentStartPiModelStep(t, s, root.ID)
	next := piVisionTaskCanonical(t, s, run, stepState.ActiveTaskID)
	if len(cloudAgentCanonicalImageKeys(&next)) != 0 || !strings.Contains(cloudAgentJSON(next.Messages), `summarySaved`) {
		t.Fatal("after saving an observation, the next step must use the text receipt without replaying pixels")
	}
	for idx, message := range projected.Messages[:indexes[0]] {
		if stringField(message, cloudAgentContextSourceKey) == "runtime" {
			break
		}
		if cloudAgentJSON(message) != cloudAgentJSON(next.Messages[idx]) {
			t.Fatalf("conversation prefix changed at message %d", idx)
		}
	}
}

func TestPiVisionComparisonDeliversAllOrNone(t *testing.T) {
	for _, tt := range []struct {
		name, note string
		maxImages  int
		unbind     bool
		want       int
	}{
		{name: "both", maxImages: 2, want: 2},
		{name: "channel-limit", maxImages: 1, note: "需同批发送 2 张"},
		{name: "unbound", maxImages: 2, unbind: true, note: "已更换、解绑或不可用"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, db, _ := cloudAgentVisionFixture(t)
			profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
			profile.Text.References.MaxImages = tt.maxImages
			if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
				t.Fatal(err)
			}
			root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
			if err != nil {
				t.Fatal(err)
			}
			calls := []cloudAgentCall{
				piAgentTestCall("compare-cat", "canvas_inspect_image", `{"nodeId":"cat","purpose":"compare"}`),
				piAgentTestCall("compare-hero", "canvas_inspect_image", `{"nodeId":"hero","purpose":"compare"}`),
			}
			agentSettlePiOutputForTest(t, s, db, root.ID, "", calls)
			if tt.unbind {
				canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
				if err != nil {
					t.Fatal(err)
				}
				// The resource stays bound elsewhere, while the compared node changes.
				payload := strings.Replace(canvas.PayloadJSON, "resource:ref-two", "resource:ref-one", 1)
				if err := db.Model(canvas).Update("payload_json", payload).Error; err != nil {
					t.Fatal(err)
				}
			}
			run, state := agentStartPiModelStep(t, s, root.ID)
			projected := piVisionTaskCanonical(t, s, run, state.ActiveTaskID)
			if got := len(cloudAgentCanonicalImageKeys(&projected)); got != tt.want {
				t.Fatalf("sent %d originals, want %d", got, tt.want)
			}
			if tt.note != "" && !strings.Contains(cloudAgentJSON(projected.Messages), tt.note) {
				t.Fatalf("missing delivery limitation %q", tt.note)
			}
			assertCloudAgentToolCallPairing(t, projected.Messages)
		})
	}
}

func TestPiVisionRetriesImageAfterFailedModelStep(t *testing.T) {
	s, db, _ := cloudAgentVisionFixture(t)
	root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("inspect-cat", "canvas_inspect_image", `{"nodeId":"cat"}`)
	agentSettlePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	run, first := agentStartPiModelStep(t, s, root.ID)
	if err := db.Model(&model.Task{}).Where("id = ?", first.ActiveTaskID).Updates(map[string]any{
		"status": model.TaskStatusFailed, "error": "Upstream stalled: no response data within the proxy timeout",
	}).Error; err != nil {
		t.Fatal(err)
	}
	decision, err := s.PiFailModelStepResult(run.UserID, run.ID, run.LeaseOwner, first.ActiveTaskID)
	if err != nil || decision.Status != "continue" {
		t.Fatalf("expected retry after relay stall: %+v, %v", decision, err)
	}
	run, retry := agentStartPiModelStep(t, s, root.ID)
	canonical := piVisionTaskCanonical(t, s, run, retry.ActiveTaskID)
	if retry.ActiveTaskID == first.ActiveTaskID || len(cloudAgentCanonicalImageKeys(&canonical)) != 1 || !deliveredImageNodeIDs(canonical)["cat"] {
		t.Fatal("retry must create a new model task with the same queued original")
	}
	if retry.cloudAgentImageInspectionCount("cat") != 0 || retry.DeliveredImageSHAs["cat"] != "" {
		t.Fatal("a failed or unconfirmed request must not advance the delivered image ledger")
	}
}

func piVisionTaskCanonical(t *testing.T, s *Service, run *model.CloudAgentExecution, taskID string) canonicalAgentRequest {
	t.Helper()
	task, err := s.repo.TaskForUser(run.UserID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input.AgentRequests == nil || input.AgentRequests.Canonical == nil {
		t.Fatal("missing model request")
	}
	return *input.AgentRequests.Canonical
}
