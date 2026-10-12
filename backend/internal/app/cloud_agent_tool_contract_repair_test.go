package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func repairToolParameters(t *testing.T, name string) map[string]any {
	t.Helper()
	req := agentTestRequest()
	req.PermissionMode, req.VisionEnabled = "auto", true
	for _, tool := range cloudAgentTools(req) {
		f := tool["function"].(map[string]any)
		if f["name"] == name {
			return f["parameters"].(map[string]any)
		}
	}
	t.Fatalf("missing tool %s", name)
	return nil
}

func TestPiModelStepPersistsOnlyAdmittedImageSHAs(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 64000, 8192)
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	profile.Text.ContextWindowTokens, profile.Text.MaxOutputTokens = 64000, 8192
	profile.Text.References.MaxImages = 1
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
		t.Fatal(err)
	}
	const resourceID = "11112222333344445555666677778888"
	if err := db.Create(&model.Resource{ID: resourceID, UserID: "user", Kind: "image", Status: "ready", MimeType: "image/png", Size: 12}).Error; err != nil {
		t.Fatal(err)
	}
	message := func(nodeID, sha string) map[string]any {
		return map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": `{"bytes":12,"nodeId":"` + nodeID + `","sha256":"` + sha + `"}`},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "resource:" + resourceID}},
		}}
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Canonical.Messages = append(state.Canonical.Messages, message("excluded", strings.Repeat("b", 64)))
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, state := reloadPiRun(t, s, run.ID)
	request, _ := piFirstStepRequest(state)
	request.Canonical.Messages = []map[string]any{message("delivered", strings.Repeat("a", 64))}
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request); err != nil {
		t.Fatal(err)
	}
	_, saved := reloadPiRun(t, s, run.ID)
	if saved.DeliveredImageSHAs["delivered"] != strings.Repeat("a", 64) || saved.DeliveredImageSHAs["excluded"] != "" {
		t.Fatalf("delivery must be persisted from admitted envelope: %+v", saved.DeliveredImageSHAs)
	}
}

func TestCloudAgentToolContractRepairConditions(t *testing.T) {
	cases := []struct {
		name, tool, raw string
		valid           bool
	}{
		{"question_example", "ask_user", `{"question":"style?","options":[{"label":"real"},{"label":"anime","detail":"drawn"}]}`, true},
		{"question_string", "ask_user", `{"question":"style?","options":"real,anime"}`, false},
		{"plan_example", "plan_update", `{"items":[{"id":"1","title":"read","status":"doing"}]}`, true},
		{"annotation_example", "image_annotation_render", `{"nodeId":"n","annotations":[{"x":0.5,"y":0.5,"label":"edit"}]}`, true},
		{"image", "generate_media", `{"mode":"image","prompt":"cat","nodeId":"n","title":"cat","referenceNodeIds":[],"size":"1:1"}`, true},
		{"missing_size", "generate_media", `{"mode":"image","prompt":"cat","nodeId":"n","title":"cat","referenceNodeIds":[]}`, false},
		{"video", "generate_media", `{"mode":"video","prompt":"cat","nodeId":"n","title":"cat","referenceNodeIds":[],"size":"16:9","durationSeconds":5}`, true},
		{"video_zero_duration", "generate_media", `{"mode":"video","prompt":"cat","nodeId":"n","title":"cat","referenceNodeIds":[],"size":"16:9","durationSeconds":0}`, false},
		{"audio", "generate_media", `{"mode":"audio","prompt":"cat","nodeId":"n","title":"cat","referenceNodeIds":[]}`, true},
		{"image_audio_flag", "generate_media", `{"mode":"image","prompt":"cat","nodeId":"n","title":"cat","referenceNodeIds":[],"size":"1:1","videoGenerateAudio":false}`, false},
		{"storyboard_missing_plot", "canvas_create_storyboard", `{"snapshotHash":"h","nodeId":"n","title":"s","rows":[{"durationSeconds":5}]}`, false},
		{"storyboard_plot", "canvas_create_storyboard", `{"snapshotHash":"h","nodeId":"n","title":"s","rows":[{"durationSeconds":5,"plotDescription":"cat"}]}`, true},
		{"append_missing_duration", "canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"n","action":"append","patch":{"plotDescription":"cat"}}`, false},
		{"update", "canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"n","action":"update","rowId":"r","patch":{"dialogue":"hello"}}`, true},
		{"remove_patch", "canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"n","action":"remove","rowId":"r","patch":{"dialogue":"hello"}}`, false},
		{"remove", "canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"n","action":"remove","rowId":"r"}`, true},
		{"concurrency_missing", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"set_concurrency"}`, false},
		{"concurrency", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"set_concurrency","concurrency":5}`, true},
		{"concurrency_patch", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"set_concurrency","concurrency":5,"patch":{"enabled":true}}`, false},
		{"column", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"add_reference_column"}`, true},
		{"remove_column", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"remove_reference_column"}`, true},
		{"operation", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"set_operation","operation":"creative"}`, true},
		{"append_table", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"append"}`, true},
		{"update_table", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"update","rowId":"r","patch":{"prompt":"cat"}}`, true},
		{"remove_table", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"remove","rowId":"r"}`, true},
		{"bad_concurrency", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"set_concurrency","concurrency":2}`, false},
		{"clear_global_prompt", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"set_global_prompt","globalPrompt":""}`, true},
		{"global_prompt_missing", "canvas_edit_batch_table", `{"snapshotHash":"h","nodeId":"n","action":"set_global_prompt"}`, false},
		{"summary_shape", "canvas_inspect_image", `{"nodeId":"n","sha256":"` + strings.Repeat("a", 64) + `","summary":{"short":"cat","detailed":{"subjects":["cat"]}}}`, true},
		{"summary_missing_sha", "canvas_inspect_image", `{"nodeId":"n","summary":{"short":"cat","detailed":{}}}`, false},
		{"summary_bad_subjects", "canvas_inspect_image", `{"nodeId":"n","sha256":"` + strings.Repeat("a", 64) + `","summary":{"short":"cat","detailed":{"subjects":"cat"}}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCloudAgentToolArguments(repairToolParameters(t, c.tool), c.raw)
			if (err == nil) != c.valid {
				t.Fatalf("valid=%v, err=%v", c.valid, err)
			}
		})
	}
}

func TestCloudAgentVisionSummarySubmissionBypassesReadGuard(t *testing.T) {
	s, _, _ := cloudAgentVisionFixture(t)
	state := cloudAgentRuntime{Request: agentTestRequest()}
	state.Request.VisionEnabled = true
	first, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state, cloudAgentStoryboardCall(t, "canvas_inspect_image", "first", map[string]any{"nodeId": "cat"}))
	if err != nil {
		t.Fatal(err)
	}
	image := first.(cloudAgentImageInspection)
	state.ImageInspectionReads = map[string]int{image.CacheKey: 1}
	// Persist the model request's actual image envelope before submitting its observation.
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": cloudAgentImageContentParts(image)}}
	summary := map[string]any{"short": "red pixel", "detailed": map[string]any{"subjects": []any{"pixel"}}}
	call := cloudAgentStoryboardCall(t, "canvas_inspect_image", "save", map[string]any{"nodeId": "cat", "sha256": image.ResourceSHA, "summary": summary})
	if _, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state, call); err == nil {
		t.Fatal("durable history alone cannot prove that an image reached the model")
	}
	state.cloudAgentRecordImageDeliverySHA(state.Canonical)
	result, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state, call)
	if err != nil {
		t.Fatal(err)
	}
	saved := result.(cloudAgentImageInspection)
	if saved.ImageURL != "" || saved.Summary == nil || saved.Receipt["summarySaved"] != true {
		t.Fatalf("invalid save receipt: %+v", saved)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.persistCloudAgentVisionCache(s.repo, "user", "agent-canvas", saved, policy); err != nil {
		t.Fatal(err)
	}
	again, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state, cloudAgentStoryboardCall(t, "canvas_inspect_image", "cached", map[string]any{"nodeId": "cat"}))
	if err != nil {
		t.Fatal(err)
	}
	if cached := again.(cloudAgentImageInspection); cached.ImageURL != "" || cached.Receipt["visionCache"] == nil {
		t.Fatalf("not cached: %+v", cached)
	}
	state.Canonical.Messages = nil
	state.DeliveredImageSHAs = nil
	if _, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state, call); err == nil {
		t.Fatal("accepted an observation without a delivered image")
	}
	args := map[string]any{"nodeId": "cat", "sha256": strings.Repeat("a", 64), "summary": summary}
	raw, _ := json.Marshal(args)
	call.Function.Arguments = string(raw)
	if _, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state, call); err == nil {
		t.Fatal("accepted stale SHA")
	}
}
