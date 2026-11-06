package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
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
			return
		}
		_ = f.svc.taskWorker().processClaimedTask(claimed, nil)
	}
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
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "s", Goal: "看项目", BudgetMicrocredits: 2_000_000, IdempotencyKey: "atomic-delivery"})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "s", ActionKey: "d", DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, Handoff: "读取项目状态", BudgetMicrocredits: 1_000_000})
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

// Delegated work starts the executor without a user message: the delivery of
// the assignment queues a turn under the child budget grant.
func TestAgentRunnerStartsDelegatedWork(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "s", Goal: "看项目", BudgetMicrocredits: 2_000_000, IdempotencyKey: "runner-work"})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "s", ActionKey: "d", DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, Handoff: "读取项目状态并汇报", BudgetMicrocredits: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	f.text.script(textReply("项目状态已读"))
	for range 6 {
		if _, err := f.svc.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
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
