package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W05 音色与调度推进：三条不变量与图片相同，另加「已提交的 job 必须被下一 tick 收口」。
// 后者是真实缺口：只领 queued 会让提交后的 running job 永远停在 running。

func seedVoicedAsset(t *testing.T, svc *Service, shortplayID string, id int, kind, name, voice, status string) *model.ShortplayAsset {
	t.Helper()
	row := &model.ShortplayAsset{
		ShortplayID: shortplayID, ID: id, Kind: kind, Name: name, Voice: voice,
		AudioStatus: status, Revision: 1,
	}
	if err := svc.repo.CreateShortplayAsset(row); err != nil {
		t.Fatal(err)
	}
	return row
}

func seedAudioJob(t *testing.T, svc *Service, shortplayID, taskID string, assetID int) *model.ShortplayJob {
	t.Helper()
	job := &model.ShortplayJob{
		ID: "job-" + taskID, ShortplayID: shortplayID, Kind: shortplay.JobAssetAudio,
		Status: shortplay.JobRunning, TaskID: taskID,
		PayloadJSON: shortplay.AssetAudioJobPayload{AssetID: assetID, Prompt: "青年女声"}.Encode(),
	}
	if err := svc.repo.CreateShortplayJob(job); err != nil {
		t.Fatal(err)
	}
	return job
}

func setShortplaySettings(t *testing.T, db *gorm.DB, shortplayID, settingsJSON string) {
	t.Helper()
	if err := db.Model(&model.Shortplay{}).Where("id = ?", shortplayID).
		Update("settings_json", settingsJSON).Error; err != nil {
		t.Fatal(err)
	}
}

const audioModelSettings = `{"aspectRatio":"9:16","language":"中文","stylePrompt":"霓虹雨夜","models":{"text":{"logicalModelId":"lm-text"},"audio":{"logicalModelId":"lm-audio"},"image":{"logicalModelId":"lm-image"}}}`

func TestQueueShortplayAssetAudioFiltersAndDedupes(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-audio", "sp-audio")
	seedVoicedAsset(t, svc, "sp-audio", 1, shortplay.AssetCharacter, "沈青禾", "青年女声，克制", "")
	seedVoicedAsset(t, svc, "sp-audio", 2, shortplay.AssetCharacter, "旁白", "", "")
	seedVoicedAsset(t, svc, "sp-audio", 3, shortplay.AssetProp, "油纸伞", "", "")
	locked := seedVoicedAsset(t, svc, "sp-audio", 4, shortplay.AssetCharacter, "顾行舟", "低沉男声", "")
	locked.Locked = true
	if applied, err := svc.repo.UpdateShortplayAsset(locked, 1); err != nil || !applied {
		t.Fatalf("lock = %v, %v", applied, err)
	}

	first, err := svc.QueueShortplayAssetAudio("owner", "sp-audio", shortplay.QueueAssetAudioRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Queued != 1 || first.Skipped != 3 || len(first.JobIDs) != 1 {
		t.Fatalf("first queue = %#v", first)
	}
	second, err := svc.QueueShortplayAssetAudio("owner", "sp-audio", shortplay.QueueAssetAudioRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Queued != 0 || second.Reused != 1 || second.Skipped != 3 {
		t.Fatalf("second queue = %#v", second)
	}
	// 指定单个角色（无音色文本）也走跳过，不整批报错。
	single, err := svc.QueueShortplayAssetAudio("owner", "sp-audio", shortplay.QueueAssetAudioRequest{AssetIDs: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	if single.Queued != 0 || single.Reused != 0 || single.Skipped != 4 {
		t.Fatalf("single asset queue = %#v", single)
	}
}

func TestRunShortplayAssetAudioJobAppliesOnceAndRejectsLateCallback(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-audio-apply", "sp-audio-apply")
	setShortplaySettings(t, db, "sp-audio-apply", audioModelSettings)
	asset := seedVoicedAsset(t, svc, "sp-audio-apply", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", shortplay.ImageRunning)
	job := seedAudioJob(t, svc, "sp-audio-apply", "task-audio-apply", asset.ID)
	seedTask(t, db, "task-audio-apply", string(model.TaskStatusSucceeded), `{"audios":[{"resourceId":"res-audio"}]}`, "")

	run := svc.runShortplayAssetAudioJob(job)
	if run.Status != shortplay.JobSucceeded {
		t.Fatalf("run = %#v", run)
	}
	stored, err := svc.repo.ShortplayAssetByID("sp-audio-apply", asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AudioStatus != shortplay.ImageReady || stored.CurrentAudioID == 0 || stored.AudioVersion != 1 {
		t.Fatalf("asset after apply = %#v", stored)
	}
	if stored.AudioResourceID != "res-audio" {
		t.Fatalf("projection = %q", stored.AudioResourceID)
	}

	// 迟到回调：状态已收口成 ready，CAS 拒绝第二次写入，也不新增音频行。
	if late := svc.runShortplayAssetAudioJob(job); late.Status != shortplay.JobSucceeded {
		t.Fatalf("late run = %#v", late)
	}
	audios, err := svc.repo.ShortplayAssetAudios("sp-audio-apply", asset.ID)
	if err != nil || len(audios) != 1 {
		t.Fatalf("late callback added an audio row: %#v, %v", audios, err)
	}
	stored, _ = svc.repo.ShortplayAssetByID("sp-audio-apply", asset.ID)
	if stored.AudioVersion != 1 {
		t.Fatalf("audio version moved on late callback: %d", stored.AudioVersion)
	}
}

func TestRunShortplayAssetAudioJobFailureKeepsPreviousAudio(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-audio-fail", "sp-audio-fail")
	setShortplaySettings(t, db, "sp-audio-fail", audioModelSettings)
	asset := seedVoicedAsset(t, svc, "sp-audio-fail", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", shortplay.ImageRunning)
	if err := db.Model(&model.ShortplayAsset{}).Where("shortplay_id = ? AND id = ?", "sp-audio-fail", asset.ID).
		Updates(map[string]any{"current_audio_id": 5, "audio_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	job := seedAudioJob(t, svc, "sp-audio-fail", "task-audio-fail", asset.ID)
	seedTask(t, db, "task-audio-fail", string(model.TaskStatusFailed), "", "上游返回 429")

	run := svc.runShortplayAssetAudioJob(job)
	if run.Status != shortplay.JobFailed || run.LastError != "上游返回 429" {
		t.Fatalf("run = %#v", run)
	}
	stored, err := svc.repo.ShortplayAssetByID("sp-audio-fail", asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AudioStatus != shortplay.ImageFailed || stored.CurrentAudioID != 5 || stored.AudioVersion != 2 {
		t.Fatalf("failed redo changed the usable audio: %#v", stored)
	}
	audios, err := svc.repo.ShortplayAssetAudios("sp-audio-fail", asset.ID)
	if err != nil || len(audios) != 1 || audios[0].Status != shortplay.ImageFailed {
		t.Fatalf("failure history = %#v, %v", audios, err)
	}
}

func TestRunShortplayAssetAudioJobRequiresVoiceAndCharacter(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-audio-voice", "sp-audio-voice")
	setShortplaySettings(t, db, "sp-audio-voice", audioModelSettings)
	prop := seedVoicedAsset(t, svc, "sp-audio-voice", 1, shortplay.AssetProp, "油纸伞", "低沉男声", "")
	runProp := svc.runShortplayAssetAudioJob(&model.ShortplayJob{
		ID: "job-prop", ShortplayID: "sp-audio-voice", Kind: shortplay.JobAssetAudio,
		Status: shortplay.JobRunning, PayloadJSON: shortplay.AssetAudioJobPayload{AssetID: prop.ID}.Encode(),
	})
	if runProp.Status != shortplay.JobFailed || runProp.LastError == "" {
		t.Fatalf("prop run = %#v", runProp)
	}
	silent := seedVoicedAsset(t, svc, "sp-audio-voice", 2, shortplay.AssetCharacter, "旁白", "", "")
	runSilent := svc.runShortplayAssetAudioJob(&model.ShortplayJob{
		ID: "job-silent", ShortplayID: "sp-audio-voice", Kind: shortplay.JobAssetAudio,
		Status: shortplay.JobRunning, PayloadJSON: shortplay.AssetAudioJobPayload{AssetID: silent.ID}.Encode(),
	})
	if runSilent.Status != shortplay.JobFailed || runSilent.LastError == "" {
		t.Fatalf("silent run = %#v", runSilent)
	}
}

func TestRunShortplayAssetAudioJobFailsWithoutAudioModel(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-audio-nomodel", "sp-audio-nomodel")
	asset := seedVoicedAsset(t, svc, "sp-audio-nomodel", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")
	run := svc.runShortplayAssetAudioJob(&model.ShortplayJob{
		ID: "job-nomodel", ShortplayID: "sp-audio-nomodel", Kind: shortplay.JobAssetAudio,
		Status: shortplay.JobRunning, PayloadJSON: shortplay.AssetAudioJobPayload{AssetID: asset.ID}.Encode(),
	})
	if run.Status != shortplay.JobFailed || !strings.Contains(run.LastError, "音频模型") {
		t.Fatalf("run = %#v", run)
	}
	var jobs int64
	if err := db.Model(&model.ShortplayJob{}).Where("kind = ?", shortplay.JobAssetAudio).Count(&jobs).Error; err != nil || jobs != 0 {
		t.Fatalf("no audio model must not submit a task: jobs=%d err=%v", jobs, err)
	}
}

// TestTickShortplayAssetAudioCompletesSubmittedJob 覆盖真实调度两轮：
// 第一轮领取并提交（job 变 running + taskId），任务成功后再跑一轮收口成 succeeded。
func TestTickShortplayAssetAudioCompletesSubmittedJob(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-audio-tick", "sp-audio-tick")
	setShortplaySettings(t, db, "sp-audio-tick", audioModelSettings)
	asset := seedVoicedAsset(t, svc, "sp-audio-tick", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")

	var submitted CreateTaskRequest
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submitted = req
		task := &model.Task{ID: "task-audio-tick", UserID: userID, ProjectID: req.ProjectID,
			Type: req.Type, Operation: req.Operation, Prompt: req.Prompt, Status: model.TaskStatusRunning}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		return task, nil
	})
	job := &model.ShortplayJob{
		ID: "job-audio-tick", ShortplayID: "sp-audio-tick", Kind: shortplay.JobAssetAudio,
		Status: shortplay.JobQueued, DedupeKey: shortplay.JobDedupeKey(shortplay.JobAssetAudio, "sp-audio-tick|1"),
		PayloadJSON: shortplay.AssetAudioJobPayload{AssetID: asset.ID}.Encode(),
	}
	if err := svc.repo.CreateShortplayJob(job); err != nil {
		t.Fatal(err)
	}
	svc.tickShortplayAssetAudio(t.Context())
	if submitted.Type != "canvas_audio" || submitted.Operation != shortplay.AudioOperation {
		t.Fatalf("submitted task = %#v", submitted)
	}
	if submitted.LogicalModelID != "lm-audio" || submitted.Prompt != "青年女声" {
		t.Fatalf("submitted task = %#v", submitted)
	}
	running := loadShortplayJob(t, db, "job-audio-tick")
	if running.TaskID != "task-audio-tick" || running.Status != shortplay.JobRunning {
		t.Fatalf("job after submit = %#v", running)
	}

	if err := db.Model(&model.Task{}).Where("id = ?", "task-audio-tick").
		Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"audios":[{"resourceId":"res-tick"}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	svc.tickShortplayAssetAudio(t.Context())
	finished := loadShortplayJob(t, db, "job-audio-tick")
	if finished.Status != shortplay.JobSucceeded {
		t.Fatalf("job after apply = %#v", finished)
	}
	stored, err := svc.repo.ShortplayAssetByID("sp-audio-tick", asset.ID)
	if err != nil || stored.AudioStatus != shortplay.ImageReady || stored.CurrentAudioID == 0 {
		t.Fatalf("asset after apply = %#v, %v", stored, err)
	}
}

// TestTickShortplayAssetImagesCompletesSubmittedJob 守住出图同样的两轮推进。
func TestTickShortplayAssetImagesCompletesSubmittedJob(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-image-tick", "sp-image-tick")
	setShortplaySettings(t, db, "sp-image-tick", audioModelSettings)
	asset := seedAsset(t, svc, "sp-image-tick", 1, shortplay.AssetCharacter, "沈青禾", "")

	var submitted CreateTaskRequest
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submitted = req
		task := &model.Task{ID: "task-image-tick", UserID: userID, ProjectID: req.ProjectID,
			Type: req.Type, Operation: req.Operation, Prompt: req.Prompt, Status: model.TaskStatusRunning}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		return task, nil
	})
	job := &model.ShortplayJob{
		ID: "job-image-tick", ShortplayID: "sp-image-tick", Kind: shortplay.JobAssetImage,
		Status: shortplay.JobQueued, DedupeKey: shortplay.JobDedupeKey(shortplay.JobAssetImage, "sp-image-tick|1"),
		PayloadJSON: shortplay.AssetImageJobPayload{AssetID: asset.ID}.Encode(),
	}
	if err := svc.repo.CreateShortplayJob(job); err != nil {
		t.Fatal(err)
	}
	svc.tickShortplayAssetImages(t.Context())
	if submitted.Type != "canvas_image" {
		t.Fatalf("submitted task = %#v", submitted)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", "task-image-tick").
		Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"images":[{"resourceId":"res-image-tick"}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	svc.tickShortplayAssetImages(t.Context())
	finished := loadShortplayJob(t, db, "job-image-tick")
	if finished.Status != shortplay.JobSucceeded {
		t.Fatalf("job after apply = %#v", finished)
	}
	stored, err := svc.repo.ShortplayAssetByID("sp-image-tick", asset.ID)
	if err != nil || stored.ImageStatus != shortplay.ImageReady || stored.CurrentImageID == 0 {
		t.Fatalf("asset after apply = %#v, %v", stored, err)
	}
}

// loadShortplayJob 按 id 读一条 job；结束后的行不再命中「未结束」查询。
func loadShortplayJob(t *testing.T, db *gorm.DB, id string) *model.ShortplayJob {
	t.Helper()
	var row model.ShortplayJob
	if err := db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return &row
}
