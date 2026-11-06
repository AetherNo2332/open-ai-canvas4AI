package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// structuredFixture is a migrated database with one system text channel that
// points at a recording chat-completions upstream.
type structuredFixture struct {
	svc      *Service
	db       *gorm.DB
	calls    atomic.Int32
	mu       sync.Mutex
	bodies   []map[string]any
	reply    atomic.Value // string: raw JSON body the upstream returns
	channel  string
	modelKey string
}

func newStructuredFixture(t *testing.T, db *gorm.DB) *structuredFixture {
	t.Helper()
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	f := &structuredFixture{db: db, channel: "text-channel", modelKey: "text-model"}
	f.reply.Store(`{"choices":[{"message":{"role":"assistant","content":"好的"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.reply.Load().(string)))
	}))
	t.Cleanup(upstream.Close)
	f.svc = &Service{repo: repository.New(db), dataDir: t.TempDir(), activeCancels: make(map[string]context.CancelFunc), coordinator: platformTestCoordinator(t, db.Dialector.Name())}
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), f.modelKey)
	capabilityJSON, _ := json.Marshal(profile)
	for _, row := range []any{
		&model.User{ID: "user-a", Username: "user-a", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.CreditAccount{UserID: "user-a", AvailableMicrocredits: 10_000_000},
		&model.Project{ID: "drama-1", UserID: "user-a", Name: "短剧", Type: "short-drama", Status: model.ProjectStatusActive, Revision: 1},
		&model.ModelChannel{ID: f.channel, Scope: model.ChannelScopeSystem, Enabled: true, Name: "文本", BaseURL: upstream.URL + "/v1", APIKey: "upstream-key", APIFormat: "openai"},
		&model.ChannelModel{ID: "text-cm", ChannelID: f.channel, ModelKey: f.modelKey, ProviderModelKey: f.modelKey, Capability: "text", Protocol: model.ChannelInterfaceChatCompletion, CapabilityConfigJSON: string(capabilityJSON), BillingMode: "token", UnitPriceMicrocredits: 10_000, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "text-tier", ChannelModelID: "text-cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "token", UnitPriceMicrocredits: 10_000, InputTokenPriceMicrocredits: 1_000, OutputTokenPriceMicrocredits: 1_000, PriceConfigured: true, Enabled: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func structuredDialects(t *testing.T, run func(t *testing.T, f *structuredFixture)) {
	t.Run("sqlite", func(t *testing.T) { run(t, newStructuredFixture(t, newSQLiteTestDB(t))) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
		}
		run(t, newStructuredFixture(t, platformPostgresDB(t, dsn)))
	})
}

func (f *structuredFixture) request(key string) cp.StructuredTextRequest {
	return cp.StructuredTextRequest{
		Model:  cp.ModelSelection{ChannelID: f.channel, ModelKey: f.modelKey},
		System: "你是短剧助手",
		Messages: []cp.StructuredMessage{
			{Role: cp.RoleUser, Text: "读第一集"},
			{Role: cp.RoleAssistant, ToolCalls: []cp.ToolCall{{ID: "call-1", Name: "drama_read", Arguments: json.RawMessage(`{"episode":1}`)}}},
			{Role: cp.RoleTool, ToolCallID: "call-1", Text: `{"title":"开端"}`},
		},
		Tools:          []cp.ToolDefinition{{Name: "drama_read", Description: "读取剧集", Parameters: json.RawMessage(`{"type":"object","properties":{"episode":{"type":"integer"}}}`)}},
		ToolChoice:     cp.ToolChoiceAuto,
		Business:       cp.BusinessRef{Application: "drama", ObjectType: "agent_step", ObjectID: "s1", OperationID: key},
		IdempotencyKey: key,
	}
}

func (f *structuredFixture) run(t *testing.T) {
	t.Helper()
	claimed, err := f.svc.repo.ClaimNextTask("structured-worker", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := f.svc.taskWorker().processClaimedTask(claimed, nil); err != nil {
		t.Fatalf("process: %v", err)
	}
}

func (f *structuredFixture) lastBody(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) == 0 {
		t.Fatal("upstream was not called")
	}
	return f.bodies[len(f.bodies)-1]
}

// A4-A: messages, tool calls and tool results reach the upstream as real tool
// messages, the result keeps tool calls and usage, and the step is billed.
func TestAgentPlatformStructuredModelStep(t *testing.T) {
	structuredDialects(t, func(t *testing.T, f *structuredFixture) {
		caller := cp.Caller{UserID: "user-a"}
		f.reply.Store(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-2","type":"function","function":{"name":"drama_read","arguments":"{\"episode\":2}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":40,"completion_tokens":9}}`)
		receipt, err := f.svc.SubmitStructuredText(context.Background(), caller, f.request("step-1"), "")
		if err != nil {
			t.Fatal(err)
		}
		f.run(t)
		body := f.lastBody(t)
		messages, _ := body["messages"].([]any)
		roles := []string{}
		for _, raw := range messages {
			message, _ := raw.(map[string]any)
			roles = append(roles, stringValue(message["role"]))
		}
		if strings.Join(roles, ",") != "system,user,assistant,tool" {
			t.Fatalf("upstream roles %v (tool semantics lost?)", roles)
		}
		assistant, _ := messages[2].(map[string]any)
		calls, _ := assistant["tool_calls"].([]any)
		toolMessage, _ := messages[3].(map[string]any)
		if len(calls) != 1 || stringValue(toolMessage["tool_call_id"]) != "call-1" || !strings.Contains(stringValue(toolMessage["content"]), "开端") {
			t.Fatalf("tool call/result not preserved: %#v %#v", assistant, toolMessage)
		}
		if tools, _ := body["tools"].([]any); len(tools) != 1 {
			t.Fatalf("tools not forwarded: %#v", body["tools"])
		}
		result, snapshot, err := f.svc.StructuredResult(context.Background(), caller, receipt.Snapshot.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Execution != cp.ExecutionSucceeded || len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-2" || result.ToolCalls[0].Name != "drama_read" || string(result.ToolCalls[0].Arguments) != `{"episode":2}` {
			t.Fatalf("structured result: %+v %+v", result, snapshot.Execution)
		}
		if !result.Usage.Available || result.Usage.InputTokens != 40 || result.Usage.OutputTokens != 9 {
			t.Fatalf("usage: %+v", result.Usage)
		}
		if snapshot.Billing.Status != cp.BillingSettled || snapshot.Billing.ChargedMicrocredits == nil || *snapshot.Billing.ChargedMicrocredits <= 0 {
			t.Fatalf("model step must be billed: %+v", snapshot.Billing)
		}
		// Replaying the same step returns the same task and calls the model once.
		replay, err := f.svc.SubmitStructuredText(context.Background(), caller, f.request("step-1"), "")
		if err != nil || replay.Snapshot.TaskID != receipt.Snapshot.TaskID || !replay.Replayed || f.calls.Load() != 1 {
			t.Fatalf("replay: %+v %v calls=%d", replay, err, f.calls.Load())
		}
		changed := f.request("step-1")
		changed.Messages[2].Text = `{"title":"改了"}`
		if _, err := f.svc.SubmitStructuredText(context.Background(), caller, changed, ""); !cp.IsCode(err, cp.CodeIdempotencyConflict) {
			t.Fatalf("same key, other content: %v", err)
		}
		if _, _, err := f.svc.StructuredResult(context.Background(), cp.Caller{UserID: "user-b"}, receipt.Snapshot.TaskID); !cp.IsCode(err, cp.CodeNotFound) {
			t.Fatalf("foreign result read: %v", err)
		}
	})
}

// A4-B: an invalid structured request fails before any task, order or call.
func TestAgentPlatformStructuredRejectsBeforeCost(t *testing.T) {
	f := newStructuredFixture(t, newSQLiteTestDB(t))
	bad := f.request("bad")
	bad.Messages[2].ToolCallID = "missing"
	if _, err := f.svc.SubmitStructuredText(context.Background(), cp.Caller{UserID: "user-a"}, bad, ""); !cp.IsCode(err, cp.CodeInvalidRequest) {
		t.Fatalf("invalid request: %v", err)
	}
	if _, err := f.svc.SubmitStructuredText(context.Background(), cp.Caller{}, f.request("anon"), ""); !cp.IsCode(err, cp.CodeUnauthorized) {
		t.Fatalf("anonymous: %v", err)
	}
	if got := platformCount(t, f.db, &model.Task{}, "user_id = ?", "user-a"); got != 0 || f.calls.Load() != 0 {
		t.Fatalf("rejected request produced tasks=%d calls=%d", got, f.calls.Load())
	}
}

// A4-B/C: a runtime turn freezes its context, admits the step under the
// Session lease and budget, and a crash between model success and the
// assistant checkpoint resumes on the same task without a second model call.
func TestAgentRuntimeResumeAfterCheckpointGap(t *testing.T) {
	structuredDialects(t, func(t *testing.T, f *structuredFixture) {
		caller := cp.Caller{UserID: "user-a"}
		session, err := f.svc.AgentSessions().Create(context.Background(), as.Caller{UserID: "user-a"}, as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "runtime"})
		if err != nil {
			t.Fatal(err)
		}
		root, err := f.svc.BudgetGrants().CreateRoot(context.Background(), caller, cp.CreateBudgetRequest{LimitMicrocredits: 5_000_000, IdempotencyKey: "runtime-root"})
		if err != nil {
			t.Fatal(err)
		}
		turn := agentTurnRequest{SessionID: session.ID, MessageID: "u1", Text: "写第一集", Model: cp.ModelSelection{ChannelID: f.channel, ModelKey: f.modelKey}, BudgetGrantID: root.ID}
		step, err := f.svc.startAgentTurn("user-a", turn)
		if err != nil {
			t.Fatal(err)
		}
		f.run(t)
		// Simulate a crash after the model succeeded: the turn is retried from scratch.
		again, err := f.svc.startAgentTurn("user-a", turn)
		if err != nil || again.TaskID != step.TaskID {
			t.Fatalf("retried turn must reuse the step task: %+v %v", again, err)
		}
		done, err := f.svc.completeAgentTurn("user-a", step)
		if err != nil || done.Text != "好的" {
			t.Fatalf("complete: %+v %v", done, err)
		}
		// Completing twice appends nothing new.
		if _, err := f.svc.completeAgentTurn("user-a", step); err != nil {
			t.Fatal(err)
		}
		if f.calls.Load() != 1 {
			t.Fatalf("model called %d times for one turn", f.calls.Load())
		}
		rows, err := f.svc.repo.AgentSessionMessages("user-a", session.ID)
		if err != nil || len(rows) != 2 || rows[0].Actor != as.ActorUser || rows[1].Actor != as.ActorExecutor || rows[1].Body != "好的" {
			t.Fatalf("history after turn: %+v %v", rows, err)
		}
		// The step was billed to the Session's budget.
		view, err := f.svc.BudgetGrants().Get(context.Background(), caller, root.ID)
		if err != nil || view.Usage.Charged <= 0 {
			t.Fatalf("turn not billed to the grant: %+v %v", view, err)
		}
		// The upstream saw the frozen context and the user message.
		body := f.lastBody(t)
		raw, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(raw), "写第一集") || !strings.Contains(string(raw), "短剧 Agent") {
			t.Fatalf("model input lacks context or message: %s", raw)
		}
		// The lease is released after the turn.
		stored, err := f.svc.agentSessionForOwner("user-a", session.ID)
		if err != nil || stored.LeaseOwner != "" {
			t.Fatalf("lease kept after turn: %+v %v", stored, err)
		}
	})
}

// A4-D: a turn on another owner's Session, or under another owner's budget,
// is rejected before any model cost.
func TestAgentRuntimeRejectsForeignScope(t *testing.T) {
	f := newStructuredFixture(t, newSQLiteTestDB(t))
	session, err := f.svc.AgentSessions().Create(context.Background(), as.Caller{UserID: "user-a"}, as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	turn := agentTurnRequest{SessionID: session.ID, MessageID: "u1", Text: "hi", Model: cp.ModelSelection{ChannelID: f.channel, ModelKey: f.modelKey}}
	if _, err := f.svc.startAgentTurn("user-b", turn); !as.IsCode(err, as.CodeNotFound) {
		t.Fatalf("foreign session turn: %v", err)
	}
	turn.BudgetGrantID = "someone-elses-grant"
	if _, err := f.svc.startAgentTurn("user-a", turn); !cp.IsCode(err, cp.CodeNotFound) {
		t.Fatalf("unknown grant: %v", err)
	}
	if got := platformCount(t, f.db, &model.Task{}, "1 = 1"); got != 0 || f.calls.Load() != 0 {
		t.Fatalf("rejected turns produced tasks=%d calls=%d", got, f.calls.Load())
	}
	if stored, _ := f.svc.agentSessionForOwner("user-a", session.ID); stored.LeaseOwner != "" {
		t.Fatalf("rejected turn left a lease: %+v", stored)
	}
}

// A4-E: a structured runtime step belongs to its turn; the task center cannot
// re-run it outside the Session writer.
func TestAgentStructuredStepNotRetriableFromTaskCenter(t *testing.T) {
	f := newStructuredFixture(t, newSQLiteTestDB(t))
	caller := cp.Caller{UserID: "user-a"}
	f.reply.Store(`{"error":{"message":"bad"}}`)
	receipt, err := f.svc.SubmitStructuredText(context.Background(), caller, f.request("fail-step"), "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.svc.repo.ClaimNextTask("structured-worker", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	_ = f.svc.taskWorker().processClaimedTask(claimed, nil)
	orders := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a")
	if _, err := f.svc.RetryTask("user-a", receipt.Snapshot.TaskID); err == nil {
		t.Fatal("task center retry of a runtime step must be rejected")
	}
	if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != orders {
		t.Fatalf("rejected retry created orders: %d -> %d", orders, got)
	}
}
