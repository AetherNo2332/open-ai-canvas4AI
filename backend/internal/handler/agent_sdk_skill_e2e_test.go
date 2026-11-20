package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestAgentSDKNativeSkillsHTTP(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for Skill Worker smoke")
	}
	agentDir, err := filepath.Abs(filepath.Join("..", "..", "..", "agent"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "dist", "src", "runner.js")); err != nil {
		t.Skip("build agent before Skill Worker smoke")
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "sdk-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("REDIS_URL", "")
	gin.SetMode(gin.TestMode)
	modelRequests := make(chan agentSDKSkillModelRequest, 16)
	upstream := httptest.NewServer(agentSDKSkillUpstream(modelRequests))
	defer upstream.Close()
	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	seedFourCapabilityChannels(t, db, upstream.URL)
	for _, row := range []any{
		&model.SystemSetting{Key: "inspiration_feed", ValueJSON: `{"enabled":false}`},
		&model.User{ID: "sdk-user", Username: "sdk-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "sdk-session", UserID: "sdk-user", TokenHash: auth.HashToken("sdk-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "sdk-user", AvailableMicrocredits: 100_000_000},
		&model.CanvasProject{ID: "sdk-canvas", UserID: "sdk-user", PayloadJSON: `{"nodes":[{"id":"board","type":"script"}]}`},
		&model.Project{ID: "sdk-drama", UserID: "sdk-user", Name: "SDK drama", Status: model.ProjectStatusActive},
		&model.DramaProduction{ProjectID: "sdk-drama", UserID: "sdk-user", Status: "paused", Stage: "script", SettingsJSON: agentSDKDramaSettings},
		&model.DramaEpisode{ID: "sdk-episode", ProjectID: "sdk-drama", Synopsis: "SDK Drama"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	manual, admin := seedAgentSDKScopedSkills(t, svc)
	previous := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() {
		runtimeService = previous
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.StopWorker(ctx); err != nil {
			t.Error(err)
		}
		_ = svc.Close()
	})
	svc.StartWorker()
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	_, lines := agentSDKStartWorker(t, ctx, node, agentDir, server.URL, "skills", false)
	agentSDKAwaitLine(t, lines, "READY")

	canvasReq := agentSDKSkillRunRequest("canvas", "SDK_NATIVE_READ")
	canvasReq.SkillIDs = []string{manual}
	canvas := agentSDKStartSkillRun(t, server, canvasReq).Run
	agentSDKWaitSkillRun(t, server, canvas.ID)
	assertAgentSDKSkillIndex(t, nextAgentSDKSkillModelRequest(t, modelRequests), []string{"SDK_INDEX_CANVAS", "SDK_INDEX_MANUAL"}, []string{"SDK_INDEX_DRAMA", "SDK_BODY_CANVAS", "SDK_BODY_MANUAL"})
	if next := nextAgentSDKSkillModelRequest(t, modelRequests); !next.ManualRead {
		t.Fatal("manual skill body did not reach the model as a tool result")
	}

	drama := agentSDKStartSkillRun(t, server, agentSDKSkillRunRequest("drama", "SDK_DRAMA_SKILLS")).Run
	agentSDKWaitSkillRun(t, server, drama.ID)
	assertAgentSDKSkillIndex(t, nextAgentSDKSkillModelRequest(t, modelRequests), []string{"SDK_INDEX_DRAMA"}, []string{"SDK_INDEX_CANVAS", "SDK_INDEX_MANUAL", "SDK_BODY_DRAMA"})

	if _, err := svc.ReplaceAgentSkillDefaults(admin, 1, nil); err != nil {
		t.Fatal(err)
	}
	send := service.AgentConversationMessageRequest{Mode: "queue", ExpectedRunID: canvas.ID, InheritRunConfiguration: true,
		Request: service.CloudAgentRequest{CanvasID: "sdk-canvas", IdempotencyKey: "sdk-storyboard-skills", Prompt: "先为原分镜表 board 规划 3 镜、每镜 10 秒。先向用户确认。", FocusNodeIDs: []string{"board"}}}
	var receipt service.AgentMessageReceipt
	agentSDKSkillHTTP(t, server, http.MethodPost, "/api/agent/conversations/"+canvas.ConversationID+"/messages", send, &receipt)
	if receipt.RunID == "" {
		t.Fatalf("storyboard delivery did not start: %+v", receipt)
	}
	agentSDKWaitSkillRun(t, server, receipt.RunID)
	assertAgentSDKSkillIndex(t, nextAgentSDKSkillModelRequest(t, modelRequests), []string{"SDK_INDEX_CANVAS", "SDK_INDEX_MANUAL"}, []string{"SDK_INDEX_DRAMA", "SDK_BODY_MANUAL"})
}

func seedAgentSDKScopedSkills(t *testing.T, svc *service.Service) (string, *model.User) {
	t.Helper()
	admin := &model.User{ID: "sdk-admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	var defaults []service.AgentSkillDefaultItem
	for _, item := range []struct{ scope, suffix string }{{"canvas", "CANVAS"}, {"drama", "DRAMA"}, {"", "MANUAL"}} {
		skill, err := svc.CreateSkill("sdk-user", service.SkillMutationRequest{
			SkillName: "SDK Skill " + item.suffix, Description: "SDK_INDEX_" + item.suffix, Instruction: "SDK_BODY_" + item.suffix,
			Tag: "others", IsPrivate: item.scope == "",
		})
		if err != nil {
			t.Fatal(err)
		}
		if item.scope == "" {
			if _, err := svc.ReplaceAgentSkillDefaults(admin, 0, defaults); err != nil {
				t.Fatal(err)
			}
			return skill.SkillID, admin
		}
		defaults = append(defaults, service.AgentSkillDefaultItem{SkillID: skill.SkillID, SkillVersionID: skill.VersionID, Scope: item.scope, Position: len(defaults), Enabled: 1})
	}
	t.Fatal("manual skill missing from fixture")
	return "", nil
}

func agentSDKSkillRunRequest(business, prompt string) service.CloudAgentRequest {
	req := service.CloudAgentRequest{CanvasID: "sdk-canvas", ChannelID: "sdk-text", ChannelModelKey: "text-e2e",
		PermissionMode: "read_only", Prompt: prompt, IdempotencyKey: prompt}
	req.Budget.MaxCredits, req.Budget.MaxSteps = 20, 12
	if business == "drama" {
		req.CanvasID, req.DramaEpisodeID = "", "sdk-episode"
		req.Scope = &service.AgentBusinessScope{Type: business, ID: "sdk-drama"}
	}
	return req
}

type agentSDKSkillRunReceipt struct {
	Run struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversationId"`
		Status         string `json:"status"`
		FailureMessage string `json:"failureMessage"`
	} `json:"run"`
}

func agentSDKStartSkillRun(t *testing.T, server *httptest.Server, input service.CloudAgentRequest) agentSDKSkillRunReceipt {
	t.Helper()
	var receipt agentSDKSkillRunReceipt
	agentSDKSkillHTTP(t, server, http.MethodPost, "/api/agent/runs", input, &receipt)
	return receipt
}

func agentSDKWaitSkillRun(t *testing.T, server *httptest.Server, id string) {
	t.Helper()
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		var result agentSDKSkillRunReceipt
		agentSDKSkillHTTP(t, server, http.MethodGet, "/api/agent/runs/"+id, nil, &result)
		switch result.Run.Status {
		case "completed":
			return
		case "failed", "cancelled", "rejected":
			t.Fatalf("skill run %s: %s: %s", id, result.Run.Status, result.Run.FailureMessage)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("timed out waiting for skill run", id)
}

func agentSDKSkillHTTP(t *testing.T, server *httptest.Server, method, path string, input, result any) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "sdk-session.sdk-token"})
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || envelope.Code != 0 {
		t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, envelope.Msg)
	}
	if err := json.Unmarshal(envelope.Data, result); err != nil {
		t.Fatal(err)
	}
}

type agentSDKSkillModelRequest struct {
	System     string
	ManualRead bool
}

func nextAgentSDKSkillModelRequest(t *testing.T, observed <-chan agentSDKSkillModelRequest) agentSDKSkillModelRequest {
	t.Helper()
	select {
	case request := <-observed:
		return request
	default:
		t.Fatal("completed Worker run did not produce the expected model request")
		return agentSDKSkillModelRequest{}
	}
}

var sdkManualSkillLocation = regexp.MustCompile(`(?s)<description>SDK_INDEX_MANUAL</description>\s*<location>(skills/[^<]+)</location>`)

func agentSDKSkillUpstream(observed chan<- agentSDKSkillModelRequest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var observation agentSDKSkillModelRequest
		var userPrompt string
		for _, message := range body.Messages {
			switch message.Role {
			case "system":
				var prompt string
				if err := json.Unmarshal(message.Content, &prompt); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				observation.System += prompt
			case "user":
				if !strings.Contains(string(message.Content), "【运行状态】") {
					userPrompt = string(message.Content)
				}
			case "tool":
				observation.ManualRead = observation.ManualRead || strings.Contains(string(message.Content), "SDK_BODY_MANUAL")
			}
		}
		observed <- observation
		delta, finish := map[string]any{"content": "SDK Skill verified"}, "stop"
		if strings.Contains(userPrompt, "SDK_NATIVE_READ") && !observation.ManualRead {
			if matched := sdkManualSkillLocation.FindStringSubmatch(observation.System); len(matched) == 2 {
				delta, finish = agentSDKToolDelta("read", fmt.Sprintf(`{"path":%q}`, matched[1])), "tool_calls"
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		end, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
			"usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 10, "total_tokens": 40}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", end)
	})
}

func assertAgentSDKSkillIndex(t *testing.T, observed agentSDKSkillModelRequest, present, absent []string) {
	t.Helper()
	if !strings.Contains(observed.System, "<available_skills>") {
		t.Fatal("model request lacks the Native Skill index")
	}
	for _, marker := range present {
		if !strings.Contains(observed.System, marker) {
			t.Errorf("model index lacks %s", marker)
		}
	}
	for _, marker := range absent {
		if strings.Contains(observed.System, marker) {
			t.Errorf("model index unexpectedly contains %s", marker)
		}
	}
}
