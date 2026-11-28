package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

func TestW09StaleRecoveryCannotOverwriteReplacementAsset(t *testing.T) {
	for _, kind := range []string{shortplay.JobAssetImage, shortplay.JobAssetAudio} {
		t.Run(kind, func(t *testing.T) {
			svc, db := newShortplayWorkflowService(t)
			seedShortplay(t, db, "owner", "sp-replacement", "sp-replacement")
			seedVoicedAsset(t, svc, "sp-replacement", 1, shortplay.AssetCharacter, "角色", "青年女声", "")
			job := model.ShortplayJob{ID: "old-job", ShortplayID: "sp-replacement", Kind: kind, Status: shortplay.JobFailed, DedupeKey: "asset-key", PayloadJSON: shortplay.AssetImageJobPayload{AssetID: 1}.Encode()}
			if err := db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&job).Update("updated_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			replacement := job
			replacement.ID = "replacement"
			replacement.Status = shortplay.JobRunning
			replacement.TaskID = "replacement-task"
			if err := db.Create(&replacement).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.ShortplayAsset{}).Where("shortplay_id = ? AND id = ?", job.ShortplayID, 1).Updates(map[string]any{"image_status": shortplay.ImageRunning, "audio_status": shortplay.ImageRunning}).Error; err != nil {
				t.Fatal(err)
			}
			job.Status = shortplay.JobRunning
			if err := svc.failOrphanedShortplayJob(&job, "stale recovery"); err != nil {
				t.Fatal(err)
			}
			asset, err := svc.repo.ShortplayAssetByID(job.ShortplayID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if asset.ImageStatus != shortplay.ImageRunning || asset.AudioStatus != shortplay.ImageRunning {
				t.Fatalf("replacement overwritten: %+v", asset)
			}
		})
	}
}

func TestW09MediaCommitThenCompletionDoesNotRegress(t *testing.T) {
	for _, kind := range []string{shortplay.JobAssetImage, shortplay.JobAssetAudio} {
		t.Run(kind, func(t *testing.T) {
			svc, db := newShortplayWorkflowService(t)
			seedShortplay(t, db, "owner", "sp-late-running", "sp-late-running")
			setShortplaySettings(t, db, "sp-late-running", audioModelSettings)
			seedVoicedAsset(t, svc, "sp-late-running", 1, shortplay.AssetCharacter, "角色", "青年女声", "")
			job := model.ShortplayJob{ID: "late-running", ShortplayID: "sp-late-running", Kind: kind, Status: shortplay.JobRunning, DedupeKey: "late-running", PayloadJSON: shortplay.AssetImageJobPayload{AssetID: 1}.Encode()}
			if err := db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
				task := &model.Task{ID: "late-running-task", UserID: userID, ProjectID: req.ProjectID, Type: req.Type, Status: model.TaskStatusSucceeded}
				err := svc.repo.Transaction(func(tx *repository.Repository) error { return req.afterTaskCreate(tx, task) })
				if err != nil {
					return nil, err
				}
				// Pause the submitting call after binding commits; another worker has settled the asset.
				if kind == shortplay.JobAssetImage {
					err = svc.repo.UpdateShortplayAssetImageStatus(job.ShortplayID, 1, shortplay.ImageReady)
				} else {
					err = svc.repo.UpdateShortplayAssetAudioStatus(job.ShortplayID, 1, shortplay.ImageReady)
				}
				if err != nil {
					return nil, err
				}
				if err = svc.repo.FinishShortplayJob(job.ID, shortplay.JobSucceeded, "", job.PayloadJSON); err != nil {
					return nil, err
				}
				return task, nil
			})
			var run shortplayImageJobRun
			if kind == shortplay.JobAssetImage {
				run = svc.runShortplayAssetImageJob(context.Background(), &job)
			} else {
				run = svc.runShortplayAssetAudioJob(&job)
			}
			if run.LastError != "" {
				t.Fatal(run.LastError)
			}
			asset, err := svc.repo.ShortplayAssetByID(job.ShortplayID, 1)
			if err != nil {
				t.Fatal(err)
			}
			status := asset.ImageStatus
			if kind == shortplay.JobAssetAudio {
				status = asset.AudioStatus
			}
			if status != shortplay.ImageReady {
				t.Fatalf("terminal asset regressed to %s", status)
			}
		})
	}
}

func TestW09MediaOrphanFailureProjectsAsset(t *testing.T) {
	for _, kind := range []string{shortplay.JobAssetImage, shortplay.JobAssetAudio} {
		for _, candidates := range []int{0, 2} {
			t.Run(kind+strconv.Itoa(candidates), func(t *testing.T) {
				svc, db := newShortplayWorkflowService(t)
				seedShortplay(t, db, "owner", "sp-media-orphan", "sp-media-orphan")
				seedVoicedAsset(t, svc, "sp-media-orphan", 1, shortplay.AssetCharacter, "角色", "青年女声", "")
				if err := db.Model(&model.ShortplayAsset{}).Where("shortplay_id = ? AND id = ?", "sp-media-orphan", 1).Updates(map[string]any{"image_status": shortplay.ImageRunning, "audio_status": shortplay.ImageRunning}).Error; err != nil {
					t.Fatal(err)
				}
				job := model.ShortplayJob{ID: "media-orphan", ShortplayID: "sp-media-orphan", Kind: kind, Status: shortplay.JobRunning, DedupeKey: "media-orphan", PayloadJSON: shortplay.AssetImageJobPayload{AssetID: 1}.Encode()}
				if err := db.Create(&job).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&job).Update("updated_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
				for i := 0; i < candidates; i++ {
					if err := db.Create(&model.Task{ID: "candidate-" + strconv.Itoa(i), UserID: "owner", ProjectID: "sp-media-orphan", Type: shortplayJobTaskType(kind), Status: model.TaskStatusRunning, InputJSON: `{"metadata":{"shortplayJobId":"media-orphan"}}`}).Error; err != nil {
						t.Fatal(err)
					}
				}
				if _, err := svc.recoverShortplayJobTask(&job); err != nil {
					t.Fatal(err)
				}
				asset, err := svc.repo.ShortplayAssetByID(job.ShortplayID, 1)
				if err != nil {
					t.Fatal(err)
				}
				status := asset.ImageStatus
				if kind == shortplay.JobAssetAudio {
					status = asset.AudioStatus
				}
				if job.Status != shortplay.JobFailed || status != shortplay.ImageFailed {
					t.Fatalf("job=%s asset=%s", job.Status, status)
				}
			})
		}
	}
}

func TestW09RecoveryOwnerQueryFailurePreservesJob(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-query-failure", "sp-query-failure")
	job := model.ShortplayJob{ID: "query-failure", ShortplayID: "sp-query-failure", Kind: shortplay.JobExtract, Status: shortplay.JobRunning, DedupeKey: "query-failure"}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&job).Update("updated_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected owner query unavailable")
	if err := db.Callback().Query().Before("gorm:query").Register("w09_owner_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "shortplays" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove("w09_owner_failure")
	_, err := svc.recoverShortplayJobTask(&job)
	if !errors.Is(err, injected) {
		t.Errorf("owner query failure swallowed: %v", err)
	}
	if err := db.First(&job, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobRunning {
		t.Fatalf("query failure terminalized job: %s", job.Status)
	}
}

func TestW09DirectExtractDrainDoesNotClaim(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-direct-drain", "sp-direct-drain")
	queued, err := svc.QueueShortplayExtract("owner", "sp-direct-drain")
	if err != nil {
		t.Fatal(err)
	}
	svc.BeginDrain()
	if _, err := svc.RunShortplayExtractOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var job model.ShortplayJob
	if err := db.First(&job, "id = ?", queued.JobID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Attempt != 0 || job.Status != shortplay.JobQueued {
		t.Fatalf("drain claimed job: status=%s attempt=%d", job.Status, job.Attempt)
	}
}

func TestW09ExtractOrphanNotStarvedByRunningTask(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-starved", "sp-starved")
	active := model.ShortplayJob{ID: "active-starver", ShortplayID: "sp-starved", Kind: shortplay.JobExtract, Status: shortplay.JobRunning, TaskID: "active-task", DedupeKey: "active-starver"}
	orphan := model.ShortplayJob{ID: "starved-orphan", ShortplayID: "sp-starved", Kind: shortplay.JobExtract, Status: shortplay.JobRunning, DedupeKey: "starved-orphan"}
	for _, job := range []*model.ShortplayJob{&active, &orphan} {
		if err := db.Create(job).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.Task{ID: "active-task", UserID: "owner", ProjectID: "sp-starved", Type: "canvas_text", Status: model.TaskStatusRunning}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orphan).Update("updated_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunShortplayExtractOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&orphan, "id = ?", orphan.ID).Error; err != nil {
		t.Fatal(err)
	}
	if orphan.Status != shortplay.JobFailed {
		t.Fatalf("orphan starved: %s", orphan.Status)
	}
}

// W09 排空与恢复语义的合同测试。场景各自独立断言，不互相替代：
//
//	A. drain 之后新 tick 不领取（现行实现有守卫，本文件补证据）。
//	B. tick 已进入、worker ctx 已被取消后不得再开始下一次派发（现行实现只为红）。
//	C. 「任务已建、写回 taskId 之前中断」留下的 running + 空 taskId 行必须有归宿且不得自动重发
//	   （修前为红）；故障点用真实代码路径注入，不是手写库状态。
//	D. 重启后读回已提交任务并收口（现行实现支持，本文件补证据，不替代 A/B）。
//	E. 任务状态 submission_uncertain（上游结果不明）时按合同收口为失败/待核对，不重发。
//	F. 并发找回必须只有一个认领者，不得写出两个不同 taskId。
//
// 关停合同（backend/cmd/server/main.go:35/148/153、platform/worker_runtime.go:64）：
// BeginDrain 置标记并立即 cancel worker ctx；合同只要求「停止新领取 + 等在跑协程返回」，
// 不承诺「drain 期间把在途任务收口」（docs/content/docs/backend/task-runtime-stability.mdx:135）。
// 所以 A/B/C/D 分开测，不用 D 替代该验收；D 也不被解读为 drain 内收口的承诺。

// TestW09DrainStopsNewTicks 预置 queued job，BeginDrain 后三类 tick 的代表路径不得新增提交，
// 对照组不 drain 时会提交。
func TestW09DrainStopsNewTicks(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-w09-drain", "sp-w09-drain")
	setShortplaySettings(t, db, "sp-w09-drain", audioModelSettings)
	seedVoicedAsset(t, svc, "sp-w09-drain", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")

	submits := 0
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submits++
		return nil, nil
	})
	extract, err := svc.QueueShortplayExtract("owner", "sp-w09-drain")
	if err != nil || !extract.Queued {
		t.Fatalf("queue extract = %#v, %v", extract, err)
	}
	audio, err := svc.QueueShortplayAssetAudio("owner", "sp-w09-drain", shortplay.QueueAssetAudioRequest{})
	if err != nil || audio.Queued != 1 {
		t.Fatalf("queue audio = %#v, %v", audio, err)
	}

	svc.BeginDrain()
	svc.tickShortplayExtract(context.Background())
	svc.tickShortplayAssetAudio(context.Background())

	if submits != 0 {
		t.Fatalf("drain 后仍派发了 %d 条任务", submits)
	}
	for _, jobID := range []string{extract.JobID, audio.JobIDs[0]} {
		var job model.ShortplayJob
		if err := db.Where("id = ?", jobID).First(&job).Error; err != nil {
			t.Fatal(err)
		}
		if job.Status != shortplay.JobQueued || job.TaskID != "" {
			t.Fatalf("drain 后 job %s 被推进：status=%s taskId=%q", jobID, job.Status, job.TaskID)
		}
	}
}

// TestW09CancelledWorkerContextStopsDispatch 注入「tick 已进入、worker ctx 已被取消」：
// BeginDrain 会 cancel worker ctx，已进入的 tick 必须在下次领取前观察到取消，不得再领取或派发。
// 三类 tick 各测一遍；现状（修复前）为红：三个 tick 只检查 s.IsDraining()，从不读 ctx。
func TestW09CancelledWorkerContextStopsDispatch(t *testing.T) {
	cases := []struct {
		name  string
		queue func(t *testing.T, svc *Service) []string
		tick  func(svc *Service, ctx context.Context)
	}{
		{
			name: "extract",
			queue: func(t *testing.T, svc *Service) []string {
				res, err := svc.QueueShortplayExtract("owner", "sp-w09-ctx")
				if err != nil || !res.Queued {
					t.Fatalf("queue extract = %#v, %v", res, err)
				}
				return []string{res.JobID}
			},
			tick: func(svc *Service, ctx context.Context) { svc.tickShortplayExtract(ctx) },
		},
		{
			name: "image",
			queue: func(t *testing.T, svc *Service) []string {
				res, err := svc.QueueShortplayAssetImages("owner", "sp-w09-ctx", shortplay.QueueAssetImagesRequest{})
				if err != nil || res.Queued != 1 {
					t.Fatalf("queue image = %#v, %v", res, err)
				}
				return res.JobIDs
			},
			tick: func(svc *Service, ctx context.Context) { svc.tickShortplayAssetImages(ctx) },
		},
		{
			name: "audio",
			queue: func(t *testing.T, svc *Service) []string {
				res, err := svc.QueueShortplayAssetAudio("owner", "sp-w09-ctx", shortplay.QueueAssetAudioRequest{})
				if err != nil || res.Queued != 1 {
					t.Fatalf("queue audio = %#v, %v", res, err)
				}
				return res.JobIDs
			},
			tick: func(svc *Service, ctx context.Context) { svc.tickShortplayAssetAudio(ctx) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := newShortplayWorkflowService(t)
			seedShortplay(t, db, "owner", "sp-w09-ctx", "sp-w09-ctx")
			setShortplaySettings(t, db, "sp-w09-ctx", audioModelSettings)
			seedVoicedAsset(t, svc, "sp-w09-ctx", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")

			submits := 0
			svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
				submits++
				return &model.Task{
					ID: "task-w09-ctx", UserID: userID, ProjectID: req.ProjectID,
					Type: req.Type, Operation: req.Operation, Status: model.TaskStatusRunning,
				}, nil
			})
			jobIDs := tc.queue(t, svc)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			tc.tick(svc, ctx)

			if submits != 0 {
				t.Fatalf("worker ctx 已取消，已进入的 tick 仍派发了 %d 条任务（应在领取前停止）", submits)
			}
			for _, jobID := range jobIDs {
				var job model.ShortplayJob
				if err := db.Where("id = ?", jobID).First(&job).Error; err != nil {
					t.Fatal(err)
				}
				// attempt/startedAt 一起断言：即使后续又把行退回 queued，领取动作本身也必须没发生。
				if job.Status != shortplay.JobQueued || job.TaskID != "" || job.Attempt != 0 || job.StartedAt != nil {
					t.Fatalf("取消后 job 被领取/推进：status=%s taskId=%q attempt=%d startedAt=%v",
						job.Status, job.TaskID, job.Attempt, job.StartedAt)
				}
			}
		})
	}
}

// TestW09OrphanProducedByInterruptedWriteback 用真实代码路径注入故障：任务行已建立
// （模拟 CreateTask 已提交），随后在 advanceShortplayExtract 写回 taskId 之前中断
// （shortplay_extract_worker.go:197 提交 → :131 写回之间的窗口）。
//
// 断言分两段：(1) 注入确实落在预期窗口，(2) 该孤儿行必须有归宿且不得自动重发。
// 第 (2) 段现状为红。本测试不断言是否已扣费：注入桩不写订单，扣费要靠真实账单观测。
func TestW09OrphanProducedByInterruptedWriteback(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-w09-orphan", "sp-w09-orphan")
	setShortplaySettings(t, db, "sp-w09-orphan", audioModelSettings)

	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		input, err := json.Marshal(req.Input)
		if err != nil {
			t.Fatal(err)
		}
		task := &model.Task{
			ID: "task-w09-orphan", UserID: userID, ProjectID: req.ProjectID,
			Type: req.Type, Operation: req.Operation, Prompt: req.Prompt,
			InputJSON:  string(input),
			Status:     model.TaskStatusSucceeded,
			ResultJSON: `{"text":` + strconv.Quote(extractReply()) + `}`,
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		panic(w09InjectedCrash{})
	})
	queued, err := svc.QueueShortplayExtract("owner", "sp-w09-orphan")
	if err != nil || !queued.Queued {
		t.Fatalf("queue = %#v, %v", queued, err)
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("预期在写回 taskId 之前中断，tick 却正常返回")
			} else if _, ok := r.(w09InjectedCrash); !ok {
				t.Fatalf("非注入错误 panic: %v", r)
			}
		}()
		svc.tickShortplayExtract(context.Background())
	}()

	var job model.ShortplayJob
	if err := db.Where("id = ?", queued.JobID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobRunning || job.TaskID != "" {
		t.Fatalf("注入未落在预期窗口：status=%s taskId=%q", job.Status, job.TaskID)
	}
	var tasks []model.Task
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || !strings.Contains(tasks[0].InputJSON, `"shortplayJobId":"`+job.ID+`"`) {
		t.Fatalf("任务行与 job 的关联不符：tasks=%d input=%s", len(tasks), tasks[0].InputJSON)
	}

	// 模拟进程重启后的调度：不得重发，orphan 必须有归宿。
	submits := 0
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submits++
		return nil, nil
	})
	// 重启发生在静默期之后：把最后更新时间推到 10 分钟前，与真实「崩溃 → 重启」同形。
	if err := db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), queued.JobID).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		svc.tickShortplayExtract(context.Background())
	}
	if err := db.Where("id = ?", queued.JobID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status == shortplay.JobRunning && job.TaskID == "" {
		t.Fatalf("running + 空 taskId 的 job 三轮 tick 后仍无归宿（status=%s taskId=%q lastError=%q）",
			job.Status, job.TaskID, job.LastError)
	}
	// 优先找回：任务行存在时必须复用那条任务的结果，而不是判失败。
	if job.Status != shortplay.JobSucceeded || job.TaskID != "task-w09-orphan" {
		t.Fatalf("没有复用已有任务：status=%s taskId=%q lastError=%q", job.Status, job.TaskID, job.LastError)
	}
	if submits != 0 {
		t.Fatalf("孤儿 job 被自动重发 %d 次", submits)
	}
	var taskCount int64
	if err := db.Model(&model.Task{}).Count(&taskCount).Error; err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 {
		t.Fatalf("任务数变成 %d，出现重复提交", taskCount)
	}
}

// TestW09InFlightSubmitCompletesAfterDrain 对应验收口径：drain 发生在提交已经开始之后时，
// 已进入的那一次提交按正常请求完成并记录 taskId，此后不得再开始下一次派发。
// 这不是缺口修复的驱动用例——修复前后都应绿，用来防止把守卫收紧成“取消在途提交”。
func TestW09InFlightSubmitCompletesAfterDrain(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-w09-inflight", "sp-w09-inflight")
	setShortplaySettings(t, db, "sp-w09-inflight", audioModelSettings)

	submits := 0
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submits++
		// 提交已经开始，此刻收到 SIGTERM：BeginDrain 置标记并取消 worker ctx。
		svc.BeginDrain()
		task := &model.Task{
			ID: "task-w09-inflight", UserID: userID, ProjectID: req.ProjectID,
			Type: req.Type, Operation: req.Operation, Prompt: req.Prompt, Status: model.TaskStatusRunning,
		}
		if err := db.Create(task).Error; err != nil {
			return nil, err
		}
		return task, nil
	})
	queued, err := svc.QueueShortplayExtract("owner", "sp-w09-inflight")
	if err != nil || !queued.Queued {
		t.Fatalf("queue = %#v, %v", queued, err)
	}

	svc.tickShortplayExtract(context.Background())
	if submits != 1 {
		t.Fatalf("已进入的提交应当完成：submits=%d", submits)
	}
	var job model.ShortplayJob
	if err := db.Where("id = ?", queued.JobID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobRunning || job.TaskID != "task-w09-inflight" {
		t.Fatalf("在途提交没有被记录：status=%s taskId=%q", job.Status, job.TaskID)
	}

	// drain 生效后不得再开始下一次派发。
	svc.tickShortplayExtract(context.Background())
	if submits != 1 {
		t.Fatalf("drain 后又开始了新的派发：submits=%d", submits)
	}
}

// TestW09ProductionSubmitBindsJobInTaskTransaction 走真实 CreateTask，不注入任务提交桩：
// 任务行与 job.taskId 必须在同一次提交里出现，后续 tick 只能读回这一条任务。
func TestW09ProductionSubmitBindsJobInTaskTransaction(t *testing.T) {
	svc, db, _, _ := creationTestService(t)
	seedShortplay(t, db, "user", "sp-w09-atomic", "sp-w09-atomic")
	setShortplaySettings(t, db, "sp-w09-atomic",
		`{"models":{"text":{"channelId":"channel","modelKey":"text-test"}}}`)

	queued, err := svc.QueueShortplayExtract("user", "sp-w09-atomic")
	if err != nil || !queued.Queued {
		t.Fatalf("queue = %#v, %v", queued, err)
	}
	svc.tickShortplayExtract(context.Background())

	var job model.ShortplayJob
	if err := db.Where("id = ?", queued.JobID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobRunning || job.TaskID == "" {
		t.Fatalf("真实提交后 job 未绑定任务：status=%s taskId=%q lastError=%q",
			job.Status, job.TaskID, job.LastError)
	}
	var tasks []model.Task
	if err := db.Where("user_id = ? AND project_id = ? AND type = ?", "user", "sp-w09-atomic", "canvas_text").
		Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != job.TaskID {
		t.Fatalf("任务与 job 绑定不一致：job.taskId=%q tasks=%d", job.TaskID, len(tasks))
	}

	svc.tickShortplayExtract(context.Background())
	var count int64
	if err := db.Model(&model.Task{}).Where("user_id = ? AND project_id = ? AND type = ?", "user", "sp-w09-atomic", "canvas_text").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("后续 tick 重复提交任务：count=%d", count)
	}
}

type w09InjectedCrash struct{}

// TestW09AudioOrphanSettlesWithExistingResult 音色 job 在「任务已建、写回 taskId 之前」中断，
// 重启后必须按 metadata.shortplayJobId 找回并复用已成功结果：job 收口 succeeded、资产音色
// 变成 ready、只落一份样本，且不重复提交。
func TestW09AudioOrphanSettlesWithExistingResult(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	if err := db.AutoMigrate(&model.Resource{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Resource{
		ID: "res-w09-audio", UserID: "owner", Kind: "audio",
		MimeType: "audio/mpeg", Status: model.ResourceStatusReady,
	}).Error; err != nil {
		t.Fatal(err)
	}
	seedShortplay(t, db, "owner", "sp-w09-audio-orphan", "sp-w09-audio-orphan")
	setShortplaySettings(t, db, "sp-w09-audio-orphan", audioModelSettings)
	asset := seedVoicedAsset(t, svc, "sp-w09-audio-orphan", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")

	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		input, err := json.Marshal(req.Input)
		if err != nil {
			t.Fatal(err)
		}
		task := &model.Task{
			ID: "task-w09-audio-orphan", UserID: userID, ProjectID: req.ProjectID,
			Type: req.Type, Operation: req.Operation, Prompt: req.Prompt,
			InputJSON:  string(input),
			Status:     model.TaskStatusSucceeded,
			ResultJSON: `{"resourceId":"res-w09-audio"}`,
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		panic(w09InjectedCrash{})
	})
	queued, err := svc.QueueShortplayAssetAudio("owner", "sp-w09-audio-orphan", shortplay.QueueAssetAudioRequest{})
	if err != nil || queued.Queued != 1 {
		t.Fatalf("queue audio = %#v, %v", queued, err)
	}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("预期在写回 taskId 之前中断，tick 却正常返回")
			} else if _, ok := r.(w09InjectedCrash); !ok {
				t.Fatalf("非注入错误 panic: %v", r)
			}
		}()
		svc.tickShortplayAssetAudio(context.Background())
	}()

	var job model.ShortplayJob
	if err := db.Where("id = ?", queued.JobIDs[0]).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobRunning || job.TaskID != "" {
		t.Fatalf("注入未落在预期窗口：status=%s taskId=%q", job.Status, job.TaskID)
	}
	if err := db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}
	submits := 0
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submits++
		return nil, nil
	})
	svc.tickShortplayAssetAudio(context.Background())

	if err := db.Where("id = ?", queued.JobIDs[0]).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobSucceeded || job.TaskID != "task-w09-audio-orphan" {
		t.Fatalf("音色孤儿 job 没有复用已成功结果：status=%s taskId=%q lastError=%q",
			job.Status, job.TaskID, job.LastError)
	}
	if submits != 0 {
		t.Fatalf("孤儿 job 被重发 %d 次", submits)
	}
	var gotAsset model.ShortplayAsset
	if err := db.Where("id = ? AND shortplay_id = ?", asset.ID, "sp-w09-audio-orphan").First(&gotAsset).Error; err != nil {
		t.Fatal(err)
	}
	if gotAsset.AudioStatus != shortplay.ImageReady || gotAsset.CurrentAudioID == 0 {
		t.Fatalf("资产音色没有收口：status=%q currentAudioId=%d", gotAsset.AudioStatus, gotAsset.CurrentAudioID)
	}
	var samples int64
	if err := db.Model(&model.ShortplayAssetAudio{}).Where("shortplay_id = ? AND asset_id = ?", "sp-w09-audio-orphan", asset.ID).
		Count(&samples).Error; err != nil {
		t.Fatal(err)
	}
	if samples != 1 {
		t.Fatalf("音色样本条数=%d，应为 1", samples)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("任务数=%d，出现重复提交", tasks)
	}
}

// TestW09ImageOrphanRecoveredByAssociation 出图 job 走同一条孤儿回收路径：任务已建但没写回
// taskId 时，重启的 image tick 必须按 metadata.shortplayJobId 找回，且不重复提交。
func TestW09ImageOrphanRecoveredByAssociation(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	if err := db.AutoMigrate(&model.Resource{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Resource{
		ID: "res-w09-image-orphan", UserID: "owner", Kind: "image",
		MimeType: "image/png", Status: model.ResourceStatusReady,
	}).Error; err != nil {
		t.Fatal(err)
	}
	seedShortplay(t, db, "owner", "sp-w09-image-orphan", "sp-w09-image-orphan")
	setShortplaySettings(t, db, "sp-w09-image-orphan", audioModelSettings)
	asset := seedVoicedAsset(t, svc, "sp-w09-image-orphan", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")

	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		input, err := json.Marshal(req.Input)
		if err != nil {
			t.Fatal(err)
		}
		task := &model.Task{
			ID: "task-w09-image-orphan", UserID: userID, ProjectID: req.ProjectID,
			Type: req.Type, Operation: req.Operation, Prompt: req.Prompt,
			InputJSON: string(input), Status: model.TaskStatusSucceeded,
			ResultJSON: `{"resourceId":"res-w09-image-orphan"}`,
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		panic(w09InjectedCrash{})
	})
	queued, err := svc.QueueShortplayAssetImages("owner", "sp-w09-image-orphan", shortplay.QueueAssetImagesRequest{})
	if err != nil || queued.Queued != 1 {
		t.Fatalf("queue image = %#v, %v", queued, err)
	}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("预期在写回 taskId 之前中断，tick 却正常返回")
			} else if _, ok := r.(w09InjectedCrash); !ok {
				t.Fatalf("非注入错误 panic: %v", r)
			}
		}()
		svc.tickShortplayAssetImages(context.Background())
	}()

	var job model.ShortplayJob
	if err := db.Where("id = ?", queued.JobIDs[0]).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobRunning || job.TaskID != "" {
		t.Fatalf("注入未落在预期窗口：status=%s taskId=%q", job.Status, job.TaskID)
	}
	if err := db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}
	submits := 0
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submits++
		return nil, nil
	})
	svc.tickShortplayAssetImages(context.Background())

	if err := db.Where("id = ?", queued.JobIDs[0]).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobSucceeded || job.TaskID != "task-w09-image-orphan" {
		t.Fatalf("出图孤儿 job 没有关联找回：status=%s taskId=%q lastError=%q", job.Status, job.TaskID, job.LastError)
	}
	if submits != 0 {
		t.Fatalf("孤儿 job 被重发 %d 次", submits)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("任务数=%d，出现重复提交", tasks)
	}
	var gotAsset model.ShortplayAsset
	if err := db.Where("id = ? AND shortplay_id = ?", asset.ID, "sp-w09-image-orphan").First(&gotAsset).Error; err != nil {
		t.Fatal(err)
	}
	if gotAsset.ImageStatus != shortplay.ImageReady || gotAsset.CurrentImageID == 0 {
		t.Fatalf("资产参考图没有收口：status=%q currentImageId=%d", gotAsset.ImageStatus, gotAsset.CurrentImageID)
	}
	var samples int64
	if err := db.Model(&model.ShortplayAssetImage{}).Where("shortplay_id = ? AND asset_id = ?", "sp-w09-image-orphan", asset.ID).
		Count(&samples).Error; err != nil {
		t.Fatal(err)
	}
	if samples != 1 {
		t.Fatalf("参考图样本条数=%d，应为 1", samples)
	}
}

// TestW09OrphanWithoutTaskFailsExplicitly 孤儿 job 反查不到任务时，必须按合同明确失败并写明
// 结果待核对：不得停在 running（堵死 dedupe_key），也不得退回 queued 重发。
func TestW09OrphanWithoutTaskFailsExplicitly(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-w09-lost", "sp-w09-lost")
	setShortplaySettings(t, db, "sp-w09-lost", audioModelSettings)
	job := &model.ShortplayJob{
		ID: "job-w09-lost", ShortplayID: "sp-w09-lost", Kind: shortplay.JobExtract,
		Status:      shortplay.JobRunning,
		DedupeKey:   shortplay.JobDedupeKey(shortplay.JobExtract, "sp-w09-lost"),
		PayloadJSON: shortplay.ExtractCursorPayload(0),
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		t.Fatalf("找不到任务时不得重发提交")
		return nil, nil
	})

	svc.tickShortplayExtract(context.Background())

	var got model.ShortplayJob
	if err := db.Where("id = ?", job.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != shortplay.JobFailed {
		t.Fatalf("孤儿 job 未明确失败：status=%s taskId=%q lastError=%q", got.Status, got.TaskID, got.LastError)
	}
	if !strings.Contains(got.LastError, "结果待核对") {
		t.Fatalf("失败原因没有写明结果待核对：%q", got.LastError)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 0 {
		t.Fatalf("找不到任务却新建了 %d 条任务", tasks)
	}
}

// TestW09RestartSettlesSubmittedJob 重启后读回：job 停在 running 但已有 taskId，任务已成功，
// 新进程的 tick 应读回结果、落库并收口，且不重新提交。这条不能替代「drain 期间收口」。
func TestW09RestartSettlesSubmittedJob(t *testing.T) {
	_, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-w09-restart", "sp-w09-restart")
	setShortplaySettings(t, db, "sp-w09-restart", audioModelSettings)

	job := &model.ShortplayJob{
		ID: "job-w09-restart", ShortplayID: "sp-w09-restart", Kind: shortplay.JobExtract,
		Status:      shortplay.JobRunning,
		TaskID:      "task-w09-restart",
		DedupeKey:   shortplay.JobDedupeKey(shortplay.JobExtract, "sp-w09-restart"),
		PayloadJSON: shortplay.ExtractCursorPayload(0),
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		ID: "task-w09-restart", UserID: "owner", ProjectID: "p-sp-w09-restart",
		Type: "canvas_text", Operation: "text", Status: model.TaskStatusSucceeded,
		InputJSON:  `{"metadata":{"shortplayJobId":"job-w09-restart"}}`,
		ResultJSON: `{"text":` + strconv.Quote(extractReply()) + `}`,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	// 新进程：同一份库上的新 Service 实例。
	restarted := New(repository.New(db), t.TempDir())
	restarted.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		t.Fatalf("重启读回不应重新提交任务")
		return nil, nil
	})
	restarted.tickShortplayExtract(context.Background())

	var got model.ShortplayJob
	if err := db.Where("id = ?", "job-w09-restart").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != shortplay.JobSucceeded {
		t.Fatalf("重启读回未收口：status=%s taskId=%q lastError=%q", got.Status, got.TaskID, got.LastError)
	}
	var assets int64
	if err := db.Model(&model.ShortplayAsset{}).Where("shortplay_id = ?", "sp-w09-restart").Count(&assets).Error; err != nil {
		t.Fatal(err)
	}
	if assets == 0 {
		t.Fatalf("重启读回后没有落任何资产")
	}
}

// assertW09SettledUnknown 断言在途 job 已按「结果不明」显式收口：不再是 running，原因为
// 待核对，且没有新建任务（不重发）。
func assertW09SettledUnknown(t *testing.T, db *gorm.DB, jobID string) {
	t.Helper()
	var job model.ShortplayJob
	if err := db.Where("id = ?", jobID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status == shortplay.JobRunning {
		t.Fatalf("上游结果不明的 job 仍在 running：taskId=%q lastError=%q", job.TaskID, job.LastError)
	}
	if job.Status != shortplay.JobFailed {
		t.Fatalf("上游结果不明应按合同收口为 failed，实际 status=%s lastError=%q", job.Status, job.LastError)
	}
	if !strings.Contains(job.LastError, "待核对") {
		t.Fatalf("收口原因没有写明结果待核对：%q", job.LastError)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("结果不明时重发了任务：任务数=%d", tasks)
	}
}

// TestW09SubmissionUncertainSettlesWithoutResubmit 上游任务状态 submission_uncertain（提交结果
// 未知）时，在途 job 必须显式收口为 failed/待核对：不能停在 running 堵死去重键，也不能重发。
func TestW09SubmissionUncertainSettlesWithoutResubmit(t *testing.T) {
	seedUncertain := func(t *testing.T) (*Service, *gorm.DB) {
		t.Helper()
		svc, db := newShortplayWorkflowService(t)
		seedShortplay(t, db, "owner", "sp-w09-uncertain", "sp-w09-uncertain")
		setShortplaySettings(t, db, "sp-w09-uncertain", audioModelSettings)
		seedVoicedAsset(t, svc, "sp-w09-uncertain", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")
		return svc, db
	}
	seedTask := func(t *testing.T, db *gorm.DB, taskID, jobID string) {
		t.Helper()
		task := &model.Task{
			ID: taskID, UserID: "owner", ProjectID: "sp-w09-uncertain", Type: "canvas_text",
			Operation: "text", Status: model.TaskStatusSubmissionUncertain,
			InputJSON: `{"metadata":{"shortplayJobId":"` + jobID + `"}}`, ResultJSON: `{}`,
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}

	t.Run("extract", func(t *testing.T) {
		svc, db := seedUncertain(t)
		job := &model.ShortplayJob{
			ID: "job-unc-extract", ShortplayID: "sp-w09-uncertain", Kind: shortplay.JobExtract,
			Status: shortplay.JobRunning, TaskID: "task-unc-extract",
			DedupeKey:   shortplay.JobDedupeKey(shortplay.JobExtract, "sp-w09-uncertain"),
			PayloadJSON: shortplay.ExtractCursorPayload(0),
		}
		if err := db.Create(job).Error; err != nil {
			t.Fatal(err)
		}
		seedTask(t, db, "task-unc-extract", job.ID)
		svc.tickShortplayExtract(context.Background())
		assertW09SettledUnknown(t, db, job.ID)
	})

	t.Run("image", func(t *testing.T) {
		svc, db := seedUncertain(t)
		job := &model.ShortplayJob{
			ID: "job-unc-image", ShortplayID: "sp-w09-uncertain", Kind: shortplay.JobAssetImage,
			Status: shortplay.JobRunning, TaskID: "task-unc-image",
			DedupeKey:   shortplay.JobDedupeKey(shortplay.JobAssetImage, "sp-w09-uncertain|1"),
			PayloadJSON: shortplay.AssetImageJobPayload{AssetID: 1}.Encode(),
		}
		if err := db.Create(job).Error; err != nil {
			t.Fatal(err)
		}
		seedTask(t, db, "task-unc-image", job.ID)
		svc.tickShortplayAssetImages(context.Background())
		assertW09SettledUnknown(t, db, job.ID)
	})

	t.Run("audio", func(t *testing.T) {
		svc, db := seedUncertain(t)
		job := &model.ShortplayJob{
			ID: "job-unc-audio", ShortplayID: "sp-w09-uncertain", Kind: shortplay.JobAssetAudio,
			Status: shortplay.JobRunning, TaskID: "task-unc-audio",
			DedupeKey:   shortplay.JobDedupeKey(shortplay.JobAssetAudio, "sp-w09-uncertain|1"),
			PayloadJSON: shortplay.AssetAudioJobPayload{AssetID: 1, Prompt: "青年女声"}.Encode(),
		}
		if err := db.Create(job).Error; err != nil {
			t.Fatal(err)
		}
		seedTask(t, db, "task-unc-audio", job.ID)
		svc.tickShortplayAssetAudio(context.Background())
		assertW09SettledUnknown(t, db, job.ID)
	})
}

// TestW09OrphanLinkedOnceUnderConcurrency 并发找回同一条孤儿 job 时只能有一个认领者，
// 避免两个 tick 各自写入不同 taskId；认领失败的一方不得再推进这条行。
func TestW09OrphanLinkedOnceUnderConcurrency(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-w09-conc", "sp-w09-conc")
	job := &model.ShortplayJob{
		ID: "job-w09-conc", ShortplayID: "sp-w09-conc", Kind: shortplay.JobExtract,
		Status:      shortplay.JobRunning,
		DedupeKey:   shortplay.JobDedupeKey(shortplay.JobExtract, "sp-w09-conc"),
		PayloadJSON: shortplay.ExtractCursorPayload(0),
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		ID: "task-w09-conc", UserID: "owner", ProjectID: "sp-w09-conc", Type: "canvas_text",
		Operation: "text", Status: model.TaskStatusRunning,
		InputJSON: `{"metadata":{"shortplayJobId":"job-w09-conc"}}`,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	var wins, failures int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			linked, err := svc.recoverShortplayJobTask(&model.ShortplayJob{
				ID: job.ID, ShortplayID: job.ShortplayID, Kind: job.Kind,
			})
			if err != nil {
				atomic.AddInt32(&failures, 1)
				return
			}
			if linked {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()

	if failures != 0 {
		t.Fatalf("%d 个并发调用返回错误", failures)
	}
	var got model.ShortplayJob
	if err := db.Where("id = ?", job.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if wins != 1 {
		t.Fatalf("并发找回应当只有一个认领者，实际 %d 个（status=%s taskId=%q lastError=%q）",
			wins, got.Status, got.TaskID, got.LastError)
	}
	if got.TaskID != "task-w09-conc" {
		t.Fatalf("并发找回后 taskId = %q", got.TaskID)
	}
}

// TestW09AmbiguousTaskCandidatesFailExplicitly 同一 shortplayJobId 命中多条可信任务时，不得
// 挑一条当结果：job 明确失败并写明结果待核对，且不重发、不新增任务。
func TestW09AmbiguousTaskCandidatesFailExplicitly(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "sp-w09-amb", "sp-w09-amb")
	setShortplaySettings(t, db, "sp-w09-amb", audioModelSettings)

	job := &model.ShortplayJob{
		ID: "job-w09-amb", ShortplayID: "sp-w09-amb", Kind: shortplay.JobExtract,
		Status:      shortplay.JobRunning,
		DedupeKey:   shortplay.JobDedupeKey(shortplay.JobExtract, "sp-w09-amb"),
		PayloadJSON: shortplay.ExtractCursorPayload(0),
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{"task-w09-amb-1", "task-w09-amb-2"} {
		task := &model.Task{
			ID: taskID, UserID: "owner", ProjectID: "sp-w09-amb",
			Type: "canvas_text", Operation: "text", Status: model.TaskStatusSucceeded,
			InputJSON:  `{"metadata":{"shortplayJobId":"job-w09-amb"}}`,
			ResultJSON: `{"text":` + strconv.Quote(extractReply()) + `}`,
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}
	submits := 0
	svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
		submits++
		return nil, nil
	})

	svc.tickShortplayExtract(context.Background())

	var got model.ShortplayJob
	if err := db.Where("id = ?", job.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != shortplay.JobFailed || !strings.Contains(got.LastError, "多条") {
		t.Fatalf("多条候选没有明确失败：status=%s taskId=%q lastError=%q", got.Status, got.TaskID, got.LastError)
	}
	if submits != 0 {
		t.Fatalf("多条候选被重发 %d 次", submits)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 2 {
		t.Fatalf("任务数=%d，不应新增任务", tasks)
	}
}
