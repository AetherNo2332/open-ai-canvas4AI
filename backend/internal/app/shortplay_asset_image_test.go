package app

import (
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W05：参考图入队与回写。重点不是「有没有图」，而是三条不变量：
// 同资产只排一条在途 job；成功回写只在在途状态收口；失败不覆盖上一张可用图。

func seedAsset(t *testing.T, svc *Service, shortplayID string, id int, kind, name, status string) *model.ShortplayAsset {
	t.Helper()
	row := &model.ShortplayAsset{
		ShortplayID: shortplayID, ID: id, Kind: kind, Name: name,
		ImageStatus: status, Revision: 1,
	}
	if err := svc.repo.CreateShortplayAsset(row); err != nil {
		t.Fatal(err)
	}
	return row
}

func seedImageJob(t *testing.T, svc *Service, shortplayID, taskID string, assetID int) *model.ShortplayJob {
	t.Helper()
	job := &model.ShortplayJob{
		ID: "job-" + taskID, ShortplayID: shortplayID, Kind: shortplay.JobAssetImage,
		Status: shortplay.JobRunning, TaskID: taskID,
		PayloadJSON: shortplay.AssetImageJobPayload{AssetID: assetID, Prompt: "提示词", Appearance: "外貌"} .Encode(),
	}
	if err := svc.repo.CreateShortplayJob(job); err != nil {
		t.Fatal(err)
	}
	return job
}

func seedTask(t *testing.T, db *gorm.DB, id, status, result, failure string) {
	t.Helper()
	task := &model.Task{ID: id, UserID: "owner", Type: "canvas_image", Status: model.TaskStatus(status), ResultJSON: result, Error: failure}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
}

func TestQueueShortplayAssetImagesFiltersAndDedupes(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-img", "sp-img")
	seedAsset(t, svc, "sp-img", 1, shortplay.AssetCharacter, "沈青禾", "")
	seedAsset(t, svc, "sp-img", 2, shortplay.AssetCharacter, "顾行舟", "")
	seedAsset(t, svc, "sp-img", 3, shortplay.AssetProp, "油纸伞", "")
	locked := seedAsset(t, svc, "sp-img", 4, shortplay.AssetScene, "雨巷客栈", "")
	locked.Locked = true
	if applied, err := svc.repo.UpdateShortplayAsset(locked, 1); err != nil || !applied {
		t.Fatalf("lock scene = %v, %v", applied, err)
	}

	first, err := svc.QueueShortplayAssetImages("owner", "sp-img", shortplay.QueueAssetImagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Queued != 3 || first.Skipped != 1 || len(first.JobIDs) != 3 {
		t.Fatalf("first queue = %#v", first)
	}
	second, err := svc.QueueShortplayAssetImages("owner", "sp-img", shortplay.QueueAssetImagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Queued != 0 || second.Reused != 3 || second.Skipped != 1 {
		t.Fatalf("second queue = %#v", second)
	}
	var jobs int64
	if err := db.Model(&model.ShortplayJob{}).Where("kind = ?", shortplay.JobAssetImage).Count(&jobs).Error; err != nil || jobs != 3 {
		t.Fatalf("jobs = %d, err = %v", jobs, err)
	}
	// 只排某一类：其余类别被跳过；已在途的复用。
	onlyProp, err := svc.QueueShortplayAssetImages("owner", "sp-img", shortplay.QueueAssetImagesRequest{Kind: shortplay.AssetProp})
	if err != nil {
		t.Fatal(err)
	}
	if onlyProp.Queued != 0 || onlyProp.Reused != 1 || onlyProp.Skipped != 3 {
		t.Fatalf("only prop = %#v", onlyProp)
	}
	// 只重试失败：没有 failed 的资产就全跳过。
	onlyFailed, err := svc.QueueShortplayAssetImages("owner", "sp-img", shortplay.QueueAssetImagesRequest{OnlyFailed: true})
	if err != nil {
		t.Fatal(err)
	}
	if onlyFailed.Queued != 0 || onlyFailed.Reused != 0 || onlyFailed.Skipped != 4 {
		t.Fatalf("only failed = %#v", onlyFailed)
	}
}

func TestRunShortplayAssetImageJobAppliesOnceAndRejectsLateCallback(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-apply", "sp-apply")
	asset := seedAsset(t, svc, "sp-apply", 1, shortplay.AssetCharacter, "沈青禾", shortplay.ImageRunning)
	job := seedImageJob(t, svc, "sp-apply", "task-apply", asset.ID)
	seedTask(t, db, "task-apply", string(model.TaskStatusSucceeded), `{"images":[{"resourceId":"res-apply"}]}`, "")

	run := svc.runShortplayAssetImageJob(t.Context(), job)
	if run.Status != shortplay.JobSucceeded {
		t.Fatalf("run = %#v", run)
	}
	stored, err := svc.repo.ShortplayAssetByID("sp-apply", asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ImageStatus != shortplay.ImageReady || stored.CurrentImageID == 0 || stored.ImageVersion != 1 {
		t.Fatalf("asset after apply = %#v", stored)
	}
	if stored.ImageResourceID != "res-apply" {
		t.Fatalf("projection = %q", stored.ImageResourceID)
	}
	images, err := svc.repo.ShortplayAssetImages("sp-apply", asset.ID)
	if err != nil || len(images) != 1 || images[0].SourceImageID != 0 {
		t.Fatalf("images = %#v, %v", images, err)
	}

	// 迟到回调：状态已从 running 收口成 ready，CAS 拒绝第二次写入，也不新增图片行。
	late := svc.runShortplayAssetImageJob(t.Context(), job)
	if late.Status != shortplay.JobSucceeded {
		t.Fatalf("late run = %#v", late)
	}
	images, err = svc.repo.ShortplayAssetImages("sp-apply", asset.ID)
	if err != nil || len(images) != 1 {
		t.Fatalf("late callback added an image row: %#v, %v", images, err)
	}
	stored, _ = svc.repo.ShortplayAssetByID("sp-apply", asset.ID)
	if stored.ImageVersion != 1 {
		t.Fatalf("image version moved on late callback: %d", stored.ImageVersion)
	}
}

func TestRunShortplayAssetImageJobFailureKeepsPreviousImage(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-fail", "sp-fail")
	asset := seedAsset(t, svc, "sp-fail", 1, shortplay.AssetCharacter, "沈青禾", shortplay.ImageRunning)
	// 上一张可用图仍指向旧产物行；失败的重做不能把它清掉。
	if err := db.Model(&model.ShortplayAsset{}).Where("shortplay_id = ? AND id = ?", "sp-fail", asset.ID).
		Updates(map[string]any{"current_image_id": 7, "image_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	job := seedImageJob(t, svc, "sp-fail", "task-fail", asset.ID)
	seedTask(t, db, "task-fail", string(model.TaskStatusFailed), "", "上游返回 500")

	run := svc.runShortplayAssetImageJob(t.Context(), job)
	if run.Status != shortplay.JobFailed || run.LastError != "上游返回 500" {
		t.Fatalf("run = %#v", run)
	}
	stored, err := svc.repo.ShortplayAssetByID("sp-fail", asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ImageStatus != shortplay.ImageFailed || stored.CurrentImageID != 7 || stored.ImageVersion != 2 {
		t.Fatalf("failed redo changed the usable image: %#v", stored)
	}
	images, err := svc.repo.ShortplayAssetImages("sp-fail", asset.ID)
	if err != nil || len(images) != 1 || images[0].Status != shortplay.ImageFailed || images[0].ResourceID != "" {
		t.Fatalf("failure history = %#v, %v", images, err)
	}
}

func TestRunShortplayAssetImageJobWaitsForDerivedParentImage(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-wait", "sp-wait")
	base := seedAsset(t, svc, "sp-wait", 1, shortplay.AssetCharacter, "沈青禾", "")
	derived := &model.ShortplayAsset{
		ShortplayID: "sp-wait", ID: 2, Kind: shortplay.AssetCharacter, DerivedFrom: base.ID,
		VariantName: "战袍", Revision: 1, ImageStatus: shortplay.ImageRunning,
	}
	if err := svc.repo.CreateShortplayAsset(derived); err != nil {
		t.Fatal(err)
	}
	job := seedImageJob(t, svc, "sp-wait", "task-wait", derived.ID)
	job.TaskID = "" // 走未提交路径：父图未就绪时本轮不排队。

	run := svc.runShortplayAssetImageJob(t.Context(), job)
	if run.Status != "" {
		t.Fatalf("derived image without parent image = %#v, want pending", run)
	}
	if run.LastError != "" {
		t.Fatalf("pending run carried error: %q", run.LastError)
	}
}

func TestRunShortplayAssetImageJobRejectsLockedAsset(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-lock", "sp-lock")
	asset := seedAsset(t, svc, "sp-lock", 1, shortplay.AssetCharacter, "沈青禾", "")
	asset.Locked = true
	if applied, err := svc.repo.UpdateShortplayAsset(asset, 1); err != nil || !applied {
		t.Fatalf("lock = %v, %v", applied, err)
	}
	job := seedImageJob(t, svc, "sp-lock", "task-lock", asset.ID)
	job.TaskID = "" // 未提交路径才做锁定判断；已提交的 job 只读结果。
	run := svc.runShortplayAssetImageJob(t.Context(), job)
	if run.Status != shortplay.JobFailed || run.LastError == "" {
		t.Fatalf("locked run = %#v", run)
	}
}

// db 便于测试直接写入任务行；不在生产代码暴露。
