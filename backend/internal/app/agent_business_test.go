package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// businessFixture combines a text channel (for Agent steps) and the image
// channel of the platform fixture (for workbench generations) in one service.
type businessFixture struct {
	*platformImageFixture
	text *structuredFixture
}

func newBusinessFixture(t *testing.T) *businessFixture {
	t.Helper()
	image := workbenchPlatformFixture(t)
	text := attachStructuredText(t, image.svc, image.db)
	if err := image.db.Create(&model.Project{ID: "drama-1", UserID: "user-a", Name: "短剧", Type: "short-drama", Status: model.ProjectStatusActive, Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := image.db.Create(&model.DramaProduction{ProjectID: "drama-1", UserID: "user-a", Status: model.DramaStatusPaused, Stage: model.DramaStageProduce, BudgetMicrocredits: 5_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	return &businessFixture{platformImageFixture: image, text: text}
}

func toolCallReply(id, name, arguments string) string {
	raw, _ := json.Marshal(arguments)
	return `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(raw) + `}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`
}

func textReply(text string) string {
	return `{"choices":[{"message":{"role":"assistant","content":"` + text + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`
}

// runTurn advances a turn until it finishes, running queued tasks between
// advances the way the background worker would.
func (f *businessFixture) runTurn(t *testing.T, turn businessTurn) (businessTurnResult, error) {
	t.Helper()
	for range 10 {
		result, done, err := f.svc.advanceBusinessTurn("user-a", turn)
		if err != nil || done {
			return result, err
		}
		f.drain(t)
	}
	t.Fatal("turn did not finish")
	return businessTurnResult{}, nil
}

// drain runs every queued task (model steps and generations).
func (f *businessFixture) drain(t *testing.T) {
	t.Helper()
	for range 10 {
		claimed, err := f.svc.repo.ClaimNextTask("business-worker", 60_000_000_000)
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil {
			return
		}
		_ = f.svc.taskWorker().processClaimedTask(claimed, nil)
	}
}

// A7-A/B/D: the supervisor delegates a drama task and an ecommerce task; each
// business Agent runs in its own Session, calls its own business service
// through the adapter, and reports. Generations stay traceable to the batch.
func TestAgentSupervisorDelegatesDramaAndEcommerce(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "sup-1", Goal: "第一集分镜 + 一套主图", BudgetMicrocredits: 3_000_000, IdempotencyKey: "biz-work"})
	if err != nil {
		t.Fatal(err)
	}
	drama, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "sup-1", ActionKey: "drama", DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, Handoff: "梳理第一集", BudgetMicrocredits: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	ecommerce, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "sup-1", ActionKey: "ecommerce", DefinitionID: "ecommerce.agent", Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_agent1"}, Handoff: "白底陶瓷杯主图", BudgetMicrocredits: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if drama.ExecutorSessionID == ecommerce.ExecutorSessionID {
		t.Fatal("two businesses share one Session")
	}
	textModel := cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}

	// Drama: the model reads the project through the adapter, then answers.
	f.text.script(toolCallReply("call-d1", "drama_summary", `{}`), textReply("完成"))
	result, err := f.runTurn(t, businessTurn{Assignment: drama, MessageID: "d-1", Text: "看看项目状态", Model: textModel})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Calls) != 1 || !strings.Contains(result.Calls[0].Result.Text, "短剧") {
		t.Fatalf("drama tool result: %+v", result)
	}
	// Ecommerce: the model submits a workbench image through the existing bridge.
	f.text.script(toolCallReply("call-e1", "ecommerce_generate_image", `{"prompt":"白底陶瓷杯","channelId":"`+f.channelID+`","model":"`+f.modelKey+`","parameters":{"size":"1024x1024"},"index":0,"count":1}`), textReply("完成"))
	result, err = f.runTurn(t, businessTurn{Assignment: ecommerce, MessageID: "e-1", Text: "生成一张主图", Model: textModel})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Calls) != 1 || result.Calls[0].Result.Operation == "" {
		t.Fatalf("ecommerce tool result: %+v", result)
	}
	snapshot, err := f.svc.CreationPlatform().Task(context.Background(), cp.Caller{UserID: "user-a"}, result.Calls[0].Result.Operation)
	if err != nil || snapshot.Business.Application != "ecommerce" || snapshot.Business.ProjectID != "batch_agent1" {
		t.Fatalf("generation must be a workbench batch task: %+v %v", snapshot, err)
	}
	grant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, ecommerce.BudgetGrantID)
	if err != nil || grant.Usage.Spent() == 0 {
		t.Fatalf("generation not charged to the ecommerce grant: %+v %v", grant, err)
	}
	// Replaying the same turn neither calls the model again nor resubmits.
	calls := f.text.calls.Load()
	if _, err := f.runTurn(t, businessTurn{Assignment: ecommerce, MessageID: "e-1", Text: "生成一张主图", Model: textModel}); err != nil {
		t.Fatal(err)
	}
	if f.text.calls.Load() != calls || platformCount(t, f.db, &model.PlatformSubmission{}, "application = ? AND object_type = ?", "ecommerce", "workbench_image") != 1 {
		t.Fatalf("replayed turn re-ran: model calls %d->%d", calls, f.text.calls.Load())
	}
	// Both report; the supervisor sees two reports and no business history.
	for _, assignment := range []AssignmentView{drama, ecommerce} {
		child, _ := f.svc.GetWork("user-a", assignment.WorkItemID)
		if _, err := f.svc.SubmitReport("user-a", SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: child.Version, ReportID: "done", Status: ao.ReportCompleted, Summary: "完成"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.deliverAgentOutbox("user-a", 50); err != nil {
		t.Fatal(err)
	}
	supervisor, err := f.svc.buildAgentSessionContext("user-a", work.SupervisorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	text := agentContextText(supervisor)
	if strings.Count(text, "子工作报告") != 2 || strings.Contains(text, "看看项目状态") || strings.Contains(text, "白底陶瓷杯主图\n") && strings.Contains(text, "生成一张主图") {
		t.Fatalf("supervisor context: %s", text)
	}
}

// A7-B: an adapter re-checks scope on every call; a tool of another
// application or a forged project is refused before any business write.
func TestAgentBusinessToolsPreserveDomainRules(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "s", Goal: "g", BudgetMicrocredits: 1_000_000, IdempotencyKey: "rules"})
	if err != nil {
		t.Fatal(err)
	}
	drama, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "s", ActionKey: "d", DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, BudgetMicrocredits: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := f.svc.agentSessionForOwner("user-a", drama.ExecutorSessionID)
	call := as.ToolInvocation{OwnerID: "user-a", SessionID: session.ID, Scope: as.Scope{Application: session.Application, SpaceType: session.SpaceType, SpaceID: session.SpaceID}, WorkItemID: drama.WorkItemID, BudgetGrantID: drama.BudgetGrantID, CallID: "x"}
	call.Name = "ecommerce_generate_image"
	if _, err := f.svc.executeBusinessTool(context.Background(), call); !as.IsCode(err, as.CodeForbidden) {
		t.Fatalf("tool of another application: %v", err)
	}
	call.Name, call.Scope.SpaceID = "drama_summary", "someone-elses"
	if _, err := f.svc.executeBusinessTool(context.Background(), call); !as.IsCode(err, as.CodeScopeMismatch) {
		t.Fatalf("forged scope: %v", err)
	}
	call.Scope.SpaceID, call.OwnerID = "drama-1", "user-b"
	if _, err := f.svc.executeBusinessTool(context.Background(), call); !as.IsCode(err, as.CodeNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	// Starting production goes through the drama service's own checks (here:
	// no models configured), not around them.
	call.OwnerID, call.Name = "user-a", "drama_set_running"
	call.Arguments = json.RawMessage(`{"running":true}`)
	if _, err := f.svc.executeBusinessTool(context.Background(), call); err == nil || !strings.Contains(err.Error(), "模型") {
		t.Fatalf("drama rules bypassed: %v", err)
	}
	if production, _ := f.svc.repo.DramaProduction("drama-1"); production.Status != model.DramaStatusPaused {
		t.Fatalf("production started without its checks: %+v", production)
	}
}

// testBusinessAdapter is a minimal business registered only in the test.
type testBusinessAdapter struct{ executed *int }

func (testBusinessAdapter) Application() string  { return "notebook" }
func (testBusinessAdapter) SpaceTypes() []string { return []string{"notebook_page"} }
func (testBusinessAdapter) ValidateSpace(context.Context, string, as.Scope) error {
	return nil
}
func (testBusinessAdapter) Tools() []as.ToolSpec {
	return []as.ToolSpec{{Name: "notebook_write", Description: "写一页", Parameters: json.RawMessage(`{"type":"object"}`)}}
}
func (a testBusinessAdapter) Execute(context.Context, as.ToolInvocation) (as.ToolResult, error) {
	*a.executed++
	return as.ToolResult{Text: "已写入"}, nil
}

// A7-E: a new business registers an adapter and a definition; the supervisor
// can delegate to it and read its report without touching the core.
func TestAgentNewBusinessRegistration(t *testing.T) {
	f := newBusinessFixture(t)
	executed := 0
	if err := f.svc.registerAgentBusiness(testBusinessAdapter{executed: &executed}, as.Definition{ID: "notebook.agent", Version: "1", Role: as.RoleBusiness, Application: "notebook", SpaceTypes: []string{"notebook_page"}, Title: "笔记 Agent", Tools: []string{"notebook_write"}}); err != nil {
		t.Fatal(err)
	}
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "n", Goal: "写一页", BudgetMicrocredits: 100_000, IdempotencyKey: "nb"})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "n", ActionKey: "nb", DefinitionID: "notebook.agent", Scope: as.Scope{Application: "notebook", SpaceType: "notebook_page", SpaceID: "p1"}, BudgetMicrocredits: 50_000})
	if err != nil {
		t.Fatal(err)
	}
	f.text.script(toolCallReply("call-n1", "notebook_write", `{}`), textReply("完成"))
	if _, err := f.runTurn(t, businessTurn{Assignment: assignment, MessageID: "n-1", Text: "写", Model: cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}}); err != nil {
		t.Fatal(err)
	}
	if executed != 1 {
		t.Fatalf("new business tool executed %d times", executed)
	}
	child, _ := f.svc.GetWork("user-a", assignment.WorkItemID)
	if _, err := f.svc.SubmitReport("user-a", SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: child.Version, ReportID: "r", Status: ao.ReportCompleted, Summary: "写好了"}); err != nil {
		t.Fatal(err)
	}
	if reports, err := f.svc.ListReports("user-a", assignment.WorkItemID); err != nil || len(reports) != 1 || !reports[0].Applied {
		t.Fatalf("report from the new business: %+v %v", reports, err)
	}
}
