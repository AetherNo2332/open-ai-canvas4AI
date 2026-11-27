package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

// Only the provider boundary is deterministic. Real HTTP handlers, Go task
// execution/billing, Node's Pi runner, tools, checkpoints and dispatch are used.
func TestAgentGenerationContinuationRealPiDynamicBatch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node required:", err)
	}
	agentDir, err := filepath.Abs(filepath.Join("..", "..", "..", "agent"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "dist", "src", "runner.js")); err != nil {
		t.Fatal("build agent first:", err)
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "sdk-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("REDIS_URL", "")
	gin.SetMode(gin.TestMode)
	const n = 3 // Fixture choice only; the product barrier comes from registered rows.
	provider := newContinuationProvider(n)
	provider.t = t
	upstream := httptest.NewServer(provider)
	defer upstream.Close()
	defer provider.releaseAll()
	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	seedFourCapabilityChannels(t, db, upstream.URL)
	for _, row := range []any{
		&model.User{ID: "sdk-user", Username: "sdk-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "sdk-session", UserID: "sdk-user", TokenHash: auth.HashToken("sdk-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "sdk-user", AvailableMicrocredits: 100_000_000},
		&model.SystemSetting{Key: "inspiration_feed", ValueJSON: `{"enabled":false}`},
		&model.CanvasProject{ID: "sdk-canvas", UserID: "sdk-user", PayloadJSON: `{"referenceFieldsVersion":1,"nodes":[],"connections":[]}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := repository.New(db)
	svc := service.New(repo, t.TempDir())
	previous := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() {
		runtimeService = previous
		provider.releaseAll()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	worker, lines := agentSDKStartWorker(t, ctx, node, agentDir, server.URL, "continuation-old", false)
	agentSDKAwaitLine(t, lines, "READY")
	req := agentSDKSkillRunRequest("canvas", "CONTINUATION_SOURCE")
	req.PermissionMode = "auto"
	req.ContextScope = []string{"canvas"}
	req.Budget.MaxGenerationTasks = n
	req.Budget.MaxSteps = 24
	source := agentSDKStartSkillRun(t, server, req).Run
	awaitContinuation(t, "source finish_run", func() bool {
		run, err := repo.CloudAgent("sdk-user", source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == "failed" {
			t.Fatalf("source failed: %s", run.FailureMessage)
		}
		return run.Status == "completed"
	})
	batch, err := repo.AgentGenerationBatchBySourceRun("sdk-user", source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchArmed || batch.ExpectedTasks != n || batch.TerminalTasks != 0 {
		t.Fatalf("source did not finish with dynamic armed batch: %+v", batch)
	}
	provider.mu.Lock()
	receipts := append([]continuationSubmission(nil), provider.receipts...)
	provider.mu.Unlock()
	if len(receipts) != n {
		t.Fatalf("submission receipts: %+v", receipts)
	}
	for i, r := range receipts {
		if r.BatchID != batch.ID || r.Count != i+1 || r.TaskID == "" {
			t.Fatalf("receipt %d: %+v batch=%s", i, r, batch.ID)
		}
	}
	// Lose the Pi process after source completion, while durable generation work remains.
	if err := worker.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = worker.Wait()
	_, lines = agentSDKStartWorker(t, ctx, node, agentDir, server.URL, "continuation-new", false)
	agentSDKAwaitLine(t, lines, "READY")
	provider.release(0)
	awaitContinuation(t, "partial terminal task", func() bool {
		_, err := svc.SettlePendingCloudAgentGenerations()
		if err != nil {
			t.Fatal(err)
		}
		current, err := repo.AgentGenerationBatch("sdk-user", batch.ID)
		if err != nil {
			t.Fatal(err)
		}
		return current.TerminalTasks == 1
	})
	current, err := repo.AgentGenerationBatch("sdk-user", batch.ID)
	if err != nil || current.Status != model.AgentGenerationBatchArmed || current.ContinuationRunID != "" || current.QueuedMessageID != "" {
		t.Fatalf("partial results triggered continuation: %+v %v", current, err)
	}
	send := service.AgentConversationMessageRequest{Mode: "queue", ExpectedRunID: source.ID, InheritRunConfiguration: true,
		Request: service.CloudAgentRequest{CanvasID: "sdk-canvas", Prompt: "CONTINUATION_USER_PRIORITY", IdempotencyKey: "continuation-user"}}
	var delivery service.AgentMessageReceipt
	agentSDKSkillHTTP(t, server, http.MethodPost, "/api/agent/conversations/"+source.ConversationID+"/messages", send, &delivery)
	if delivery.RunID == "" {
		t.Fatalf("user message did not start while generation pending: %+v", delivery)
	}
	select {
	case <-provider.userStarted:
	case <-time.After(30 * time.Second):
		t.Fatal("user model step did not start")
	}
	for i := 1; i < n; i++ {
		provider.release(i)
	}
	awaitContinuation(t, "queued behind active user", func() bool {
		if _, err := svc.SettlePendingCloudAgentGenerations(); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DispatchAgentMessages(20); err != nil {
			t.Fatal(err)
		}
		b, err := repo.AgentGenerationBatch("sdk-user", batch.ID)
		if err != nil {
			t.Fatal(err)
		}
		return b.Status == model.AgentGenerationBatchQueued
	})
	for i := 0; i < 3; i++ {
		if _, err := svc.DispatchAgentMessages(20); err != nil {
			t.Fatal(err)
		}
	}
	assertContinuationCounts(t, db, source.ConversationID, 2, 1)
	provider.releaseUser()
	agentSDKWaitSkillRun(t, server, delivery.RunID)
	awaitContinuation(t, "one continuation run", func() bool {
		b, err := repo.AgentGenerationBatch("sdk-user", batch.ID)
		if err != nil {
			t.Fatal(err)
		}
		current = b
		return b.Status == model.AgentGenerationBatchConsumed
	})
	agentSDKWaitSkillRun(t, server, current.ContinuationRunID)
	if provider.continuations.Load() != 1 {
		t.Fatalf("continuation model executions=%d", provider.continuations.Load())
	}
	var beforeOrders int64
	if err := db.Model(&model.BillingOrder{}).Count(&beforeOrders).Error; err != nil {
		t.Fatal(err)
	}
	account, err := repo.CreditAccount("sdk-user")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.SettlePendingCloudAgentGenerations(); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DispatchAgentMessages(20); err != nil {
			t.Fatal(err)
		}
	}
	assertContinuationCounts(t, db, source.ConversationID, 3, 1)
	var orders int64
	if err := db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	after, err := repo.CreditAccount("sdk-user")
	if err != nil {
		t.Fatal(err)
	}
	if orders != beforeOrders || after.AvailableMicrocredits != account.AvailableMicrocredits || after.ReservedMicrocredits != account.ReservedMicrocredits {
		t.Fatalf("replay changed billing: orders=%d/%d account=%+v/%+v", orders, beforeOrders, account, after)
	}
	if provider.images.Load() != n {
		t.Fatalf("paid generation replay: calls=%d expected=%d", provider.images.Load(), n)
	}
	if !provider.resultsOK.Load() {
		t.Fatal("continuation did not receive all outcomes and failure instruction")
	}
	t.Logf("real Pi dynamic batch: N=%d receipts=%d same batch; source completed; partial terminal blocked; user run first; Pi restarted armed; internal messages=1 runs=3 continuation=1 image calls=%d billing orders=%d unchanged on replay", n, len(receipts), provider.images.Load(), orders)
}

func awaitContinuation(t *testing.T, label string, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out:", label)
}
func assertContinuationCounts(t *testing.T, db *gorm.DB, conversation string, wantRuns, wantMessages int64) {
	t.Helper()
	var runs, messages int64
	if err := db.Model(&model.CloudAgentExecution{}).Where("conversation_id = ?", conversation).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AgentQueuedMessage{}).Where("conversation_id = ? AND kind = ?", conversation, model.AgentQueuedMessageGenerationContinuation).Count(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if runs != wantRuns || messages != wantMessages {
		t.Fatalf("conversation counts runs=%d/%d messages=%d/%d", runs, wantRuns, messages, wantMessages)
	}
}

type continuationSubmission struct {
	BatchID string `json:"generationBatchId"`
	Count   int    `json:"batchTaskCount"`
	TaskID  string `json:"taskId"`
}
type continuationProvider struct {
	t             *testing.T
	mu            sync.Mutex
	gates         []chan struct{}
	receipts      []continuationSubmission
	userStarted   chan struct{}
	userGate      chan struct{}
	userOnce      sync.Once
	images        atomic.Int32
	continuations atomic.Int32
	resultsOK     atomic.Bool
}

func newContinuationProvider(n int) *continuationProvider {
	p := &continuationProvider{userStarted: make(chan struct{}), userGate: make(chan struct{})}
	for range n {
		p.gates = append(p.gates, make(chan struct{}))
	}
	return p
}
func (p *continuationProvider) release(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.gates[i]:
	default:
		close(p.gates[i])
	}
}
func (p *continuationProvider) releaseUser() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.userGate:
	default:
		close(p.userGate)
	}
}
func (p *continuationProvider) releaseAll() {
	for i := range p.gates {
		p.release(i)
	}
	p.releaseUser()
}
func (p *continuationProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
		Tools  []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
			Name    string `json:"name"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.Contains(r.URL.Path, "images/generations") {
		p.images.Add(1)
		var index int
		if _, err := fmt.Sscanf(body.Prompt, "CONTINUATION_IMAGE_%d", &index); err != nil || index < 0 || index >= len(p.gates) {
			http.Error(w, "unexpected image", 400)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-p.gates[index]:
		}
		// Every fixture task fails deterministically, exercising terminal failures
		// without real media storage or any call to a paid provider.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"CONTINUATION_EXPECTED_FAILURE","type":"invalid_request_error"}}`)
		return
	}
	lastUser := ""
	var receipts []continuationSubmission
	for _, message := range body.Messages {
		raw, _ := json.Marshal(message.Content)
		text := strings.ReplaceAll(string(raw), `\"`, `"`)
		if message.Role == "user" && (strings.Contains(text, "CONTINUATION_SOURCE") || strings.Contains(text, "CONTINUATION_USER_PRIORITY") || strings.Contains(text, "[系统生成结果]")) {
			lastUser = text
		}
		if message.Role == "tool" && strings.Contains(text, "Tool generate_media not found") {
			p.t.Errorf("generate_media missing from Pi tool registry")
		}
		if message.Role == "tool" && strings.Contains(text, "generationBatchId") {
			// Tool content is normally a JSON string; unquote it before decoding.
			value, ok := message.Content.(string)
			if !ok {
				value = string(raw)
			}
			var receipt continuationSubmission
			if json.Unmarshal([]byte(value), &receipt) == nil && receipt.BatchID != "" {
				receipts = append(receipts, receipt)
			}
		}
	}
	p.t.Logf("provider model step: receipts=%d userMarker=%s", len(receipts), lastUser[:min(len(lastUser), 140)])
	var delta map[string]any
	switch {
	case strings.Contains(lastUser, "[系统生成结果]"):
		p.continuations.Add(1)
		p.resultsOK.Store(strings.Contains(lastUser, "CONTINUATION_EXPECTED_FAILURE") && strings.Contains(lastUser, "不要自动重新提交付费生成") && strings.Contains(lastUser, fmt.Sprintf("任务数：%d", len(p.gates))))
		delta = agentSDKToolDelta("finish_run", `{"summary":"CONTINUATION_CHECKED_FAILURES"}`)
	case strings.Contains(lastUser, "CONTINUATION_USER_PRIORITY"):
		p.userOnce.Do(func() { close(p.userStarted) })
		select {
		case <-r.Context().Done():
			return
		case <-p.userGate:
		}
		delta = agentSDKToolDelta("finish_run", `{"summary":"CONTINUATION_USER_DONE"}`)
	default:
		p.mu.Lock()
		p.receipts = append([]continuationSubmission(nil), receipts...)
		p.mu.Unlock()
		if len(receipts) < len(p.gates) {
			available := false
			for _, tool := range body.Tools {
				available = available || tool.Function.Name == "generate_media"
			}
			if !available {
				p.t.Error("fixture request did not disclose generate_media")
				http.Error(w, "missing generation tool", 400)
				return
			}
			index := len(receipts)
			args, _ := json.Marshal(map[string]any{"mode": "image", "referenceNodeIds": []string{}, "nodeId": fmt.Sprintf("continuation-image-%d", index), "title": fmt.Sprintf("Batch image %d", index), "prompt": fmt.Sprintf("CONTINUATION_IMAGE_%d", index), "channelId": "sdk-image", "channelModelKey": "image-e2e", "size": "1024x1024"})
			delta = agentSDKToolDelta("generate_media", string(args))
			delta["tool_calls"].([]any)[0].(map[string]any)["id"] = fmt.Sprintf("continuation-generate-%d", index)
		} else {
			delta = agentSDKToolDelta("finish_run", `{"summary":"全部任务已提交；本批全部终态后检查结果。","continuation":{"when":"all_generations_terminal","instruction":"检查本批结果，失败项报告原因，不自动重新生成。"}}`)
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
	fmt.Fprintf(w, "data: %s\n\n", chunk)
	end, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 10, "total_tokens": 40}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", end)
}
