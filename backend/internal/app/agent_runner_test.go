package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type agentRunnerFixture struct {
	*structuredFixture
	text *structuredFixture
}

func newAgentRunnerFixture(t *testing.T, text *structuredFixture) *agentRunnerFixture {
	t.Helper()
	production := &model.DramaProduction{ProjectID: "drama-1", UserID: "user-a", Status: model.DramaStatusPaused, Stage: model.DramaStageProduce, BudgetMicrocredits: 5_000_000}
	if err := text.db.Create(production).Error; err != nil {
		t.Fatal(err)
	}
	return &agentRunnerFixture{structuredFixture: text, text: text}
}

func (f *agentRunnerFixture) drain(t *testing.T) {
	t.Helper()
	for range 10 {
		claimed, err := f.svc.repo.ClaimNextTask("runner-model-worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil {
			driveTestPiRuns(t, f.svc, f.text, "user-a")
			return
		}
		_ = f.svc.taskWorker().processClaimedTask(claimed, nil)
	}
	driveTestPiRuns(t, f.svc, f.text, "user-a")
}

// A user message to a business Session is answered by the background runner:
// the message is queued, the runner picks the space's text model, runs the
// turn through the worker, and the reply lands in the Session history once.
func TestAgentRunnerRepliesToUserMessage(t *testing.T) {
	f := newBusinessFixture(t)
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "runner"})
	f.text.script(toolCallReply("call-1", "drama_summary", `{}`), textReply("项目已读取，可以开始"))
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-1", Text: "看看项目状态"}); err != nil {
		t.Fatal(err)
	}
	bound, err := f.svc.agentSessionForOwner("user-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.WorkItemID == "" {
		t.Fatal("first paid turn did not bind an independent work item")
	}
	work, err := f.svc.GetWork("user-a", bound.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	if work.ParentWorkID != "" || work.Scope != session.Scope || work.BudgetGrantID == "" {
		t.Fatalf("independent work: %+v", work)
	}
	grant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, work.BudgetGrantID)
	if err != nil || grant.LimitMicrocredits != 1_000_000 {
		t.Fatalf("independent budget: %+v %v", grant, err)
	}
	var queued model.AgentTurn
	if err := f.db.First(&queued, "session_id = ?", session.ID).Error; err != nil || queued.BudgetGrantID != work.BudgetGrantID {
		t.Fatalf("queued turn budget: %+v %v", queued, err)
	}
	// The runner advances; the worker runs queued model steps between passes.
	for range 10 {
		if _, err := f.svc.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
		driveTestPiRuns(t, f.svc, f.text, "user-a")
	}
	rows, err := f.svc.repo.AgentSessionMessages("user-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	replies := 0
	for _, row := range rows {
		if row.Actor == as.ActorExecutor && row.Kind == as.MessageKindMessage {
			replies++
			if !strings.Contains(row.Body, "项目已读取") {
				t.Fatalf("reply: %+v", row)
			}
		}
	}
	if replies != 1 {
		t.Fatalf("replies = %d in %+v", replies, rows)
	}
	// Further passes do nothing: the turn is done.
	calls := f.text.calls.Load()
	for range 3 {
		if _, err := f.svc.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
		driveTestPiRuns(t, f.svc, f.text, "user-a")
	}
	if f.text.calls.Load() != calls {
		t.Fatalf("finished turn re-ran: %d -> %d", calls, f.text.calls.Load())
	}
	var turns []model.AgentTurn
	f.db.Where("session_id = ?", session.ID).Find(&turns)
	if len(turns) != 1 || turns[0].Status != "done" {
		t.Fatalf("turn rows: %+v", turns)
	}
	// A second message gets its own turn and reply.
	f.text.script(textReply("好的，已记下"))
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-2", Text: "第一集更紧凑"}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := f.svc.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
		driveTestPiRuns(t, f.svc, f.text, "user-a")
	}
	rows, _ = f.svc.repo.AgentSessionMessages("user-a", session.ID)
	if last := rows[len(rows)-1]; last.Actor != as.ActorExecutor || last.Body != "好的，已记下" {
		t.Fatalf("second reply: %+v", last)
	}
}

func TestAgentRunnerDirectReplyCrossDialect(t *testing.T) {
	structuredDialects(t, func(t *testing.T, text *structuredFixture) {
		f := newAgentRunnerFixture(t, text)
		session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "runner-dialect"})
		f.text.script(textReply("跨库回复"))
		if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-1", Text: "你好"}); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		claimed, err := f.svc.repo.ClaimRunnableAgentTurns(f.svc.agentRuntimeOwner(), now, now.Add(time.Minute), 10)
		if err != nil || len(claimed) != 1 || claimed[0].BudgetGrantID == "" {
			t.Fatalf("claim: %+v %v", claimed, err)
		}
		if other, err := f.svc.repo.ClaimRunnableAgentTurns("other-instance", now, now.Add(time.Minute), 10); err != nil || len(other) != 0 {
			t.Fatalf("claim stolen: %+v %v", other, err)
		}
		for range 5 {
			if _, err := f.svc.runPendingAgentTurns(10); err != nil {
				t.Fatal(err)
			}
			f.drain(t)
		}
		rows, err := f.svc.repo.AgentSessionMessages("user-a", session.ID)
		if err != nil || rows[len(rows)-1].Body != "跨库回复" {
			t.Fatalf("reply: %+v %v", rows, err)
		}
		grant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, claimed[0].BudgetGrantID)
		if err != nil || grant.Usage.Spent() == 0 {
			t.Fatalf("grant usage: %+v %v", grant, err)
		}
	})
}

func TestAgentDirectMessageAndIndependentWorkRollbackTogether(t *testing.T) {
	f := newBusinessFixture(t)
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "atomic-direct"})
	if err := f.db.Exec(`CREATE TRIGGER reject_agent_turn BEFORE INSERT ON agent_turns BEGIN SELECT RAISE(ABORT, 'reject turn'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-1", Text: "检查项目"}); err == nil {
		t.Fatal("turn insert failure did not fail direct message")
	}
	var messages, works, grants int64
	f.db.Model(&model.AgentSessionMessage{}).Where("session_id = ?", session.ID).Count(&messages)
	f.db.Model(&model.AgentWorkItem{}).Where("source_session_id = ?", session.ID).Count(&works)
	f.db.Model(&model.PlatformBudgetGrant{}).Where("object_type = ? AND application = ?", "work", as.ApplicationDrama).Count(&grants)
	stored, err := f.svc.agentSessionForOwner("user-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages != 0 || works != 0 || grants != 0 || stored.WorkItemID != "" {
		t.Fatalf("partial direct message committed: messages=%d works=%d grants=%d session=%+v", messages, works, grants, stored)
	}
}

func TestAgentRunnerRecoversMessageBeforeWorkBinding(t *testing.T) {
	f := newBusinessFixture(t)
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "recover-binding"})
	message := model.AgentSessionMessage{ID: newID(), MessageID: "u-crash", Kind: as.MessageKindMessage, Actor: as.ActorUser, Role: "user", Body: "崩溃前已保存"}
	if err := f.svc.repo.AppendAgentInboxMessage("user-a", session.ID, message, time.Now()); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-crash", Text: message.Body})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.WorkItemID == "" {
		t.Fatal("replay did not finish independent work binding")
	}
	var turns int64
	if err := f.db.Model(&model.AgentTurn{}).Where("session_id = ? AND message_id = ?", session.ID, message.MessageID).Count(&turns).Error; err != nil || turns != 1 {
		t.Fatalf("recovered turns = %d, err=%v", turns, err)
	}
}

func TestAgentRunnerRecoversWorkBindingBeforeTurnQueue(t *testing.T) {
	f := newBusinessFixture(t)
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "recover-queue"})
	message := model.AgentSessionMessage{ID: newID(), MessageID: "u-crash", Kind: as.MessageKindMessage, Actor: as.ActorUser, Role: "user", Body: "预算已建但队列未写"}
	if err := f.svc.repo.AppendAgentInboxMessage("user-a", session.ID, message, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ensureIndependentAgentWork(&model.AgentSession{ID: session.ID, OwnerID: "user-a", DefinitionID: session.DefinitionID, Role: session.Role, Application: session.Scope.Application, SpaceType: session.Scope.SpaceType, SpaceID: session.Scope.SpaceID}, message.MessageID, message.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: message.MessageID, Text: message.Body}); err != nil {
		t.Fatal(err)
	}
	var turns int64
	if err := f.db.Model(&model.AgentTurn{}).Where("session_id = ? AND message_id = ?", session.ID, message.MessageID).Count(&turns).Error; err != nil || turns != 1 {
		t.Fatalf("recovered turns = %d, err=%v", turns, err)
	}
}

func TestAgentRunnerClaimsTurnOnceAcrossInstances(t *testing.T) {
	f := newBusinessFixture(t)
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "claim"})
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-1", Text: "检查状态"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	until := now.Add(time.Minute)
	first, err := f.svc.repo.ClaimRunnableAgentTurns("runner-a", now, until, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: %+v %v", first, err)
	}
	second, err := f.svc.repo.ClaimRunnableAgentTurns("runner-b", now, until, 10)
	if err != nil || len(second) != 0 {
		t.Fatalf("live claim was stolen: %+v %v", second, err)
	}
	takenOver, err := f.svc.repo.ClaimRunnableAgentTurns("runner-b", until.Add(time.Second), until.Add(2*time.Minute), 10)
	if err != nil || len(takenOver) != 1 || takenOver[0].ID != first[0].ID {
		t.Fatalf("expired claim was not recoverable: %+v %v", takenOver, err)
	}
	updated, err := f.svc.repo.UpdateClaimedAgentTurn(first[0].ID, "runner-a", map[string]any{"status": "failed"})
	if err != nil || updated {
		t.Fatalf("expired claimant wrote after takeover: updated=%v err=%v", updated, err)
	}
}

func TestAgentTurnRetryPolicyUsesStructuredErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "agent validation", err: as.NewError(as.CodeInvalidRequest, "bad")},
		{name: "agent internal", err: as.NewError(as.CodeInternal, "retry"), want: true},
		{name: "budget exhausted", err: cp.NewError(cp.CodeBudgetExceeded, "spent")},
		{name: "platform unavailable", err: cp.NewError(cp.CodeUnavailable, "retry"), want: true},
		{name: "unknown infrastructure", err: errors.New("connection reset"), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentTurnRetryable(tc.err); got != tc.want {
				t.Fatalf("retryable = %v, want %v", got, tc.want)
			}
		})
	}
}

// Without any usable text model the turn fails visibly instead of hanging.
func TestAgentRunnerReportsMissingModel(t *testing.T) {
	f := newBusinessFixture(t)
	if err := f.db.Model(&model.ChannelModel{}).Where("capability = ?", "text").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "nomodel"})
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-1", Text: "你好"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.runPendingAgentTurns(10); err != nil {
		t.Fatal(err)
	}
	rows, _ := f.svc.repo.AgentSessionMessages("user-a", session.ID)
	last := rows[len(rows)-1]
	if last.Actor != as.ActorSystem || !strings.Contains(last.Body, "文本模型") {
		t.Fatalf("missing-model notice: %+v", last)
	}
	var turn model.AgentTurn
	if err := f.db.First(&turn, "session_id = ?", session.ID).Error; err != nil || turn.Status != "failed" {
		t.Fatalf("turn: %+v %v", turn, err)
	}
}

func TestAgentRunnerFailureStateAndNoticeCommitTogether(t *testing.T) {
	f := newBusinessFixture(t)
	if err := f.db.Model(&model.ChannelModel{}).Where("capability = ?", "text").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "atomic-failure"})
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-1", Text: "你好"}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`CREATE TRIGGER reject_failure_notice BEFORE INSERT ON agent_session_messages WHEN NEW.message_id = 'u-1:failed' BEGIN SELECT RAISE(ABORT, 'reject notice'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.runPendingAgentTurns(10); err == nil {
		t.Fatal("failure notice insert did not fail the runner pass")
	}
	var turn model.AgentTurn
	if err := f.db.First(&turn, "session_id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status == "failed" {
		t.Fatalf("turn committed without its visible failure notice: %+v", turn)
	}
	if err := f.db.Exec(`DROP TRIGGER reject_failure_notice`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.runPendingAgentTurns(10); err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&turn, "session_id = ?", session.ID).Error; err != nil || turn.Status != "failed" {
		t.Fatalf("turn did not finish after retry: %+v %v", turn, err)
	}
	rows, err := f.svc.repo.AgentSessionMessages("user-a", session.ID)
	if err != nil || rows[len(rows)-1].MessageID != "u-1:failed" {
		t.Fatalf("failure notice: %+v %v", rows, err)
	}
}

func TestAgentOutboxDeliveryAndTurnQueueRollbackTogether(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "s", Goal: "看项目", BudgetMicrocredits: 1_000_000, IdempotencyKey: "atomic-delivery", AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "s", ActionKey: "d", DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, Handoff: "读取项目状态", BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`CREATE TRIGGER reject_agent_turn BEFORE INSERT ON agent_turns BEGIN SELECT RAISE(ABORT, 'reject turn'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.deliverAgentOutbox("user-a", 10); err == nil {
		t.Fatal("turn insert failure did not fail delivery")
	}
	var outbox model.AgentOutbox
	if err := f.db.First(&outbox, "target_session_id = ?", assignment.ExecutorSessionID).Error; err != nil {
		t.Fatal(err)
	}
	var messages int64
	if err := f.db.Model(&model.AgentSessionMessage{}).Where("session_id = ?", assignment.ExecutorSessionID).Count(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if outbox.Status != "pending" || messages != 0 {
		t.Fatalf("partial delivery committed: status=%s messages=%d", outbox.Status, messages)
	}
}

// newSecondaryAgentService builds a second backend instance on the same
// database, the way a redeployed or neighbouring instance would join it.
func newSecondaryAgentService(t *testing.T, db *gorm.DB) *Service {
	t.Helper()
	return &Service{repo: repository.New(db), dataDir: t.TempDir(), activeCancels: make(map[string]context.CancelFunc), coordinator: platformTestCoordinator(t, db.Dialector.Name()), workerID: "instance-2"}
}

// B0-D: an expired lease is taken over within one instance and across
// instances, a late writer holding the expired lease cannot commit, and the
// turn reaches exactly one deterministic terminal state.
func TestAgentLongTurnLeaseRecovery(t *testing.T) {
	f := newBusinessFixture(t)
	second := newSecondaryAgentService(t, f.db)
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "lease-recovery"})
	f.text.script(textReply("恢复后的回复"))
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "u-slow", Text: "慢慢想"}); err != nil {
		t.Fatal(err)
	}
	// Go durably hands the inbox item to Pi; Pi owns the execution lease and
	// model checkpoint after admission. AgentTurn.done only means handed off.
	if _, err := f.svc.runPendingAgentTurns(10); err != nil {
		t.Fatal(err)
	}
	var turn model.AgentTurn
	if err := f.db.First(&turn, "session_id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != "done" || turn.ClaimOwner != "" {
		t.Fatalf("claimed turn: %+v", turn)
	}
	first, err := f.svc.ClaimPiAgentForWorker("first-pi", "user-a")
	if err != nil || first == nil {
		t.Fatalf("first Pi claim: %+v %v", first, err)
	}
	oldOwner := "first-pi@" + itoa64(first.PiSessionLeaseEpoch)
	step, err := f.svc.PiModelStep("user-a", first.RunID, oldOwner, PiModelStepRequest{Canonical: first.Canonical})
	if err != nil {
		t.Fatal(err)
	}
	// Crash mid-step: expire the run and Pi Session leases, keeping the task.
	for _, row := range []any{&model.CloudAgentExecution{}, &model.CloudAgentPiSession{}} {
		if err := f.db.Model(row).Where("user_id = ?", "user-a").Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	taken, err := second.ClaimPiAgentForWorker("second-pi", "user-a")
	if err != nil || taken == nil || taken.RunID != first.RunID || taken.PiSessionLeaseEpoch != first.PiSessionLeaseEpoch+1 {
		t.Fatalf("Pi takeover: %+v %v", taken, err)
	}
	f.drain(t)
	if err := f.svc.PiCheckpointMessage("user-a", first.RunID, oldOwner, PiMessageCheckpoint{Sequence: 1, TaskID: step.TaskID, Message: json.RawMessage(`{"role":"assistant","content":"迟到的写入"}`)}); err == nil {
		t.Fatal("late Pi writer committed with an expired lease")
	}
	driveTestPiRun(t, second, f.text, "user-a", "second-pi", taken)
	run, err := second.repo.CloudAgent("user-a", first.RunID)
	if err != nil || run.Status != "completed" {
		t.Fatalf("Pi terminal state: %+v %v", run, err)
	}
	if count := platformCount(t, f.db, &model.PlatformBudgetCharge{}, "owner_id = ? AND task_id = ?", "user-a", step.TaskID); count != 1 {
		t.Fatalf("expected one charge for the resumed step, got %d", count)
	}
	// The inbox row remains done across execution takeover.
	if _, err := second.runPendingAgentTurns(10); err != nil {
		t.Fatal(err)
	}
	var finished model.AgentTurn
	if err := f.db.First(&finished, "session_id = ?", session.ID).Error; err != nil || finished.Status != "done" {
		t.Fatalf("turn terminal state: %+v %v", finished, err)
	}
	rows, err := f.svc.repo.AgentSessionMessages("user-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	replies := 0
	for _, row := range rows {
		if row.Actor == as.ActorExecutor && row.Kind == as.MessageKindMessage {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("replies = %d in %+v", replies, rows)
	}
}

// expandedNotebookAdapter is the notebook business after a deploy widened its
// tool schema for the same definition version.
type expandedNotebookAdapter struct{ executed *int }

func (expandedNotebookAdapter) Application() string  { return "notebook" }
func (expandedNotebookAdapter) SpaceTypes() []string { return []string{"notebook_page"} }
func (expandedNotebookAdapter) ValidateSpace(context.Context, string, as.Scope) error {
	return nil
}
func (a expandedNotebookAdapter) Tools() []as.ToolSpec {
	return []as.ToolSpec{
		{Name: "notebook_write", Description: "写一页", Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "notebook_erase", Description: "擦一页", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
}
func (a expandedNotebookAdapter) Execute(_ context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	switch call.Name {
	case "notebook_write":
		*a.executed++
		return as.ToolResult{Text: "已写入"}, nil
	case "notebook_erase":
		return as.ToolResult{Text: "已擦除"}, nil
	}
	return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不存在")
}

func containsAll(names []string, wanted ...string) bool {
	for _, want := range wanted {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func requestToolNames(t *testing.T, f *structuredFixture, index int) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.bodies) {
		t.Fatalf("upstream call %d not recorded (%d total)", index, len(f.bodies))
	}
	raw, err := json.Marshal(f.bodies[index]["tools"])
	if err != nil {
		t.Fatal(err)
	}
	tools := []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}{}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatalf("tools body: %v", err)
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Function.Name)
	}
	sort.Strings(names)
	return names
}

// B0-E: a turn whose first step already ran keeps its frozen tool contract
// when a deployed instance widens the schema; only a new turn freezes the new
// contract.
func TestAgentFrozenContractRecovery(t *testing.T) {
	f := newBusinessFixture(t)
	executed := 0
	assignment := reviewNotebookWork(t, f, &executed)
	selection := cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}
	turn := businessTurn{Assignment: assignment, MessageID: "frozen-message", Text: "write", Model: selection}
	f.text.script(toolCallReply("call-0", "notebook_write", `{}`))
	if _, done, err := f.svc.advanceBusinessTurn("user-a", turn); done || err != nil {
		t.Fatalf("initial step: done=%v err=%v", done, err)
	}
	f.drain(t)
	if names := requestToolNames(t, f.text, 0); !containsAll(names, "notebook_write", "report_work") {
		t.Fatalf("first step tools: %v", names)
	}
	// Deploy: a new instance ships a wider tool schema for the same definition.
	upgraded := newSecondaryAgentService(t, f.db)
	if err := upgraded.registerAgentBusiness(expandedNotebookAdapter{executed: &executed}, as.Definition{
		ID: "notebook.agent", Version: "1", Role: as.RoleBusiness,
		Application: "notebook", SpaceTypes: []string{"notebook_page"}, Title: "Notebook", Tools: []string{"notebook_write", "notebook_erase"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", assignment.ExecutorSessionID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	f.text.script(textReply("done"))
	f.drain(t)
	done := false
	for range 8 {
		result, finished, err := upgraded.advanceBusinessTurn("user-a", turn)
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			done = true
			if result.Text != "done" {
				t.Fatalf("resumed turn text: %q", result.Text)
			}
			break
		}
		f.drain(t)
	}
	if !done {
		t.Fatal("resumed turn did not finish")
	}
	if names := requestToolNames(t, f.text, 1); !containsAll(names, "notebook_write", "report_work") {
		t.Fatalf("resumed turn must keep the frozen contract, tools: %v", names)
	}
	// A new turn freezes the widened schema.
	f.text.script(textReply("新轮次完成"))
	fresh := businessTurn{Assignment: assignment, MessageID: "fresh-message", Text: "write", Model: selection}
	done = false
	for range 8 {
		_, finished, err := upgraded.advanceBusinessTurn("user-a", fresh)
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			done = true
			break
		}
		f.drain(t)
	}
	if !done {
		t.Fatal("new turn did not finish")
	}
	if names := requestToolNames(t, f.text, 2); !containsAll(names, "notebook_write", "notebook_erase", "report_work") {
		t.Fatalf("new turn must use the new contract, tools: %v", names)
	}
}

// Delegated work starts the executor without a user message: the delivery of
// the assignment queues a turn under the child budget grant.
func TestAgentRunnerStartsDelegatedWork(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "s", Goal: "看项目", BudgetMicrocredits: 1_000_000, IdempotencyKey: "runner-work", AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "s", ActionKey: "d", DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, Handoff: "读取项目状态并汇报", BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	f.text.script(textReply("项目状态已读"))
	for range 6 {
		if _, err := f.svc.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
		driveTestPiRuns(t, f.svc, f.text, "user-a")
	}
	rows, _ := f.svc.repo.AgentSessionMessages("user-a", assignment.ExecutorSessionID)
	if last := rows[len(rows)-1]; last.Actor != as.ActorExecutor || last.Body != "项目状态已读" {
		t.Fatalf("executor reply: %+v", rows)
	}
	grant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, assignment.BudgetGrantID)
	if err != nil || grant.Usage.Spent() == 0 {
		t.Fatalf("executor turn not charged to its grant: %+v %v", grant, err)
	}
}
