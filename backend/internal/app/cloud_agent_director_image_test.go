package app

import "testing"

func TestCloudAgentDirectorImageFlowKeepsInspectionSeparateFromSceneWrites(t *testing.T) {
	req := CloudAgentRequest{ContextScope: []string{"canvas"}, PermissionMode: "request_approval", VisionEnabled: true}
	tools := compileCloudAgentTools(req, false)
	seenInspection := false
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if stringField(fn, "name") == "canvas_inspect_image" {
			seenInspection = true
		}
	}
	if !seenInspection {
		t.Fatal("image-capable canvas runs must expose canvas_inspect_image")
	}
	if cloudAgentDirectorWriteTool("canvas_inspect_image") {
		t.Fatal("image inspection must not be classified as a scene write")
	}
	call := cloudAgentCall{ID: "image-flow"}
	call.Function.Name = "canvas_inspect_image"
	call.Function.Arguments = `{"nodeId":"image-1"}`
	if _, err := cloudAgentDirectorUnderlyingCall(call); err == nil {
		t.Fatal("image inspection must remain outside director scene compilation")
	}
}
