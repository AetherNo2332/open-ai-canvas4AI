package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func cameraPromptDoc(metadata map[string]any) map[string]any {
	return map[string]any{"nodes": []any{
		map[string]any{"id": "shot-1", "type": "text", "metadata": map[string]any{"content": "镜头"}},
		map[string]any{"id": "hero", "type": "image", "metadata": metadata},
		map[string]any{"id": "cat", "type": "image", "metadata": map[string]any{"status": "success"}},
	}}
}

func cameraArgs(mode string) cloudAgentMediaArgs {
	return cloudAgentMediaArgs{Mode: mode, Prompt: "基础提示词", SourceNodeID: "shot-1", ReferenceNodeIDs: []string{"hero", "cat"}}
}

func TestCloudAgentMediaCameraPromptSelection(t *testing.T) {
	enabled := func(prompt string) map[string]any {
		return map[string]any{
			"status": "success", "storageKey": "resource:ref-one",
			"cameraControl": map[string]any{"enabled": true, "camera": "arri_alexa_35", "lens": "cooke_s7i", "focalLength": 35.0, "aperture": 2.8},
			"cameraPrompt":  prompt,
		}
	}
	cases := []struct {
		name     string
		mode     string
		metadata map[string]any
		want     string
	}{
		{"compiled prompt wins", "image", enabled("Cinematic reference, shallow depth of field"), "Cinematic reference, shallow depth of field"},
		{"generic fallback without compiled prompt", "image", enabled(""), "镜头语言参考：arri_alexa_35 机身、cooke_s7i 镜头，35mm 焦段，f/2.8 光圈。"},
		{"disabled switch stays silent", "image", map[string]any{"cameraControl": map[string]any{"enabled": false, "camera": "arri_alexa_35", "lens": "cooke_s7i", "focalLength": 35.0, "aperture": 2.8}, "cameraPrompt": "ignored"}, ""},
		{"audio mode never appends", "audio", enabled("Cinematic"), ""},
		{"incomplete control stays silent", "image", map[string]any{"cameraControl": map[string]any{"enabled": true, "camera": "arri_alexa_35"}}, ""},
		{"missing nodes stay silent", "video", map[string]any{"status": "success"}, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := cloudAgentMediaCameraPrompt(cameraPromptDoc(testCase.metadata), cameraArgs(testCase.mode)); got != testCase.want {
				t.Fatalf("camera prompt = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestCloudAgentMediaCameraPromptPrefersSourceOverReferences(t *testing.T) {
	doc := map[string]any{"nodes": []any{
		map[string]any{"id": "shot-1", "type": "text", "metadata": map[string]any{
			"content":       "镜头",
			"cameraControl": map[string]any{"enabled": true, "camera": "arri_alexa_65", "lens": "arri_signature_prime", "focalLength": 65.0, "aperture": 4.0},
			"cameraPrompt":  "from source",
		}},
		map[string]any{"id": "hero", "type": "image", "metadata": map[string]any{
			"status":        "success",
			"cameraControl": map[string]any{"enabled": true, "camera": "red_komodo_6k", "lens": "cooke_s4", "focalLength": 50.0, "aperture": 2.0},
			"cameraPrompt":  "from reference",
		}},
	}}
	if got := cloudAgentMediaCameraPrompt(doc, cameraArgs("video")); got != "from source" {
		t.Fatalf("source node must win: %q", got)
	}
}

func TestCloudAgentMediaPrepareAppendsCameraPrompt(t *testing.T) {
	s, db, a := agentMediaFixture(t)
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == "shot-1" {
			metadata := cloudAgentPrevisNodeMetadata(node)
			metadata["cameraControl"] = map[string]any{"enabled": true, "camera": "arri_alexa_35", "lens": "cooke_s7i", "focalLength": 35.0, "aperture": 2.8}
			metadata["cameraPrompt"] = "Shot on ARRI ALEXA 35 with Cooke S7i, 35mm focal length, f/2.8 aperture reference."
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	a.SnapshotHash = creationHash(doc)
	run, state := agentMediaPiRun(t, s, db, a, "auto")
	request, _, err := s.prepareCloudAgentMedia(run, &state, agentMediaCall(a))
	if err != nil {
		t.Fatal(err)
	}
	prompt := stringValue(request.Input["prompt"])
	if !strings.Contains(prompt, "Shot on ARRI ALEXA 35 with Cooke S7i") {
		t.Fatalf("task input lost the camera direction: %q", prompt)
	}
	if request.Prompt != prompt {
		t.Fatalf("task request prompt diverged from input: %q vs %q", request.Prompt, prompt)
	}
}
