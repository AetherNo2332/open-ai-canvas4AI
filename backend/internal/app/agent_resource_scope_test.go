package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestAgentResourceScope(t *testing.T) {
	s, db, _ := cloudAgentVisionFixture(t)
	req := agentTestRequest()
	req.VisionEnabled = true
	snapshot, err := s.LoadAgentContextSnapshot(AgentContextScope{UserID: "user", Business: agentBusinessScope(req)}, AgentContextPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	state := cloudAgentRuntime{Request: req, Context: &snapshot}
	for _, row := range []any{
		&model.Task{ID: "private-task", UserID: "user", ProjectID: req.CanvasID, Type: "drama_analysis", ResultJSON: `{"text":"PRIVATE_TASK"}`},
		&model.Asset{ID: "private-asset", UserID: "user", Kind: "text", Title: "PRIVATE_ASSET", PayloadJSON: `{}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Run("task from another business with the same object ID", func(t *testing.T) {
		if _, err := cloudAgentReadTask(s.repo, "user", &state, piAgentTestCall("task", "task_get", `{"taskId":"private-task"}`)); err == nil {
			t.Fatal("object ID equality granted access to an unrelated task")
		}
	})
	t.Run("account library does not grant Agent access", func(t *testing.T) {
		value, err := cloudAgentReadTool(s.repo, "user", &state, piAgentTestCall("assets", "canvas_list_assets", `{}`), s)
		raw, _ := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), "PRIVATE_ASSET") {
			t.Fatalf("private asset visibility: %s, %v", raw, err)
		}
		if _, err := s.repo.AssetForUser("user", "private-asset"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("explicit canvas bindings are visible", func(t *testing.T) {
		asset := model.Asset{ID: "bound-asset", UserID: "user", Kind: "text", Title: "BOUND_ASSET", PayloadJSON: `{}`}
		if err := db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
		canvas, err := s.repo.CanvasProjectForUser("user", req.CanvasID)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := creationDocument(canvas.PayloadJSON)
		if err != nil {
			t.Fatal(err)
		}
		nodes := creationMaps(doc["nodes"])
		cloudAgentNodeMetadata(nodes[0])["assetId"] = asset.ID
		doc["nodes"] = mapsAsAny(nodes)
		raw, _ := json.Marshal(doc)
		if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
			t.Fatal(err)
		}
		visible, err := cloudAgentReadTool(s.repo, "user", &state, piAgentTestCall("assets", "canvas_list_assets", `{}`), s)
		raw, _ = json.Marshal(visible)
		if err != nil || !strings.Contains(string(raw), "BOUND_ASSET") || strings.Contains(string(raw), "PRIVATE_ASSET") {
			t.Fatalf("bound asset visibility: %s %v", raw, err)
		}
		bound := model.Task{ID: "bound-task", UserID: "user", ProjectID: req.CanvasID, Type: "canvas_text", ResultJSON: `{"text":"BOUND_TASK"}`}
		if err := db.Create(&bound).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.CanvasTaskBinding{TaskID: bound.ID, OwnerID: "user", CanvasID: req.CanvasID}).Error; err != nil {
			t.Fatal(err)
		}
		value, err := cloudAgentReadTask(s.repo, "user", &state, piAgentTestCall("task", "task_get", `{"taskId":"bound-task"}`))
		raw, _ = json.Marshal(value)
		if err != nil || !strings.Contains(string(raw), "BOUND_TASK") {
			t.Fatalf("bound task visibility: %s %v", raw, err)
		}
	})
	t.Run("model step rejects an unbound account resource", func(t *testing.T) {
		if err := db.Create(&model.Resource{ID: "private-image", UserID: "user", Status: model.ResourceStatusReady, MimeType: "image/png", Size: 80}).Error; err != nil {
			t.Fatal(err)
		}
		request := PiModelStepRequest{Canonical: canonicalAgentRequest{Messages: []map[string]any{{
			"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "resource:private-image"}}},
		}}}}
		_, _, err := s.piModelStepTaskInput(&model.CloudAgentExecution{UserID: "user"}, &state, &request)
		if err == nil || !strings.Contains(err.Error(), "当前业务未授权图片") {
			t.Fatalf("model step did not reject the resource at the scope boundary: %v", err)
		}
	})
	t.Run("replayed image history requires a current business binding", func(t *testing.T) {
		canvas, err := s.repo.CanvasProjectForUser("user", req.CanvasID)
		if err != nil {
			t.Fatal(err)
		}
		history := canonicalAgentRequest{Messages: []map[string]any{{
			"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "resource:ref-one"}}},
		}}}
		if refs, err := s.cloudAgentImageReferences("user", req, &history, true); err != nil || len(refs) != 1 {
			t.Fatalf("model input rejected the authorized image: %v %v", refs, err)
		}
		if err := db.Model(canvas).Update("payload_json", `{"nodes":[],"connections":[]}`).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := s.cloudAgentImageReferences("user", req, &history, true); err == nil || !strings.Contains(err.Error(), "当前业务未授权图片") {
			t.Fatalf("replayed image survived revocation: %v", err)
		}
	})
}
