package app

import (
	"strings"
	"sync"
	"testing"
	"time"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (w *workFixture) executor(t *testing.T) AssignmentView {
	t.Helper()
	assignment, err := w.delegate(t, "drama-episode-1", 400_000)
	if err != nil {
		t.Fatal(err)
	}
	return assignment
}

// A6-A: the user can message the executor Session of delegated work directly;
// a temporary subagent conversation still cannot be chatted with.
func TestAgentBusinessSessionDirectChat(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		assignment := w.executor(t)
		receipt, err := w.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: assignment.ExecutorSessionID, MessageID: "user-1", Text: "镜头再暗一点"})
		if err != nil || !receipt.Accepted || receipt.WorkItemID != assignment.WorkItemID {
			t.Fatalf("direct chat: %+v %v", receipt, err)
		}
		if _, err := w.svc.SendSessionMessage("user-b", SendSessionMessageRequest{SessionID: assignment.ExecutorSessionID, MessageID: "x", Text: "hi"}); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("foreign direct chat: %v", err)
		}
		// A subagent child conversation keeps its no-direct-chat rule.
		for _, row := range []any{
			&model.CloudAgentExecution{ID: "parent-run", UserID: "user-a", CanvasID: "c1", Status: "succeeded"},
			&model.CloudAgentExecution{ID: "child-run", UserID: "user-a", CanvasID: "c1", Status: "succeeded"},
			&model.AgentSubagentLink{ID: "link", UserID: "user-a", CanvasID: "c1", ParentRunID: "parent-run", ChildRunID: "child-run", IdempotencyKey: "link", DisplayName: "子", RoleLabel: "助手", Objective: "检查", Status: "succeeded"},
		} {
			if err := w.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if owned, err := w.svc.repo.AgentMessageConversationOwned("user-a", "child-run"); err != nil || owned {
			t.Fatalf("temporary subagent became chattable: %v %v", owned, err)
		}
	})
}

// A6-B/C: user and supervisor inputs to one work go through one ordered
// inbox; each is accepted once, actor kinds come from the server, and the
// supervisor is told once about the user's change.
func TestAgentUserAndSupervisorInputOrdering(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		assignment := w.executor(t)
		const workers = 10
		var wg sync.WaitGroup
		errs := make([]error, workers*2)
		start := make(chan struct{})
		for i := range workers {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				<-start
				_, errs[i] = w.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: assignment.ExecutorSessionID, MessageID: "user-" + string(rune('a'+i)), Text: "用户补充"})
			}(i)
			go func(i int) {
				defer wg.Done()
				<-start
				_, errs[workers+i] = w.svc.AppendWorkInput("user-a", AppendInputRequest{WorkItemID: assignment.WorkItemID, SourceMessageID: "sup-" + string(rune('a'+i)), Text: "总控补充", IdempotencyKey: "sup-" + string(rune('a'+i))})
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("input %d: %v", i, err)
			}
		}
		// Replays are accepted once.
		if _, err := w.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: assignment.ExecutorSessionID, MessageID: "user-a", Text: "用户补充"}); err != nil {
			t.Fatal(err)
		}
		var inputs []model.AgentWorkInput
		w.db.Where("work_item_id = ?", assignment.WorkItemID).Order("work_version ASC").Find(&inputs)
		if len(inputs) != workers*2 {
			t.Fatalf("inputs = %d, want %d", len(inputs), workers*2)
		}
		seen := map[int64]bool{}
		actors := map[string]int{}
		for _, input := range inputs {
			if seen[input.WorkVersion] {
				t.Fatalf("two inputs share work version %d", input.WorkVersion)
			}
			seen[input.WorkVersion] = true
			actors[input.Actor]++
		}
		if actors[as.ActorUser] != workers || actors[as.ActorSupervisor] != workers {
			t.Fatalf("actor kinds: %v", actors)
		}
		var userNotices int64
		w.db.Model(&model.AgentOutbox{}).Where("kind = ? AND target_session_id = ?", "user_input", w.supervisor.ID).Count(&userNotices)
		if userNotices != workers {
			t.Fatalf("supervisor told about user changes %d times, want %d", userNotices, workers)
		}
		child, _ := w.svc.GetWork("user-a", assignment.WorkItemID)
		if child.Version != int64(1+workers*2) {
			t.Fatalf("work version %d after %d inputs", child.Version, workers*2)
		}
	})
}

// A6-D: after the user stops the work, neither direct chat nor supervisor
// input is accepted, and replays of earlier inputs do not re-queue.
func TestAgentUserStopFencesQueuedDelegation(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		assignment := w.executor(t)
		if _, err := w.svc.AppendWorkInput("user-a", AppendInputRequest{WorkItemID: assignment.WorkItemID, SourceMessageID: "sup-1", Text: "继续", IdempotencyKey: "sup-1"}); err != nil {
			t.Fatal(err)
		}
		root, _ := w.svc.GetWork("user-a", w.work.ID)
		if _, err := w.svc.StopWork("user-a", WorkControlRequest{WorkItemID: w.work.ID, ExpectedVersion: root.Version, Reason: "用户停止"}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.svc.AppendWorkInput("user-a", AppendInputRequest{WorkItemID: assignment.WorkItemID, SourceMessageID: "sup-1", Text: "继续", IdempotencyKey: "sup-1"}); !ao.IsCode(err, ao.CodeWorkStopped) {
			t.Fatalf("replayed supervisor input after stop: %v", err)
		}
		if _, err := w.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: assignment.ExecutorSessionID, MessageID: "late", Text: "还在吗"}); !ao.IsCode(err, ao.CodeWorkStopped) {
			t.Fatalf("direct chat on stopped work: %v", err)
		}
		if pending, _ := w.svc.repo.PendingAgentOutbox("user-a", 50); len(pending) != 0 {
			t.Fatalf("deliveries pending after stop: %+v", pending)
		}
	})
}

// A6-E: one approval ID serves the supervisor and business views; approving
// twice executes once, and a changed quote or work expires it.
func TestAgentApprovalCrossViewIdempotency(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		assignment := w.executor(t)
		approval, err := w.svc.RequestApproval("user-a", ApprovalRequest{WorkItemID: assignment.WorkItemID, SessionID: assignment.ExecutorSessionID, CallID: "call-1", QuoteMicrocredits: 100_000, TargetVersion: "shot:v3", Summary: "生成 4 张分镜图"})
		if err != nil {
			t.Fatal(err)
		}
		again, err := w.svc.RequestApproval("user-a", ApprovalRequest{WorkItemID: assignment.WorkItemID, SessionID: assignment.ExecutorSessionID, CallID: "call-1", QuoteMicrocredits: 100_000, TargetVersion: "shot:v3", Summary: "生成 4 张分镜图"})
		if err != nil || again.ID != approval.ID {
			t.Fatalf("re-request must return the same approval: %+v %v", again, err)
		}
		forSupervisor, err := w.svc.WorkApprovals("user-a", w.work.ID)
		if err != nil || len(forSupervisor) != 1 || forSupervisor[0].ID != approval.ID {
			t.Fatalf("supervisor view: %+v %v", forSupervisor, err)
		}
		forBusiness, err := w.svc.WorkApprovals("user-a", assignment.WorkItemID)
		if err != nil || len(forBusiness) != 1 || forBusiness[0].ID != approval.ID {
			t.Fatalf("business view: %+v %v", forBusiness, err)
		}
		const workers = 10
		var wg sync.WaitGroup
		errs := make([]error, workers)
		for i := range workers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = w.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: approval.ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: approval.AuthorizationVersion})
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("decide %d: %v", i, err)
			}
		}
		consumed := 0
		for range 5 {
			if err := w.svc.ConsumeApproval("user-a", approval.ID, 100_000, "shot:v3"); err == nil {
				consumed++
			}
		}
		if consumed != 1 {
			t.Fatalf("approval consumed %d times", consumed)
		}
		second, err := w.svc.RequestApproval("user-a", ApprovalRequest{WorkItemID: assignment.WorkItemID, SessionID: assignment.ExecutorSessionID, CallID: "call-2", QuoteMicrocredits: 100_000, TargetVersion: "shot:v3"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: second.ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: second.AuthorizationVersion}); err != nil {
			t.Fatal(err)
		}
		if err := w.svc.ConsumeApproval("user-a", second.ID, 150_000, "shot:v3"); !ao.IsCode(err, ao.CodeVersionConflict) {
			t.Fatalf("consume with a changed quote: %v", err)
		}
		if _, err := w.svc.DecideApproval("user-b", DecideApprovalRequest{ApprovalID: approval.ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: approval.AuthorizationVersion}); !ao.IsCode(err, ao.CodeNotFound) {
			t.Fatalf("foreign decision: %v", err)
		}
	})
}

// A5-B/A6-B: the delivery pass hands each pending item to its target
// Session's inbox once, survives a restart, and skips stopped work.
func TestAgentOutboxDeliversOnceToTargetSession(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		assignment := w.executor(t)
		if _, err := w.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: assignment.ExecutorSessionID, MessageID: "user-1", Text: "更暗一点"}); err != nil {
			t.Fatal(err)
		}
		delivered, err := w.svc.deliverAgentOutbox("user-a", 50)
		if err != nil || delivered != 3 {
			t.Fatalf("delivered %d items: %v", delivered, err)
		}
		if again, err := w.svc.deliverAgentOutbox("user-a", 50); err != nil || again != 0 {
			t.Fatalf("second pass delivered %d: %v", again, err)
		}
		executor, err := w.svc.repo.AgentSessionMessages("user-a", assignment.ExecutorSessionID)
		if err != nil || len(executor) != 2 {
			t.Fatalf("executor inbox: %+v %v", executor, err)
		}
		for _, message := range executor {
			if message.Kind != as.MessageKindMessage || message.Role != "user" {
				t.Fatalf("executor message: %+v", message)
			}
		}
		if executor[1].Actor != as.ActorUser || executor[1].Body != "更暗一点" {
			t.Fatalf("user input not delivered as the user's message: %+v", executor[1])
		}
		supervisor, err := w.svc.repo.AgentSessionMessages("user-a", w.supervisor.ID)
		if err != nil || len(supervisor) != 1 || supervisor[0].Actor != as.ActorSystem {
			t.Fatalf("supervisor notice: %+v %v", supervisor, err)
		}
	})
}

// Outbox items written in the same clock tick are delivered in write order,
// not in the order of their random IDs.
func TestAgentOutboxDeliveryOrderIsWriteOrder(t *testing.T) {
	f := newStructuredFixture(t, newSQLiteTestDB(t))
	session := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "order"})
	tick := time.Now()
	// IDs chosen so that ID order is the reverse of write order.
	for i, id := range []string{"zzzz", "mmmm", "aaaa"} {
		item := &model.AgentOutbox{ID: id, OwnerID: "user-a", WorkItemID: "w", TargetSessionID: session.ID, Kind: "unknown", Payload: `{}`, Status: "pending", CreatedAt: tick}
		if err := f.svc.repo.InAgentWork("user-a", "", func(tx *repository.WorkTx) error { return tx.Enqueue(item) }); err != nil {
			t.Fatal(err)
		}
		if item.Seq != int64(i+1) {
			t.Fatalf("seq = %d, want %d", item.Seq, i+1)
		}
	}
	pending, err := f.svc.repo.PendingAgentOutbox("user-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, item := range pending {
		got = append(got, item.ID)
	}
	if strings.Join(got, ",") != "zzzz,mmmm,aaaa" {
		t.Fatalf("delivery order %v, want write order", got)
	}
}
