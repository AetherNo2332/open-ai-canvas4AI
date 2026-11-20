package app

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

func TestAgentConversationScope(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	for _, row := range []any{
		&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`},
		&model.CanvasProject{ID: "other-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`},
		&model.Project{ID: "agent-canvas", UserID: "user", Name: "Drama", Status: model.ProjectStatusActive},
		&model.DramaProduction{ProjectID: "agent-canvas", UserID: "user", Status: "paused", Stage: "script"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	canvas, err := s.ResolveAgentDefinition("canvas", "")
	if err != nil {
		t.Fatal(err)
	}
	drama, err := s.ResolveAgentDefinition("drama", "")
	if err != nil {
		t.Fatal(err)
	}
	alternate := canvas
	alternate.ID, alternate.IsDefault = "canvas.alternate", false
	s.agentDefinitions, err = newAgentDefinitionRegistry([]AgentDefinition{canvas, alternate, drama})
	if err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	otherObject := req
	otherObject.CanvasID = "other-canvas"
	otherDefinition := req
	otherDefinition.AgentDefinitionID = alternate.ID
	dramaReq := req
	dramaReq.CanvasID, dramaReq.ContextScope = "", nil
	dramaReq.Scope = &AgentBusinessScope{Type: "drama", ID: req.CanvasID}
	dramaReq.IdempotencyKey = "drama-scope"
	for _, attempt := range []struct {
		name, user string
		request    CloudAgentRequest
	}{
		{"cross-owner", "other", req},
		{"cross-object", "user", otherObject},
		{"cross-business", "user", dramaReq},
		{"cross-definition", "user", otherDefinition},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			attempt.request.IdempotencyKey = "invalid-message-" + attempt.name
			_, err := s.SendAgentConversationMessage(attempt.user, run.ConversationID, AgentConversationMessageRequest{
				Mode: "interject", ExpectedRunID: run.ID, Request: attempt.request,
			})
			if err == nil {
				t.Fatal("accepted message from outside the conversation identity")
			}
		})
	}
	t.Run("short drama Pi header records its actual scope", func(t *testing.T) {
		dramaRun, err := s.CreateCloudAgentRun("user", dramaReq, "")
		if err != nil {
			t.Fatal(err)
		}
		session, _, err := s.repo.CloudAgentPiSession("user", dramaRun.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		var header struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(session.HeaderJSON), &header); err != nil || header.Cwd != "drama://agent-canvas" {
			t.Fatalf("wrong short drama session cwd %q: %v", header.Cwd, err)
		}
		selector := AgentConversationSelector{Scope: *dramaReq.Scope}
		page, err := s.ScopedCloudAgentConversations("user", selector, "", 30)
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != dramaRun.ConversationID || !page.Items[0].Resumable {
			t.Fatalf("scoped directory did not return only the drama conversation: %+v %v", page, err)
		}
		for _, target := range []struct {
			id           string
			conversation bool
		}{{run.ID, false}, {run.ConversationID, true}} {
			if err := s.RequireAgentTargetScope("user", target.id, target.conversation, selector); err == nil {
				t.Fatalf("accepted canvas target as drama: %+v", target)
			}
		}
		if err := s.RequireAgentTargetScope("user", dramaRun.ID, false, selector); err != nil {
			t.Fatal(err)
		}
		dramaReq.IdempotencyKey = "independent-drama-conversation"
		dramaReq.Prompt = "第二个独立会话"
		other, err := s.CreateCloudAgentRun("user", dramaReq, "")
		if err != nil {
			t.Fatal(err)
		}
		history, err := s.CloudAgentConversationRuns("user", other.ConversationID, "", 30)
		if err != nil || len(history.Items) != 1 || history.Items[0].ID != other.ID || other.ConversationID == dramaRun.ConversationID {
			t.Fatalf("separate conversations were merged: %+v %v", history, err)
		}
	})
}
