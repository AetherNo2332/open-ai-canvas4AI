package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
)

// genericPiFixture builds one delegated drama Session with a live work,
// assignment and budget grant, the identity a generic Pi run must carry.
type genericPiFixture struct {
	*businessFixture
	root       WorkView
	assignment AssignmentView
	session    *model.AgentSession
}

func newGenericPiFixture(t *testing.T, key string, actionKey string) *genericPiFixture {
	t.Helper()
	f := newBusinessFixture(t)
	root, err := f.svc.StartWork("user-a", StartWorkRequest{
		SourceMessageID: "sup-" + key, Goal: "第一集分镜", BudgetMicrocredits: 1_000_000, IdempotencyKey: "generic-" + key,
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{
		WorkItemID: root.ID, SourceMessageID: "sup-" + key, ActionKey: actionKey, DefinitionID: "drama.agent",
		Scope:   as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"},
		Handoff: "梳理第一集", BudgetMicrocredits: 400_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.svc.agentSessionForOwner("user-a", assignment.ExecutorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	return &genericPiFixture{businessFixture: f, root: root, assignment: assignment, session: session}
}

func enqueueGenericPi(t *testing.T, f *genericPiFixture, messageID, prompt string) AgentSessionPiRunView {
	t.Helper()
	view, err := f.svc.EnqueueAgentSessionPiRun("user-a", AgentSessionPiRunRequest{SessionID: f.session.ID, TriggerMessageID: messageID, Prompt: prompt})
	if err != nil {
		t.Fatalf("enqueue generic Pi run: %v", err)
	}
	return view
}

// C1-A: a non-canvas business Session enqueues one Pi run whose claimed
// snapshot carries the complete Session, definition, scope and execution
// identity Go resolved from the work tree.
func TestAgentSessionPiRunClaimCarriesGenericIdentity(t *testing.T) {
	f := newGenericPiFixture(t, "identity", "drama")
	view := enqueueGenericPi(t, f, "d-1", "先看第一集")
	if view.RunID == "" || view.Status != "queued" {
		t.Fatalf("enqueue view: %+v", view)
	}
	replay := enqueueGenericPi(t, f, "d-1", "先看第一集")
	if replay.RunID != view.RunID || !replay.Reused {
		t.Fatalf("replay created another run: %+v vs %+v", replay, view)
	}
	var runs int64
	if err := f.db.Model(&model.CloudAgentExecution{}).Where("engine = ?", "pi").Count(&runs).Error; err != nil || runs != 1 {
		t.Fatalf("queued Pi runs = %d, err=%v", runs, err)
	}

	// Another tenant must not claim the run; the owner's worker then gets the
	// full identity.
	if snapshot, err := f.svc.ClaimPiAgentForWorker("generic-worker", "user-b"); err != nil || snapshot != nil {
		t.Fatalf("foreign tenant claim = %+v, %v", snapshot, err)
	}
	snapshot, err := f.svc.ClaimPiAgentForWorker("generic-worker", "user-a")
	if err != nil || snapshot == nil {
		t.Fatalf("claim generic run: %+v, %v", snapshot, err)
	}
	if snapshot.RunID != view.RunID || snapshot.UserID != "user-a" {
		t.Fatalf("snapshot run identity: %+v", snapshot)
	}
	if snapshot.ProtocolVersion != as.PiExecutionProtocolVersion {
		t.Fatalf("protocolVersion = %q", snapshot.ProtocolVersion)
	}
	if snapshot.SessionID != f.session.ID || snapshot.DefinitionID != "drama.agent" || snapshot.DefinitionVersion != f.session.DefinitionVersion {
		t.Fatalf("snapshot Session identity: %+v", snapshot)
	}
	wantScope := as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}
	if snapshot.Role != as.RoleBusiness || snapshot.Scope == nil || *snapshot.Scope != wantScope {
		t.Fatalf("snapshot definition/scope: role=%q scope=%+v", snapshot.Role, snapshot.Scope)
	}
	if snapshot.TriggerMessageID != "d-1" {
		t.Fatalf("triggerMessageId = %q", snapshot.TriggerMessageID)
	}
	if snapshot.Execution == nil {
		t.Fatal("work-bound run lost its execution binding")
	}
	got := *snapshot.Execution
	if got.WorkItemID != f.assignment.WorkItemID || got.AssignmentID != f.assignment.ID ||
		got.AssignmentEpoch != f.assignment.Epoch || got.BudgetGrantID != f.assignment.BudgetGrantID ||
		got.AuthorizationVersion != f.root.AuthorizationVersion {
		t.Fatalf("execution binding = %+v, assignment = %+v", got, f.assignment)
	}
	if len(snapshot.Tools) == 0 || len(snapshot.EffectiveTools) == 0 {
		t.Fatalf("generic run has no tools: %d/%d", len(snapshot.Tools), len(snapshot.EffectiveTools))
	}
	for _, tool := range snapshot.EffectiveTools {
		if tool.Name == "" || len(tool.Parameters) == 0 {
			t.Fatalf("effective tool is incomplete: %+v", tool)
		}
	}
	if snapshot.ModelLimits.ContextWindowTokens <= 0 || snapshot.ModelLimits.MaxOutputTokens <= 0 {
		t.Fatalf("model limits = %+v", snapshot.ModelLimits)
	}
	var header map[string]any
	if err := json.Unmarshal(snapshot.PiSessionHeader, &header); err != nil {
		t.Fatal(err)
	}
	if cwd, _ := header["cwd"].(string); !strings.HasPrefix(cwd, "agent://") {
		t.Fatalf("generic Pi session cwd = %q, want agent://", cwd)
	}
}

// C1-A model admission uses the same Pi bridge as canvas while the run keeps
// the business Session identity. This stops the generic path at the durable
// Go model task, before a provider worker is needed.
func TestAgentSessionPiRunAdmitsModelStep(t *testing.T) {
	f := newGenericPiFixture(t, "model-step", "drama")
	view := enqueueGenericPi(t, f, "d-1", "读取第一集")
	snapshot, err := f.svc.ClaimPiAgentForWorker("generic-worker", "user-a")
	if err != nil || snapshot == nil {
		t.Fatalf("claim generic run: %+v, %v", snapshot, err)
	}
	canonical := snapshot.Canonical
	canonical.Tools = make([]map[string]any, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		canonical.Tools = append(canonical.Tools, map[string]any{"type": "function", "function": map[string]any{
			"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters,
		}})
	}
	step, err := f.svc.PiModelStep("user-a", view.RunID, "generic-worker@"+itoa64(snapshot.PiSessionLeaseEpoch), PiModelStepRequest{Canonical: canonical})
	if err != nil {
		t.Fatalf("generic Pi model admission: %v", err)
	}
	if step == nil || step.TaskID == "" || step.Status != "queued" {
		t.Fatalf("generic Pi model step = %+v", step)
	}
}

func TestAgentSessionPiRunCompletesNoToolReplyIntoSession(t *testing.T) {
	f := newGenericPiFixture(t, "reply", "drama")
	view := enqueueGenericPi(t, f, "reply-1", "请汇总当前短剧状态")
	snapshot, err := f.svc.ClaimPiAgentForWorker("generic-worker", "user-a")
	if err != nil || snapshot == nil {
		t.Fatalf("claim generic run: %+v, %v", snapshot, err)
	}
	owner := "generic-worker@" + itoa64(snapshot.PiSessionLeaseEpoch)
	f.text.script(textReply("短剧状态已汇总"))
	canonical := snapshot.Canonical
	step, err := f.svc.PiModelStep("user-a", view.RunID, owner, PiModelStepRequest{Canonical: canonical})
	if err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	assistant, _ := json.Marshal(map[string]any{"role": "assistant", "content": "短剧状态已汇总", "stopReason": "stop"})
	if err := f.svc.PiCheckpointMessage("user-a", view.RunID, owner, PiMessageCheckpoint{Sequence: 1, TaskID: step.TaskID, Message: assistant}); err != nil {
		t.Fatal(err)
	}
	decision, err := f.svc.PiNoToolTurn("user-a", view.RunID, owner, step.TaskID)
	if err != nil || decision.Status != "completed" {
		t.Fatalf("generic no-tool decision = %+v, %v", decision, err)
	}
	rows, err := f.svc.repo.AgentSessionMessages("user-a", f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var replies []model.AgentSessionMessage
	for _, row := range rows {
		if row.Actor == as.ActorExecutor && row.Body == "短剧状态已汇总" {
			replies = append(replies, row)
		}
	}
	if len(replies) != 1 {
		t.Fatalf("generic assistant replies = %+v, want exactly one", replies)
	}
	if _, err := f.svc.PiNoToolTurn("user-a", view.RunID, owner, step.TaskID); err != nil {
		t.Fatalf("replaying completed no-tool turn: %v", err)
	}
	rows, err = f.svc.repo.AgentSessionMessages("user-a", f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row.Body == "短剧状态已汇总" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("replayed generic assistant reply count = %d, want 1", count)
	}
}

func TestAgentSessionPiRunExecutesBusinessToolThroughOperationReceipt(t *testing.T) {
	f := newGenericPiFixture(t, "tool", "drama")
	view := enqueueGenericPi(t, f, "tool-1", "读取短剧摘要")
	snapshot, err := f.svc.ClaimPiAgentForWorker("generic-worker", "user-a")
	if err != nil || snapshot == nil {
		t.Fatalf("claim generic run: %+v, %v", snapshot, err)
	}
	owner := "generic-worker@" + itoa64(snapshot.PiSessionLeaseEpoch)
	call := piAgentTestCall("generic-call", "drama_summary", `{}`)
	f.text.script(toolCallReply(call.ID, call.Function.Name, call.Function.Arguments))
	step, err := f.svc.PiModelStep("user-a", view.RunID, owner, PiModelStepRequest{Canonical: snapshot.Canonical})
	if err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	assistant, _ := json.Marshal(map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Function.Name, "arguments": call.Function.Arguments}}}})
	if err := f.svc.PiCheckpointMessage("user-a", view.RunID, owner, PiMessageCheckpoint{Sequence: 1, TaskID: step.TaskID, Message: assistant}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PiToolBatch("user-a", view.RunID, owner, PiToolBatchRequest{TaskID: step.TaskID, Calls: []cloudAgentCall{call}}); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.svc.PiToolAdvance("user-a", view.RunID, owner, step.TaskID, call.ID)
	if err != nil || receipt == nil || receipt.Pending {
		t.Fatalf("generic tool receipt = %+v, %v", receipt, err)
	}
	var operations []model.AgentOperation
	if err := f.db.Where("owner_id = ? AND session_id = ?", "user-a", f.session.ID).Find(&operations).Error; err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].Status != "succeeded" {
		t.Fatalf("generic operations = %+v", operations)
	}
}

func TestAgentRunnerRoutesBusinessTurnToGenericPi(t *testing.T) {
	f := newBusinessFixture(t)
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{
		DefinitionID:   "drama.agent",
		Scope:          as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"},
		IdempotencyKey: "generic-runner",
	})
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: session.ID, MessageID: "runner-1", Text: "读取短剧状态"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.runPendingAgentTurns(10); err != nil {
		t.Fatal(err)
	}
	var run model.CloudAgentExecution
	if err := f.db.Where("user_id = ? AND engine = ?", "user-a", "pi").First(&run).Error; err != nil {
		t.Fatalf("generic Pi run was not admitted: %v", err)
	}
	if run.ConversationID != agentSessionPiConversationID(session.ID) || run.Status != "queued" {
		t.Fatalf("generic Pi run = %+v", run)
	}
	var turn model.AgentTurn
	if err := f.db.Where("session_id = ? AND message_id = ?", session.ID, "runner-1").First(&turn).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != "done" || turn.ClaimOwner != "" {
		t.Fatalf("queued turn after Pi admission = %+v", turn)
	}
}

// C1-B: two Sessions in one space keep distinct Pi sessions and never share
// the claimed identity or history root.
func TestAgentSessionPiRunIsolatesTwoSessions(t *testing.T) {
	first := newGenericPiFixture(t, "iso", "drama-a")
	secondAssignment, err := first.svc.DelegateWork("user-a", DelegateRequest{
		WorkItemID: first.root.ID, SourceMessageID: "sup-iso", ActionKey: "drama-b", DefinitionID: "drama.agent",
		Scope:   as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"},
		Handoff: "梳理第二条支线", BudgetMicrocredits: 400_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := first.svc.agentSessionForOwner("user-a", secondAssignment.ExecutorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	second := &genericPiFixture{businessFixture: first.businessFixture, root: first.root, assignment: secondAssignment, session: secondSession}
	firstView := enqueueGenericPi(t, first, "d-1", "第一个会话")
	secondView := enqueueGenericPi(t, second, "d-1", "第二个会话")
	if firstView.RunID == secondView.RunID {
		t.Fatal("two sessions share one run")
	}
	claimA, err := first.svc.ClaimPiAgentForWorker("worker-a", "user-a")
	if err != nil || claimA == nil {
		t.Fatalf("claim A: %+v, %v", claimA, err)
	}
	claimB, err := first.svc.ClaimPiAgentForWorker("worker-b", "user-a")
	if err != nil || claimB == nil {
		t.Fatalf("claim B: %+v, %v", claimB, err)
	}
	if claimA.SessionID == claimB.SessionID || claimA.PiSessionID == claimB.PiSessionID {
		t.Fatalf("sessions collided: %q/%q %q/%q", claimA.SessionID, claimA.PiSessionID, claimB.SessionID, claimB.PiSessionID)
	}
	var sessions int64
	if err := first.db.Model(&model.CloudAgentPiSession{}).Count(&sessions).Error; err != nil || sessions != 2 {
		t.Fatalf("Pi sessions = %d, err=%v", sessions, err)
	}
}

// C1-C: an expired lease can be taken over once; the old epoch is fenced out
// of snapshots and renewals while the new epoch keeps working.
func TestAgentSessionPiRunLeaseTakeoverFencesOldEpoch(t *testing.T) {
	f := newGenericPiFixture(t, "lease", "drama")
	view := enqueueGenericPi(t, f, "d-1", "继续")
	first, err := f.svc.ClaimPiAgentForWorker("worker-a", "user-a")
	if err != nil || first == nil {
		t.Fatalf("first claim: %+v, %v", first, err)
	}
	oldOwner := "worker-a@" + itoa64(first.PiSessionLeaseEpoch)
	if _, err := f.svc.PiAgentSnapshot("user-a", view.RunID, oldOwner); err != nil {
		t.Fatalf("live snapshot: %v", err)
	}
	for _, row := range []any{&model.CloudAgentExecution{}, &model.CloudAgentPiSession{}} {
		if err := f.db.Model(row).Where("user_id = ?", "user-a").Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	taken, err := f.svc.ClaimPiAgentForWorker("worker-b", "user-a")
	if err != nil || taken == nil {
		t.Fatalf("takeover claim: %+v, %v", taken, err)
	}
	if taken.PiSessionLeaseEpoch != first.PiSessionLeaseEpoch+1 {
		t.Fatalf("takeover epoch = %d, want %d", taken.PiSessionLeaseEpoch, first.PiSessionLeaseEpoch+1)
	}
	newOwner := "worker-b@" + itoa64(taken.PiSessionLeaseEpoch)
	if _, err := f.svc.PiAgentSnapshot("user-a", view.RunID, oldOwner); err == nil {
		t.Fatal("old epoch still reads the snapshot")
	}
	if err := f.svc.RenewPiAgentLease("user-a", view.RunID, oldOwner); err == nil {
		t.Fatal("old epoch still renews the lease")
	}
	if err := f.svc.RenewPiAgentLease("user-a", view.RunID, newOwner); err != nil {
		t.Fatalf("new epoch renewal: %v", err)
	}
}

// C1-D: without a worker the run stays queued and auditable; enqueue never
// pretends it was executed.
func TestAgentSessionPiRunStaysQueuedWithoutWorker(t *testing.T) {
	f := newGenericPiFixture(t, "queued", "drama")
	view := enqueueGenericPi(t, f, "d-1", "等待执行")
	var run model.CloudAgentExecution
	if err := f.db.First(&run, "id = ?", view.RunID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" || run.Engine != "pi" || run.LeaseOwner != "" || run.LeaseExpiresAt != nil {
		t.Fatalf("queued run = %+v", run)
	}
	if snapshot, err := f.svc.ClaimPiAgentForWorker("idle-worker", "user-b"); err != nil || snapshot != nil {
		t.Fatalf("foreign claim = %+v, %v", snapshot, err)
	}
	if err := f.db.First(&run, "id = ?", view.RunID).Error; err != nil || run.Status != "queued" {
		t.Fatalf("queued run changed without a worker: %+v, %v", run, err)
	}
}

func itoa64(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
