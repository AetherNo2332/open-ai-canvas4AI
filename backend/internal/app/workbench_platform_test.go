package app

import (
	"bytes"
	"context"
	"testing"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/workbench"
)

func workbenchPlatformFixture(t *testing.T) *platformImageFixture {
	t.Helper()
	f := newPlatformImageFixture(t)
	if err := f.db.Create(&model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: `{"imageWorkbenchEnabled":true,"creditsEnabled":true,"taskCenterEnabled":true}`}).Error; err != nil {
		t.Fatal(err)
	}
	// 参考图必须是真实存储的文件：worker 会读取它作为上游输入。
	picture := mediaTestPNG(t)
	stored, _, err := f.svc.storeResource("user-a", "image", "ref.png", "image/png", int64(len(picture)), 2, 3, 0, bytes.NewReader(picture), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	f.referenceID = stored.ID
	return f
}

func workbenchImageRequest(f *platformImageFixture, batchID string, index, count int) WorkbenchTaskInput {
	return WorkbenchTaskInput{
		Kind: WorkbenchKindImage, Prompt: "白底陶瓷杯", Model: f.modelKey, ChannelID: f.channelID,
		Parameters: map[string]any{"size": "1024x1024"},
		References: []workbench.TaskReference{{ResourceID: f.referenceID, Kind: "image", Role: "reference"}},
		Batch:      &workbench.TaskBatch{ID: batchID, Index: index, Count: count},
	}
}

// S5-A：工作台经平台提交，任务、报价、费用与作品可相互追踪；结果按平台快照读取。
func TestWorkbenchSubmitsThroughPlatform(t *testing.T) {
	f := workbenchPlatformFixture(t)
	task, err := f.svc.CreateWorkbenchTask("user-a", workbenchImageRequest(f, "batch-0001", 0, 1), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if task.WorkbenchKind != WorkbenchKindImage || task.BillingOrderID == "" {
		t.Fatalf("workbench task: %+v", task)
	}
	f.runQueued(t)
	snapshot, err := f.svc.CreationPlatform().Task(context.Background(), cp.Caller{UserID: "user-a"}, task.ID)
	if err != nil {
		t.Fatalf("workbench task must be a platform task: %v", err)
	}
	if snapshot.Business.Application != "ecommerce" || snapshot.Business.ProjectID != "batch-0001" || snapshot.Business.OperationID != "batch-0001:0" {
		t.Fatalf("business ref: %+v", snapshot.Business)
	}
	if snapshot.Execution != cp.ExecutionSucceeded || snapshot.Billing.Status != cp.BillingSettled || len(snapshot.Result.Resources) != 1 {
		t.Fatalf("workbench snapshot: %+v", snapshot)
	}
	aggregate, err := f.svc.CreationPlatform().BillingAggregate(context.Background(), cp.Caller{UserID: "user-a"}, cp.BusinessRef{Application: "ecommerce", ProjectID: "batch-0001"})
	if err != nil || aggregate.TaskCount != 1 || aggregate.ChargedMicrocredits != 100_000 {
		t.Fatalf("batch aggregate: %+v %v", aggregate, err)
	}
	// 工作台列表仍能看到这条任务（批次归组靠既有 workbench_kind 与 metadata）。
	list, err := f.svc.WorkbenchTasks("user-a", WorkbenchKindImage, false, 10)
	if err != nil || len(list) != 1 || list[0].ID != task.ID {
		t.Fatalf("workbench list: %+v %v", list, err)
	}
}

// S5-B：批次部分失败后重提剩余项，已接受项按同一批次与序号回放，不重新创建或冻结。
func TestWorkbenchBatchRetryDoesNotDuplicateAcceptedItems(t *testing.T) {
	f := workbenchPlatformFixture(t)
	first, err := f.svc.CreateWorkbenchTask("user-a", workbenchImageRequest(f, "batch-0002", 0, 3), "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 第二项提交时积分不足：已接受的第一项不受影响。
	if err := f.db.Model(&model.CreditAccount{}).Where("user_id = ?", "user-a").Update("available_microcredits", 1).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateWorkbenchTask("user-a", workbenchImageRequest(f, "batch-0002", 1, 3), "", ""); err == nil {
		t.Fatal("insufficient credits must reject the second item")
	}
	if err := f.db.Model(&model.CreditAccount{}).Where("user_id = ?", "user-a").Update("available_microcredits", 10_000_000).Error; err != nil {
		t.Fatal(err)
	}
	// 页面“继续提交”会把整批未完成项连同已接受项一起重投。
	for index := 0; index < 3; index++ {
		task, err := f.svc.CreateWorkbenchTask("user-a", workbenchImageRequest(f, "batch-0002", index, 3), "", "")
		if err != nil {
			t.Fatalf("retry index %d: %v", index, err)
		}
		if index == 0 && task.ID != first.ID {
			t.Fatalf("accepted item was recreated: %s vs %s", task.ID, first.ID)
		}
	}
	if got := platformCount(t, f.db, &model.Task{}, "user_id = ?", "user-a"); got != 3 {
		t.Fatalf("tasks = %d, want 3", got)
	}
	if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != 3 {
		t.Fatalf("billing orders = %d, want 3", got)
	}
	// 同一批次同一序号但改了参数：返回冲突，而不是悄悄换成新任务。
	changed := workbenchImageRequest(f, "batch-0002", 0, 3)
	changed.Prompt = "黑底陶瓷杯"
	if _, err := f.svc.CreateWorkbenchTask("user-a", changed, "", ""); platformErrorCode(err) != cp.CodeIdempotencyConflict {
		t.Fatalf("changed item with same batch slot: %v", err)
	}
}

// 没有批次的单次提交仍然每次新建任务，保持原有“再生成一次”的语义。
func TestWorkbenchWithoutBatchKeepsCreatingNewTasks(t *testing.T) {
	f := workbenchPlatformFixture(t)
	req := workbenchImageRequest(f, "", 0, 1)
	req.Batch = nil
	a, err := f.svc.CreateWorkbenchTask("user-a", req, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.CreateWorkbenchTask("user-a", req, "", "")
	if err != nil || a.ID == b.ID {
		t.Fatalf("unbatched submits must create separate tasks: %s %s %v", a.ID, b.ID, err)
	}
}
