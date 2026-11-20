package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestPiContinuesAfterHistoricalCanvasImageLosesBinding(t *testing.T) {
	const originalKey = "resource:original-image"
	for _, change := range []struct {
		name    string
		payload string
	}{
		{"replace image", `{"nodes":[{"id":"image-a","type":"image","metadata":{"storageKey":"resource:current-image","mediaVersions":[{"id":"v1","media":{"storageKey":"resource:original-image"}}]}},{"id":"image-b","type":"image","metadata":{"storageKey":"resource:current-image"}}]}`},
		{"delete node", `{"nodes":[{"id":"image-b","type":"image","metadata":{"storageKey":"resource:current-image"}}]}`},
	} {
		t.Run(change.name, func(t *testing.T) {
			s, db, currentKey := visionReadyService(t, 4)
			payload := strings.ReplaceAll(change.payload, "resource:current-image", currentKey)
			initial := `{"nodes":[{"id":"image-a","type":"image","metadata":{"storageKey":"resource:original-image"}},{"id":"image-b","type":"image","metadata":{"storageKey":"` + currentKey + `"}}]}`
			req := agentTestRequest()
			req.VisionEnabled = true
			if err := db.Create(&model.Resource{ID: "original-image", UserID: "user", Kind: "image", Status: "ready", MimeType: "image/png", Size: 512}).Error; err != nil {
				t.Fatal(err)
			}
			canvas := model.CanvasProject{ID: req.CanvasID, UserID: "user", PayloadJSON: initial}
			if err := db.Create(&canvas).Error; err != nil {
				t.Fatal(err)
			}
			root, err := s.CreateCloudAgentRun("user", req, "")
			if err != nil {
				t.Fatal(err)
			}
			completePiRunWithAssistantForTest(t, s, root.ID, "上一轮结果")
			session, entries, err := s.repo.CloudAgentPiSession("user", root.ID)
			if err != nil {
				t.Fatal(err)
			}
			history := cloudAgentImageContentParts(
				cloudAgentImageInspection{ImageURL: originalKey, Receipt: map[string]any{"nodeId": "image-a"}},
				cloudAgentImageInspection{ImageURL: currentKey, Receipt: map[string]any{"nodeId": "image-b"}},
			)
			entry := piCompactionTestEntry(t, "prior-image-read", session.ActiveLeafID, "message", map[string]any{
				"message": map[string]any{"role": "user", "content": "", "canvasContent": history},
			})
			entry.SessionID, entry.UserID, entry.RunID, entry.Sequence = session.ID, "user", root.ID, len(entries)+1
			if err := db.Create(&entry).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(session).Update("active_leaf_id", entry.EntryID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&canvas).Update("payload_json", payload).Error; err != nil {
				t.Fatal(err)
			}
			req.Prompt, req.IdempotencyKey = "继续分析", "continue-after-image-change"
			child, err := s.CreateCloudAgentRun("user", req, root.ID)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := s.ClaimPiAgent("worker-a")
			if err != nil || claimed == nil || claimed.RunID != child.ID {
				t.Fatalf("claim continuation: %+v %v", claimed, err)
			}
			run, state := reloadPiRun(t, s, child.ID)
			request, _ := piFirstStepRequest(state)
			request.Canonical.Messages = []map[string]any{
				{"role": "user", "content": history},
				{"role": "user", "content": req.Prompt},
			}
			before, _ := json.Marshal(request.Canonical.Messages)
			_, storedBefore, err := s.repo.CloudAgentPiSession("user", root.ID)
			if err != nil {
				t.Fatal(err)
			}

			untrusted := request
			untrusted.Canonical.Messages = visionCanonical(originalKey).Messages
			if _, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, untrusted); err == nil || !strings.Contains(err.Error(), "当前业务未授权图片") {
				t.Fatalf("unproven image must still be rejected: %v", err)
			}
			preflight, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, request)
			if err != nil || preflight.Decision != "model" {
				t.Fatalf("historical image must not block continuation: %+v %v", preflight, err)
			}
			step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request)
			if err != nil {
				t.Fatal(err)
			}
			task, err := s.repo.TaskForUser("user", step.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(task.InputJSON, originalKey) || !strings.Contains(task.InputJSON, currentKey) || !strings.Contains(task.InputJSON, req.Prompt) {
				t.Fatal("model task lost valid inputs or retained the revoked reference")
			}
			after, _ := json.Marshal(request.Canonical.Messages)
			_, storedAfter, err := s.repo.CloudAgentPiSession("user", root.ID)
			if err != nil || !reflect.DeepEqual(storedBefore, storedAfter) || string(before) != string(after) {
				t.Fatalf("model projection modified durable or supplied history: %v", err)
			}

			currentEntry := piCompactionTestEntry(t, "current-image-read", entry.EntryID, "message", map[string]any{
				"message": map[string]any{"role": "user", "content": "", "canvasContent": history},
			})
			currentEntry.SessionID, currentEntry.UserID, currentEntry.RunID, currentEntry.Sequence = session.ID, "user", child.ID, entry.Sequence+1
			if err := db.Create(&currentEntry).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(session).Update("active_leaf_id", currentEntry.EntryID).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := s.piModelStepImageReferences(run, state, &request.Canonical); err == nil || !strings.Contains(err.Error(), "当前业务未授权图片") {
				t.Fatalf("a matching current-run image must still be rejected: %v", err)
			}
		})
	}
}
