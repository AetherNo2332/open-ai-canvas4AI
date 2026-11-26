package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newShortplayWorkflowService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.Project{}, &model.Shortplay{}, &model.ShortplayEpisode{},
		&model.ShortplayAsset{}, &model.ShortplayAssetImage{}, &model.ShortplayAssetAudio{},
		&model.ShortplayJob{}, &model.CanvasProject{}, &model.CanvasEditLease{}, &model.Task{}, &model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}

// seedShortplay 建一个两集作品；第二集提到道具，验证跨集绑定。
func seedShortplay(t *testing.T, db *gorm.DB, owner, projectID, shortplayID string) {
	t.Helper()
	project := &model.Project{ID: projectID, UserID: owner, Name: "雨夜回眸 " + projectID, Type: "shortplay", AspectRatio: "9:16"}
	if err := db.Create(project).Error; err != nil {
		t.Fatal(err)
	}
	row := &model.Shortplay{ID: shortplayID, ProjectID: projectID, UserID: owner,
		SettingsJSON: `{"aspectRatio":"9:16","language":"中文","stylePrompt":"霓虹雨夜","models":{"text":{"logicalModelId":"lm-text"}}}`}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	episodes := []model.ShortplayEpisode{
		{ID: shortplayID + "-ep1", ShortplayID: shortplayID, Number: 1, Title: "第一集", CanvasID: shortplayID + "-c1", ScriptText: "沈青禾撑伞站在巷口。"},
		{ID: shortplayID + "-ep2", ShortplayID: shortplayID, Number: 2, Title: "第二集", CanvasID: shortplayID + "-c2", ScriptText: "沈青禾接过那把油纸伞。"},
	}
	for index := range episodes {
		if err := db.Create(&episodes[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func extractReply() string {
	return `{"characters":[{"name":"沈青禾","aliases":["青禾"],"description":"二十岁女性","appearance":"齐肩黑发","voice":"青年女声"}],` +
		`"props":[{"name":"油纸伞","description":"定情信物","appearance":"深青油纸"}],"scenes":[]}`
}

// useShortplayTextTask 注入任务提交桩：记录提交出去的请求，并把一条 running 任务写库，
// 与生产路径的 CreateTask → 任务行 → 读结果同形（不绕过任务合同）。
func useShortplayTextTask(t *testing.T, svc *Service, db *gorm.DB, taskID string) {
	t.Helper()
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		input, _ := json.Marshal(req.Input)
		task := &model.Task{
			ID: taskID, UserID: userID, ProjectID: req.ProjectID, Type: req.Type,
			Operation: req.Operation, Prompt: req.Prompt, InputJSON: string(input),
			Status: model.TaskStatusRunning,
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		return task, nil
	})
}

func completeShortplayTextTask(t *testing.T, db *gorm.DB, taskID, status, result, failure string) {
	t.Helper()
	if status == string(model.TaskStatusSucceeded) && result == "" {
		result = `{"text":` + strconv.Quote(extractReply()) + `}`
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).
		Updates(map[string]any{"status": model.TaskStatus(status), "result_json": result, "error": failure}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestQueueShortplayExtractDeduplicatesActiveJob(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-queue", "sp-queue")

	first, err := svc.QueueShortplayExtract("owner", "sp-queue")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.QueueShortplayExtract("owner", "p-queue")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Queued || second.Queued || first.JobID == "" || first.JobID != second.JobID {
		t.Fatalf("queue result = %#v / %#v", first, second)
	}
	var count int64
	if err := db.Model(&model.ShortplayJob{}).Where("shortplay_id = ?", "sp-queue").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("jobs = %d, err = %v", count, err)
	}
	if _, err := svc.QueueShortplayExtract("intruder", "sp-queue"); err == nil {
		t.Fatal("other user queued extraction on someone else's shortplay")
	}
}

func TestRunShortplayExtractBindsAssetsToBothEpisodes(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-run", "sp-run")
	if _, err := svc.QueueShortplayExtract("owner", "sp-run"); err != nil {
		t.Fatal(err)
	}
	useShortplayTextTask(t, svc, db, "task-run")
	jobID, err := svc.RunShortplayExtractOnce(context.Background())
	if err != nil || jobID == "" {
		t.Fatalf("submit = %q, %v", jobID, err)
	}
	var submitted model.Task
	if err := db.First(&submitted, "id = ?", "task-run").Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(submitted.Prompt, "## 第 1 集") || !strings.Contains(submitted.Prompt, "沈青禾撑伞") {
		t.Fatalf("prompt 缺少批次正文：%s", submitted.Prompt)
	}
	var input struct {
		Config struct {
			SystemPrompt string `json:"systemPrompt"`
		} `json:"config"`
		Mode        string         `json:"mode"`
		TextOptions map[string]any `json:"textOptions"`
	}
	if err := json.Unmarshal([]byte(submitted.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(input.Config.SystemPrompt, "资产提取") || input.Mode != "text" || input.TextOptions["thinkingLevel"] != ThinkingLevelHigh {
		t.Fatalf("task input = %s", submitted.InputJSON)
	}
	var running model.ShortplayJob
	if err := db.First(&running, "id = ?", jobID).Error; err != nil {
		t.Fatal(err)
	}
	if running.Status != shortplay.JobRunning || running.TaskID != "task-run" {
		t.Fatalf("job after submit = %#v", running)
	}

	completeShortplayTextTask(t, db, "task-run", string(model.TaskStatusSucceeded), "", "")
	if _, err := svc.RunShortplayExtractOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assets, err := svc.ShortplayAssets("owner", "sp-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 {
		t.Fatalf("assets = %#v", assets)
	}
	byName := map[string]shortplay.AssetSummary{}
	for _, asset := range assets {
		byName[asset.Name] = asset
	}
	character, ok := byName["沈青禾"]
	if !ok || len(character.EpisodeNumbers) != 2 || character.EpisodeNumbers[0] != 1 || character.EpisodeNumbers[1] != 2 {
		t.Fatalf("character = %#v", character)
	}
	prop, ok := byName["油纸伞"]
	if !ok || len(prop.EpisodeNumbers) != 1 || prop.EpisodeNumbers[0] != 2 {
		t.Fatalf("prop = %#v", prop)
	}
	if len(character.Aliases) != 1 || character.Aliases[0] != "青禾" || character.Voice != "青年女声" {
		t.Fatalf("character fields = %#v", character)
	}
	var episode1, episode2 model.ShortplayEpisode
	if err := db.First(&episode1, "id = ?", "sp-run-ep1").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&episode2, "id = ?", "sp-run-ep2").Error; err != nil {
		t.Fatal(err)
	}
	if episode1.AssetIDsJSON != "[1]" || episode2.AssetIDsJSON != "[1,2]" {
		t.Fatalf("episode bindings = %s / %s", episode1.AssetIDsJSON, episode2.AssetIDsJSON)
	}
	var job model.ShortplayJob
	if err := db.First(&job, "id = ?", jobID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobSucceeded || job.Attempt != 1 {
		t.Fatalf("job = %#v", job)
	}
}

func TestRunShortplayExtractFailureIsRecorded(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-fail", "sp-fail")
	if _, err := svc.QueueShortplayExtract("owner", "sp-fail"); err != nil {
		t.Fatal(err)
	}
	useShortplayTextTask(t, svc, db, "task-fail")
	jobID, err := svc.RunShortplayExtractOnce(context.Background())
	if err != nil || jobID == "" {
		t.Fatalf("submit = %q, %v", jobID, err)
	}
	completeShortplayTextTask(t, db, "task-fail", string(model.TaskStatusFailed), "", "上游模型超时")
	// 业务失败写进 job.LastError，调用方通过 job 行判断结果，不需要错误返回值。
	if _, err := svc.RunShortplayExtractOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var job model.ShortplayJob
	if err := db.First(&job, "id = ?", jobID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobFailed || !strings.Contains(job.LastError, "上游模型超时") {
		t.Fatalf("job = %#v", job)
	}
	// 失败后同一作品可以再次入队（去重键只覆盖未结束行）。
	next, err := svc.QueueShortplayExtract("owner", "sp-fail")
	if err != nil || !next.Queued || next.JobID == jobID {
		t.Fatalf("retry queue = %#v, %v", next, err)
	}
}

func TestRunShortplayExtractFailsWithoutTextModel(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-nomodel", "sp-nomodel")
	// 清掉设置里的模型选择，且目录里没有可用文本模型：必须明确失败而不是静默跳过。
	if err := db.Model(&model.Shortplay{}).Where("id = ?", "sp-nomodel").
		Update("settings_json", `{"aspectRatio":"9:16"}`).Error; err != nil {
		t.Fatal(err)
	}
	useShortplayTextTask(t, svc, db, "task-should-not-exist")
	if _, err := svc.QueueShortplayExtract("owner", "sp-nomodel"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunShortplayExtractOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var job model.ShortplayJob
	if err := db.Where("shortplay_id = ?", "sp-nomodel").First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobFailed || !strings.Contains(job.LastError, "文本模型") {
		t.Fatalf("job = %#v", job)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Where("id = ?", "task-should-not-exist").Count(&tasks).Error; err != nil || tasks != 0 {
		t.Fatalf("unexpected task submitted: %d, %v", tasks, err)
	}
}

func TestUpsertShortplayAssetRejectsCrossProjectWrite(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-a", "sp-a")
	seedShortplay(t, db, "owner", "p-b", "sp-b")
	created, err := svc.UpsertShortplayAsset("owner", "sp-a", shortplay.UpsertAssetRequest{Kind: "character", Name: "沈青禾", Description: "A 作品"})
	if err != nil || created.ID != 1 {
		t.Fatalf("create = %#v, %v", created, err)
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-b", shortplay.UpsertAssetRequest{AssetID: created.ID, Kind: "character", Name: "沈青禾"}); err == nil {
		t.Fatal("cross-project asset update was accepted")
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-b", shortplay.UpsertAssetRequest{Kind: "wrong", Name: "x"}); err == nil {
		t.Fatal("invalid kind was accepted")
	}
	listA, err := svc.ShortplayAssets("owner", "sp-a")
	if err != nil || len(listA) != 1 || listA[0].Name != "沈青禾" {
		t.Fatalf("list A = %#v, %v", listA, err)
	}
	listB, err := svc.ShortplayAssets("owner", "sp-b")
	if err != nil || len(listB) != 0 {
		t.Fatalf("list B = %#v, %v", listB, err)
	}
}

func TestExtractKeepsLockedSettingsButMergesAliases(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-lock", "sp-lock")
	locked, err := svc.UpsertShortplayAsset("owner", "sp-lock", shortplay.UpsertAssetRequest{
		Kind: "character", Name: "沈青禾", Description: "用户确认的设定", Locked: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.QueueShortplayExtract("owner", "sp-lock"); err != nil {
		t.Fatal(err)
	}
	useShortplayTextTask(t, svc, db, "task-lock")
	if _, err := svc.RunShortplayExtractOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	completeShortplayTextTask(t, db, "task-lock", string(model.TaskStatusSucceeded), "", "")
	if _, err := svc.RunShortplayExtractOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assets, err := svc.ShortplayAssets("owner", "sp-lock")
	if err != nil {
		t.Fatal(err)
	}
	var character shortplay.AssetSummary
	for _, asset := range assets {
		if asset.Name == "沈青禾" {
			character = asset
		}
	}
	if character.ID != locked.ID || character.Description != "用户确认的设定" {
		t.Fatalf("locked row overwritten: %#v", character)
	}
	if len(character.Aliases) != 1 || character.Aliases[0] != "青禾" {
		t.Fatalf("aliases not merged: %#v", character.Aliases)
	}
	if len(character.EpisodeNumbers) != 2 {
		t.Fatalf("episode binding missing: %#v", character.EpisodeNumbers)
	}
}

func TestShortplayExtractSchedulerTickDrainsQueuedJobs(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-tick", "sp-tick")
	queued, err := svc.QueueShortplayExtract("owner", "sp-tick")
	if err != nil || !queued.Queued {
		t.Fatalf("queue = %#v, %v", queued, err)
	}
	useShortplayTextTask(t, svc, db, "task-tick")
	svc.tickShortplayExtract(context.Background())
	var submitted model.ShortplayJob
	if err := db.Where("id = ?", queued.JobID).First(&submitted).Error; err != nil {
		t.Fatal(err)
	}
	if submitted.Status != shortplay.JobRunning || submitted.TaskID != "task-tick" {
		t.Fatalf("job after tick = %#v", submitted)
	}
	completeShortplayTextTask(t, db, "task-tick", string(model.TaskStatusSucceeded), "", "")
	svc.tickShortplayExtract(context.Background())
	var job model.ShortplayJob
	if err := db.Where("id = ?", queued.JobID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobSucceeded {
		t.Fatalf("job status = %s lastError = %s", job.Status, job.LastError)
	}
	var episodes []model.ShortplayEpisode
	if err := db.Where("shortplay_id = ?", "sp-tick").Order("number asc").Find(&episodes).Error; err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 2 || !strings.Contains(episodes[1].AssetIDsJSON, "[1") {
		t.Fatalf("episode bindings = %#v", episodes)
	}
	// 第三次 tick 没有待推进 job，不应重复执行或报错。
	svc.tickShortplayExtract(context.Background())
	var again model.ShortplayJob
	if err := db.Where("id = ?", queued.JobID).First(&again).Error; err != nil {
		t.Fatal(err)
	}
	if again.Status != shortplay.JobSucceeded {
		t.Fatalf("job changed after empty tick: %#v", again)
	}
}
