package app

import (
	"context"
	"sync"
	"testing"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// workFixture has a supervisor Session and a budget root for one user.
type workFixture struct {
	*structuredFixture
	supervisor as.SessionView
	work       WorkView
}

func newWorkFixture(t *testing.T, f *structuredFixture) *workFixture {
	t.Helper()
	w := &workFixture{structuredFixture: f}
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "sup-msg-1", Goal: "做一集短剧和一套电商素材", BudgetMicrocredits: 1_000_000, IdempotencyKey: "work-1"})
	if err != nil {
		t.Fatal(err)
	}
	w.work = work
	supervisor, err := f.svc.agentSessionForOwner("user-a", work.SupervisorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	w.supervisor = agentSessionView(*supervisor)
	return w
}

func (w *workFixture) delegate(t *testing.T, actionKey string, budget int64) (AssignmentView, error) {
	t.Helper()
	return w.svc.DelegateWork("user-a", DelegateRequest{
		WorkItemID: w.work.ID, SourceMessageID: "sup-msg-1", ActionKey: actionKey, DefinitionID: "drama.agent",
		Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, Handoff: "按大纲写第一集分镜", BudgetMicrocredits: budget,
	})
}

func workDialects(t *testing.T, run func(t *testing.T, w *workFixture)) {
	structuredDialects(t, func(t *testing.T, f *structuredFixture) { run(t, newWorkFixture(t, f)) })
}

// A5-A: replaying one supervisor step creates one child work, one assignment,
// one executor Session and one budget allocation; distinct action keys stay distinct.
func TestAgentWorkDelegationIdempotency(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		const workers = 20
		views := make([]AssignmentView, workers)
		errs := make([]error, workers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range workers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				views[i], errs[i] = w.delegate(t, "drama-episode-1", 400_000)
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("delegate %d: %v", i, err)
			}
			if views[i].ID != views[0].ID || views[i].ExecutorSessionID != views[0].ExecutorSessionID || views[i].BudgetGrantID != views[0].BudgetGrantID {
				t.Fatalf("replay %d diverged: %+v vs %+v", i, views[i], views[0])
			}
		}
		for table, want := range map[any]int64{&model.AgentWorkItem{}: 2, &model.AgentAssignment{}: 1, &model.PlatformBudgetGrant{}: 2} {
			if got := platformCount(t, w.db, table, "owner_id = ?", "user-a"); got != want {
				t.Fatalf("%T rows = %d, want %d", table, got, want)
			}
		}
		var sessions int64
		w.db.Model(&model.AgentSession{}).Where("owner_id = ? AND work_item_id = ?", "user-a", views[0].WorkItemID).Count(&sessions)
		if sessions != 1 {
			t.Fatalf("executor sessions = %d", sessions)
		}
		other, err := w.delegate(t, "drama-episode-2", 300_000)
		if err != nil || other.ID == views[0].ID || other.WorkItemID == views[0].WorkItemID {
			t.Fatalf("distinct action key must create distinct work: %+v %v", other, err)
		}
		// 400k + 300k fit into 1,000k; another 400k does not.
		if _, err := w.delegate(t, "drama-episode-3", 400_000); !cp.IsCode(err, cp.CodeBudgetExceeded) {
			t.Fatalf("over-budget delegation: %v", err)
		}
		executor, err := w.svc.agentSessionForOwner("user-a", views[0].ExecutorSessionID)
		if err != nil || executor.Application != as.ApplicationDrama || executor.WorkItemID != views[0].WorkItemID || executor.Role != as.RoleBusiness {
			t.Fatalf("executor session: %+v %v", executor, err)
		}
	})
}

// A5-B: the child work survives the supervisor turn; pending deliveries are
// durable rows, not goroutines of the supervisor.
func TestAgentWorkOutlivesSupervisorRun(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		assignment, err := w.delegate(t, "drama-episode-1", 400_000)
		if err != nil {
			t.Fatal(err)
		}
		// The supervisor's turn ends and its writer lease is released.
		if err := w.db.Model(&model.AgentSession{}).Where("id = ?", w.supervisor.ID).Updates(map[string]any{"lease_owner": "", "active_run_id": ""}).Error; err != nil {
			t.Fatal(err)
		}
		// A "restarted" service reads the same durable state.
		restarted := &Service{repo: w.svc.repo, dataDir: t.TempDir(), activeCancels: map[string]context.CancelFunc{}, coordinator: w.svc.coordinator}
		child, err := restarted.GetWork("user-a", assignment.WorkItemID)
		if err != nil || child.Status != ao.WorkRunning || child.ActiveAssignmentID != assignment.ID {
			t.Fatalf("child work after supervisor run: %+v %v", child, err)
		}
		pending, err := restarted.repo.PendingAgentOutbox("user-a", 10)
		if err != nil || len(pending) != 1 || pending[0].Kind != "assignment_started" || pending[0].TargetSessionID != assignment.ExecutorSessionID {
			t.Fatalf("durable delivery for the executor: %+v %v", pending, err)
		}
	})
}

// A5-C: a report, the work projection and the supervisor notification commit
// together; duplicate and stale reports change nothing.
func TestAgentReportOutboxAndStaleAssignment(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		assignment, err := w.delegate(t, "drama-episode-1", 400_000)
		if err != nil {
			t.Fatal(err)
		}
		child, _ := w.svc.GetWork("user-a", assignment.WorkItemID)
		report := SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: child.Version, ReportID: "r1", Status: ao.ReportProgress, Summary: "完成前三镜"}
		if _, err := w.svc.SubmitReport("user-a", report); err != nil {
			t.Fatal(err)
		}
		if _, err := w.svc.SubmitReport("user-a", report); err != nil {
			t.Fatalf("duplicate report must be accepted idempotently: %v", err)
		}
		reports, err := w.svc.ListReports("user-a", assignment.WorkItemID)
		if err != nil || len(reports) != 1 {
			t.Fatalf("duplicate stored twice: %+v %v", reports, err)
		}
		// A new assignment replaces the first; the old one's late report is kept for audit only.
		replacement, err := w.svc.ReassignWork("user-a", assignment.WorkItemID, child.Version)
		if err != nil || replacement.Epoch != assignment.Epoch+1 {
			t.Fatalf("reassign: %+v %v", replacement, err)
		}
		// The stale report carries the current work version, so only the
		// assignment and epoch checks can fence it.
		reassigned, _ := w.svc.GetWork("user-a", assignment.WorkItemID)
		stale := report
		stale.ReportID, stale.Status, stale.WorkVersion = "r-stale", ao.ReportCompleted, reassigned.Version
		if _, err := w.svc.SubmitReport("user-a", stale); !ao.IsCode(err, ao.CodeStaleAssignment) {
			t.Fatalf("stale report: %v", err)
		}
		child, _ = w.svc.GetWork("user-a", assignment.WorkItemID)
		if child.Status != ao.WorkRunning {
			t.Fatalf("stale report changed work: %+v", child)
		}
		done := SubmitReportRequest{AssignmentID: replacement.ID, LeaseEpoch: replacement.Epoch, WorkVersion: child.Version, ReportID: "r2", Status: ao.ReportCompleted, Summary: "第一集分镜完成"}
		if _, err := w.svc.SubmitReport("user-a", done); err != nil {
			t.Fatal(err)
		}
		child, _ = w.svc.GetWork("user-a", assignment.WorkItemID)
		if child.Status != ao.WorkCompleted || child.ActiveAssignmentID != "" {
			t.Fatalf("completed report: %+v", child)
		}
		var notices []model.AgentOutbox
		w.db.Where("owner_id = ? AND kind = ?", "user-a", "report").Order("created_at ASC").Find(&notices)
		if len(notices) != 2 || notices[0].TargetSessionID != w.supervisor.ID {
			t.Fatalf("supervisor notifications: %+v", notices)
		}
		// The supervisor reads reports, not the executor's history.
		if _, err := w.svc.buildAgentSessionContext("user-a", w.supervisor.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := w.svc.SubmitReport("user-b", done); !ao.IsCode(err, ao.CodeNotFound) {
			t.Fatalf("foreign report: %v", err)
		}
	})
}

// A5-D/E: delegation needs an authorized space; stopping work fences queued
// deliveries and replays, and later results are kept without reviving it.
func TestAgentStoppedWorkDoesNotRevive(t *testing.T) {
	workDialects(t, func(t *testing.T, w *workFixture) {
		forbidden := DelegateRequest{WorkItemID: w.work.ID, SourceMessageID: "sup-msg-1", ActionKey: "foreign", DefinitionID: "canvas.agent", Scope: as.Scope{Application: as.ApplicationCanvas, SpaceType: as.SpaceCanvas, SpaceID: "not-mine"}, BudgetMicrocredits: 1}
		if _, err := w.svc.DelegateWork("user-a", forbidden); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("delegation into a foreign space: %v", err)
		}
		assignment, err := w.delegate(t, "drama-episode-1", 400_000)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := w.svc.GetWork("user-a", w.work.ID)
		if _, err := w.svc.StopWork("user-a", WorkControlRequest{WorkItemID: w.work.ID, ExpectedVersion: root.Version, Reason: "用户停止"}); err != nil {
			t.Fatal(err)
		}
		child, _ := w.svc.GetWork("user-a", assignment.WorkItemID)
		if child.Status != ao.WorkCancelled {
			t.Fatalf("child must stop with its root: %+v", child)
		}
		if pending, _ := w.svc.repo.PendingAgentOutbox("user-a", 10); len(pending) != 0 {
			t.Fatalf("queued deliveries survived stop: %+v", pending)
		}
		if _, err := w.delegate(t, "drama-episode-1", 400_000); !ao.IsCode(err, ao.CodeWorkStopped) {
			t.Fatalf("replayed delegation after stop: %v", err)
		}
		if _, err := w.delegate(t, "drama-episode-9", 1); !ao.IsCode(err, ao.CodeWorkStopped) {
			t.Fatalf("new delegation after stop: %v", err)
		}
		late := SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: child.Version, ReportID: "late", Status: ao.ReportCompleted}
		if _, err := w.svc.SubmitReport("user-a", late); !ao.IsCode(err, ao.CodeStaleAssignment) && !ao.IsCode(err, ao.CodeWorkStopped) {
			t.Fatalf("late report: %v", err)
		}
		if child, _ := w.svc.GetWork("user-a", assignment.WorkItemID); child.Status != ao.WorkCancelled {
			t.Fatalf("late report revived work: %+v", child)
		}
		grant, err := w.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, assignment.BudgetGrantID)
		if err != nil || grant.Status != cp.BudgetStatusClosed {
			t.Fatalf("stopped work must close its budget: %+v %v", grant, err)
		}
		if _, err := w.svc.StopWork("user-b", WorkControlRequest{WorkItemID: w.work.ID, ExpectedVersion: 99}); !ao.IsCode(err, ao.CodeNotFound) {
			t.Fatalf("foreign stop: %v", err)
		}
	})
}
