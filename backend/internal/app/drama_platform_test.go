package app

import (
	"context"
	"fmt"
	"testing"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// dramaPlatformRun 构造一个只用于提交路径的短剧运行：生产路径（无 dramaTaskHook）经平台提交到真实 worker。
func dramaPlatformRun(t *testing.T, f *platformImageFixture) (*dramaRun, *model.DramaJob) {
	t.Helper()
	project := &model.Project{ID: "drama-project", UserID: "user-a", Name: "斗破"}
	if err := f.db.Create(project).Error; err != nil {
		t.Fatal(err)
	}
	job := &model.DramaJob{ID: "drama-job-1", ProjectID: project.ID, Kind: model.DramaJobCastImage, CastID: "cast-1", Status: model.DramaJobQueued}
	run := &dramaRun{s: f.svc, project: project, production: &model.DramaProduction{ProjectID: project.ID, UserID: "user-a"}, settings: DramaSettings{Models: DramaModels{Image: DramaModelChoice{ChannelID: f.channelID, ModelKey: f.modelKey}}}}
	run.submitting = job
	return run, job
}

func dramaImageInput() map[string]any {
	return map[string]any{"config": map[string]any{"size": "1024x1024"}, "metadata": map[string]any{"source": "drama_production"}}
}

// S6-A：短剧在 Go 内经平台提交，不经过浏览器或 HTTP；结果由真实 worker 生成并带短剧归属。
func TestDramaPlatformSubmitsInGoWithBusinessAttribution(t *testing.T) {
	f := newPlatformImageFixture(t)
	run, job := dramaPlatformRun(t, f)
	task, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女站在大厅中央", dramaImageInput())
	if err != nil {
		t.Fatal(err)
	}
	f.runQueued(t)
	snapshot, err := f.svc.CreationPlatform().Task(context.Background(), cp.Caller{UserID: "user-a"}, task.ID)
	if err != nil {
		t.Fatalf("drama task must be a platform task: %v", err)
	}
	if snapshot.Business.Application != "drama" || snapshot.Business.ProjectID != "drama-project" || snapshot.Business.ObjectType != model.DramaJobCastImage || snapshot.Business.OperationID != job.ID+":0" {
		t.Fatalf("drama business ref: %+v", snapshot.Business)
	}
	if snapshot.Execution != cp.ExecutionSucceeded || snapshot.Billing.Status != cp.BillingSettled {
		t.Fatalf("drama snapshot: %+v", snapshot)
	}
	stored, err := f.svc.repo.Task(task.ID)
	if err != nil || stored.ProjectID != "drama-project" {
		t.Fatalf("task must stay attributed to the drama project for budget totals: %+v %v", stored, err)
	}
}

// S6-B：调度器在提交后、保存作业前重启，同一作业再次派发会取回原任务，不重复生成或冻结。
func TestDramaPlatformJobReplay(t *testing.T) {
	f := newPlatformImageFixture(t)
	run, job := dramaPlatformRun(t, f)
	first, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女站在大厅中央", dramaImageInput())
	if err != nil {
		t.Fatal(err)
	}
	// 重启后的调度器只读到未更新的作业行（TaskID 为空、无历次提交），用同样的输入再次提交。
	restarted := &dramaRun{s: &Service{repo: f.svc.repo, dataDir: f.svc.dataDir, activeCancels: f.svc.activeCancels, coordinator: f.svc.coordinator}, project: run.project, production: run.production, settings: run.settings}
	restarted.submitting = &model.DramaJob{ID: job.ID, ProjectID: job.ProjectID, Kind: job.Kind, CastID: job.CastID, Status: model.DramaJobQueued}
	again, err := restarted.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女站在大厅中央", dramaImageInput())
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("job replay created a second task: %s vs %s", again.ID, first.ID)
	}
	if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != 1 {
		t.Fatalf("job replay reserved credits again: %d orders", got)
	}
	// 原任务失败后（失败重试），同一作业的下一次提交是新的尝试：新任务、新冻结。提交序号以任务真实终态为准。
	if err := f.db.Model(&model.Task{}).Where("id = ?", first.ID).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	recordDramaAttempt(restarted.submitting, first.ID, string(model.TaskStatusFailed), "上游失败", 0)
	retry, err := restarted.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女站在大厅中央", dramaImageInput())
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == first.ID {
		t.Fatal("an explicit retry must create a new task")
	}
	if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != 2 {
		t.Fatalf("retry must reserve once more: %d orders", got)
	}
}

// S6-D：短剧费用按平台归属汇总，冻结与实收分列，不重复计入。
func TestDramaPlatformBudgetAggregation(t *testing.T) {
	f := newPlatformImageFixture(t)
	run, _ := dramaPlatformRun(t, f)
	if _, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女", dramaImageInput()); err != nil {
		t.Fatal(err)
	}
	f.runQueued(t)
	run.submitting = &model.DramaJob{ID: "drama-job-2", ProjectID: "drama-project", Kind: model.DramaJobCastImage, CastID: "cast-2"}
	if _, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "黑发少年", dramaImageInput()); err != nil {
		t.Fatal(err)
	}
	aggregate, err := f.svc.CreationPlatform().BillingAggregate(context.Background(), cp.Caller{UserID: "user-a"}, cp.BusinessRef{Application: "drama", ProjectID: "drama-project"})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.TaskCount != 2 || aggregate.ChargedMicrocredits != 100_000 || aggregate.ReservedMicrocredits != 200_000 {
		t.Fatalf("drama aggregate: %+v", aggregate)
	}
	// 短剧自己的预算口径（按订单冻结 + 实收）与平台汇总一致。
	totals, err := f.svc.dramaBillingTotals(run.production)
	if err != nil {
		t.Fatal(err)
	}
	if totals.ChargedMicrocredits()+totals.HeldMicrocredits() != 200_000 {
		t.Fatalf("drama budget totals = charged %d + held %d, want 200000", totals.ChargedMicrocredits(), totals.HeldMicrocredits())
	}
}

// 审查 F4：同步阶段的自动精简续作也必须有稳定的作业身份：提交后、作业保存前崩溃，下一轮重放不能再建一个精简任务。
func TestDramaPlatformCondenseContinuationReplays(t *testing.T) {
	f := newPlatformImageFixture(t)
	run, job := dramaPlatformRun(t, f)
	first, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "撰写", dramaImageInput())
	if err != nil {
		t.Fatal(err)
	}
	f.runQueued(t) // 首个任务成功，同步阶段据此提交续作。
	// 同步阶段：每轮新建 dramaRun，派发字段为空；续作必须仍带作业身份。
	sync := &dramaRun{s: f.svc, project: run.project, production: run.production, settings: run.settings}
	sync.submitting = &model.DramaJob{ID: job.ID, ProjectID: job.ProjectID, Kind: job.Kind, CastID: job.CastID, TaskID: first.ID, Status: model.DramaJobRunning}
	condense, err := sync.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "精简", dramaImageInput())
	if err != nil {
		t.Fatal(err)
	}
	// 崩溃：作业行没保存 TaskID=condense。下一轮同步再次处理同一个成功任务、再次提交同样的续作。
	again := &dramaRun{s: f.svc, project: run.project, production: run.production, settings: run.settings}
	again.submitting = &model.DramaJob{ID: job.ID, ProjectID: job.ProjectID, Kind: job.Kind, CastID: job.CastID, TaskID: first.ID, Status: model.DramaJobRunning}
	replayed, err := again.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "精简", dramaImageInput())
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != condense.ID || condense.ID == first.ID {
		t.Fatalf("condense continuation not idempotent: first=%s condense=%s replay=%s", first.ID, condense.ID, replayed.ID)
	}
}

// 审查 F5：作业历史只保留 20 条，提交身份不能依赖它的长度；第 21 次之后的重试仍是新的尝试。
func TestDramaPlatformRetryIdentityIgnoresHistoryTrim(t *testing.T) {
	f := newPlatformImageFixture(t)
	run, job := dramaPlatformRun(t, f)
	for index := 0; index < 20; index++ {
		recordDramaAttempt(job, fmt.Sprintf("old-%d", index), string(model.TaskStatusFailed), "旧失败", 0)
	}
	previous := ""
	for round := 0; round < 3; round++ {
		task, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女", dramaImageInput())
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if task.ID == previous {
			t.Fatalf("round %d reused the previous task %s after the history window was full", round, task.ID)
		}
		previous = task.ID
		// 本轮失败并记录；历史仍被裁剪在 20 条。
		if err := f.db.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailed).Error; err != nil {
			t.Fatal(err)
		}
		recordDramaAttempt(job, task.ID, string(model.TaskStatusFailed), "上游失败", 0)
	}
}
