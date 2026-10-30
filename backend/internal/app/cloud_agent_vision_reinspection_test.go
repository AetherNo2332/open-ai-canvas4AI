package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentVisionRefreshAfterSummarySendsStoredPixels(t *testing.T) {
	s, _, pixels := cloudAgentVisionFixture(t)
	state := cloudAgentRuntime{Request: agentTestRequest()}
	state.Request.VisionEnabled = true
	firstResult, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state,
		cloudAgentStoryboardCall(t, "canvas_inspect_image", "first", map[string]any{"nodeId": "cat"}))
	if err != nil {
		t.Fatal(err)
	}
	first := firstResult.(cloudAgentImageInspection)
	state.markCanvasImageInspection("cat", true, stringValue(first.Receipt["contentSignature"]))
	state.ImageInspectionReads = map[string]int{first.CacheKey: 1}
	canonical := canonicalAgentRequest{ToolChoice: "auto", Messages: []map[string]any{{"role": "user", "content": cloudAgentImageContentParts(first)}}}
	state.cloudAgentRecordImageDeliverySHA(canonical)
	summaryResult, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state,
		cloudAgentStoryboardCall(t, "canvas_inspect_image", "save", map[string]any{
			"nodeId": "cat", "sha256": first.ResourceSHA,
			"summary": map[string]any{"short": "red pixel", "detailed": map[string]any{"subjects": []any{"pixel"}}},
		}))
	if err != nil {
		t.Fatal(err)
	}
	saved := summaryResult.(cloudAgentImageInspection)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.persistCloudAgentVisionCache(s.repo, "user", "agent-canvas", saved, policy); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(saved.Receipt)
	savedArguments, _ := json.Marshal(map[string]any{"nodeId": "cat", "sha256": first.ResourceSHA, "summary": saved.Summary})
	canonical.Messages = append(canonical.Messages,
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
			"id": "save", "type": "function", "function": map[string]any{"name": "canvas_inspect_image", "arguments": string(savedArguments)},
		}}},
		map[string]any{"role": "tool", "tool_call_id": "save", "content": string(encoded)},
	)
	cachedResult, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state,
		cloudAgentStoryboardCall(t, "canvas_inspect_image", "cached", map[string]any{"nodeId": "cat"}))
	if err != nil {
		t.Fatal(err)
	}
	cached := cachedResult.(cloudAgentImageInspection)
	if cached.ImageURL != "" || cached.Receipt["repeat"] != true || cached.Receipt["imageAttached"] != false {
		t.Fatalf("ordinary read must explicitly identify cached text without claiming pixels: %+v", cached.Receipt)
	}
	state.markCanvasImageInspection("cat", false, "")
	result, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state,
		cloudAgentStoryboardCall(t, "canvas_inspect_image", "reinspect", map[string]any{"nodeId": "cat", "refresh": true}))
	if err != nil {
		t.Fatal(err)
	}
	reinspect := result.(cloudAgentImageInspection)
	if reinspect.ImageURL == "" || reinspect.Receipt["imageAttached"] != true || reinspect.Receipt["repeat"] != true || reinspect.Receipt["imageChanged"] != false {
		t.Fatalf("saved summary must allow explicit real-image reinspection with repeat marker: %+v", reinspect.Receipt)
	}
	canonical.Messages = append(canonical.Messages, map[string]any{"role": "user", "content": cloudAgentImageContentParts(reinspect)})
	before, _ := json.Marshal(canonical)
	projected, batch := projectCloudAgentVisionRequest(canonical, state.VisionBatchBudget, state.DeliveredImageSHAs, state.DeliveredImageComparisons)
	if len(batch) != 1 || stringValue(batch[0].Receipt["comparisonId"]) == "" {
		t.Fatalf("new explicit read was replaced by its old summary or delivery: %+v", batch)
	}
	refs, err := s.cloudAgentImageReferences("user", state.Request, &projected)
	if err != nil || len(refs) != 1 {
		t.Fatalf("reinspection did not produce an authorized model image: refs=%+v err=%v", refs, err)
	}
	input := canvasGenerationInput{
		Mode: "text", ReferenceImages: refs,
		AgentRequests: &agentToolRequests{Canonical: &projected},
		Config:        providerConfig{Model: "text-test", InterfaceType: string(model.ChannelInterfaceChatCompletion), APIKey: "test-only"},
	}
	if err := s.hydrateGenerationMedia("user", &input, providerMediaHydrationPolicy{}); err != nil {
		t.Fatal(err)
	}
	if input.ReferenceImages[0].DataURL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(pixels) {
		t.Fatal("reinspection must hydrate the exact stored bytes, not observation text")
	}
	resolved, err := resolveAgentResourcePlaceholders(input, true)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := expandCanonicalAgentRequest(resolved.AgentRequests.Canonical, input.Config, true)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]any{"chat": expanded.ChatCompletion, "responses": expanded.Responses, "claude": expanded.Claude, "gemini": expanded.Gemini} {
		raw, _ := json.Marshal(body)
		if strings.Count(string(raw), base64.StdEncoding.EncodeToString(pixels)) != 1 || strings.Contains(string(raw), "resource:") {
			t.Errorf("%s must receive exactly one real reinspection image", name)
		}
	}
	state.cloudAgentRecordImageDeliverySHA(projected)
	_, nextBatch := projectCloudAgentVisionRequest(canonical, state.VisionBatchBudget, state.DeliveredImageSHAs, state.DeliveredImageComparisons)
	if len(nextBatch) != 0 {
		t.Fatal("one explicit read must not force the same pixels into every following model request")
	}
	after, _ := json.Marshal(canonical)
	if string(before) != string(after) {
		t.Fatal("model projection mutated the durable image and summary records")
	}
	state.markCanvasImageInspection("cat", true, stringValue(reinspect.Receipt["contentSignature"]))
	limited, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", &state,
		cloudAgentStoryboardCall(t, "canvas_inspect_image", "third", map[string]any{"nodeId": "cat", "refresh": true}))
	if err != nil {
		t.Fatal(err)
	}
	if blocked := limited.(cloudAgentImageInspection); blocked.ImageURL != "" || blocked.Receipt["imageAttached"] != false || blocked.Receipt["repeat"] != true {
		t.Fatalf("explicit refresh must retain the per-image limit: %+v", blocked.Receipt)
	}
}

func TestVisionProjectionAdmitsSingleImageRefreshAfterSavedSummary(t *testing.T) {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	image := cloudAgentImageInspection{ResourceSHA: sha, ImageURL: "resource:cat", Receipt: map[string]any{
		"nodeId": "cat", "sha256": sha, "purpose": "inspect", "forceImage": true,
		"comparisonId": "step:refresh", "repeat": true, "imageAttached": true,
	}}
	canonical := canonicalAgentRequest{Messages: []map[string]any{
		{"role": "tool", "content": `{"nodeId":"cat","sha256":"` + sha + `","imageAttached":false,"visionCache":{"short":"old observation"}}`},
		{"role": "user", "content": cloudAgentImageContentParts(image)},
	}}
	_, batch := projectCloudAgentVisionRequest(canonical, cloudAgentVisionBatchBudget{MaxImages: 1, MaxCost: 16}, map[string]string{"cat": sha}, nil)
	if len(batch) != 1 || batch[0].ImageURL != image.ImageURL {
		t.Fatalf("inspect refresh must bypass saved summary and old SHA delivery: %+v", batch)
	}
}
