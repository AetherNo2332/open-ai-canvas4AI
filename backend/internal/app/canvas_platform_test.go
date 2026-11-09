package app

import (
	"context"
	"testing"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// S7-A：画布命令提交的任务同时是平台任务：同一事务写入任务、冻结、画布绑定与平台记录，归属画布与目标节点。
func TestCanvasSubmitIsPlatformTaskInSameTransaction(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	config := map[string]any{"channelId": "channel", "model": "text-test"}
	quote := splitQuote(t, s, config)
	result, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", Config: config, QuoteHash: quote, OperationID: "split-all"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.CreationPlatform().Task(context.Background(), cp.Caller{UserID: "user"}, result.TaskID)
	if err != nil {
		t.Fatalf("canvas task must be a platform task: %v", err)
	}
	if snapshot.Business.Application != "canvas" || snapshot.Business.ProjectID != "split-canvas" || snapshot.Business.ObjectType != "node" || snapshot.Business.ObjectID != "board" {
		t.Fatalf("canvas business ref: %+v", snapshot.Business)
	}
	if snapshot.Billing.Status != cp.BillingReserved {
		t.Fatalf("canvas billing: %+v", snapshot.Billing)
	}
	var bindings, submissions int64
	db.Model(&model.CanvasTaskBinding{}).Where("task_id = ?", result.TaskID).Count(&bindings)
	db.Model(&model.PlatformSubmission{}).Where("task_id = ?", result.TaskID).Count(&submissions)
	if bindings != 1 || submissions != 1 {
		t.Fatalf("binding=%d submission=%d, want 1/1", bindings, submissions)
	}
	// 同一画布操作重放走画布命令回执，不再新建任务或平台记录。
	again, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", Config: config, QuoteHash: quote, OperationID: "split-all"})
	if err != nil || again.TaskID != result.TaskID {
		t.Fatalf("replayed canvas operation: %+v %v", again, err)
	}
	db.Model(&model.PlatformSubmission{}).Where("user_id = ?", "user").Count(&submissions)
	if submissions != 1 {
		t.Fatalf("replay created another platform record: %d", submissions)
	}
	events := platformEvents(t, s.CreationPlatform(), cp.Caller{UserID: "user"}, cp.EventQuery{TaskID: result.TaskID})
	if len(events.Events) != 1 || events.Events[0].Type != cp.EventSubmitted {
		t.Fatalf("canvas submit must publish one submitted event: %+v", events.Events)
	}
}

// S7-A：画布提交在平台记录写入失败时整体回滚，不留下任务、冻结或画布绑定。
func TestCanvasSubmitRollsBackWithPlatformRecord(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	dropTrigger := injectInsertFailure(t, db, "platform_submissions")
	defer dropTrigger()
	config := map[string]any{"channelId": "channel", "model": "text-test"}
	quote := splitQuote(t, s, config)
	if _, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", Config: config, QuoteHash: quote, OperationID: "split-fail"}); err == nil {
		t.Fatal("canvas submit must fail when the platform record cannot be written")
	}
	for _, check := range []struct {
		value any
		name  string
	}{{&model.Task{}, "tasks"}, {&model.BillingOrder{}, "billing_orders"}, {&model.CanvasTaskBinding{}, "canvas_task_bindings"}} {
		var count int64
		db.Model(check.value).Count(&count)
		if count != 0 {
			t.Fatalf("%s left %d rows after rollback", check.name, count)
		}
	}
}

func splitQuote(t *testing.T, s *Service, config map[string]any) string {
	t.Helper()
	quote, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", Config: config, QuoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	return quote.Quote.QuoteHash
}
