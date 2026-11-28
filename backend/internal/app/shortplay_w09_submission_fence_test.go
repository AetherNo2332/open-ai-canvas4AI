package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"
)

func TestW09LateSubmitFailurePreservesReplacement(t *testing.T) {
	for _, kind := range []string{shortplay.JobAssetImage, shortplay.JobAssetAudio} {
		t.Run(kind, func(t *testing.T) {
			svc, db := newShortplayWorkflowService(t)
			seedShortplay(t, db, "owner", "sp-late-failure", "sp-late-failure")
			setShortplaySettings(t, db, "sp-late-failure", audioModelSettings)
			seedVoicedAsset(t, svc, "sp-late-failure", 1, shortplay.AssetCharacter, "角色", "青年女声", "")
			job := model.ShortplayJob{
				ID: "old-submit", ShortplayID: "sp-late-failure", Kind: kind,
				Status: shortplay.JobRunning, DedupeKey: "late-failure",
				PayloadJSON: shortplay.AssetImageJobPayload{AssetID: 1}.Encode(),
			}
			if err := db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			svc.SetShortplayTaskHook(func(userID string, req CreateTaskRequest) (*model.Task, error) {
				if err := db.Model(&job).Update("updated_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
				if err := svc.failOrphanedShortplayJob(&job, "结果待核对，未自动重发"); err != nil {
					t.Fatal(err)
				}
				replacement := job
				replacement.ID, replacement.Status, replacement.TaskID = "replacement", shortplay.JobRunning, "replacement-task"
				if err := db.Create(&replacement).Error; err != nil {
					t.Fatal(err)
				}
				if err := projectShortplayJobMediaStatus(svc.repo, &replacement, replacement.PayloadJSON, shortplay.ImageRunning); err != nil {
					t.Fatal(err)
				}
				task := &model.Task{ID: "late-task", UserID: userID, ProjectID: req.ProjectID, Type: req.Type}
				err := svc.repo.Transaction(func(tx *repository.Repository) error { return req.afterTaskCreate(tx, task) })
				if !errors.Is(err, repository.ErrShortplayJobNotSubmitting) {
					t.Fatalf("late binding = %v", err)
				}
				return nil, err
			})
			var run shortplayImageJobRun
			if kind == shortplay.JobAssetImage {
				run = svc.runShortplayAssetImageJob(context.Background(), &job)
			} else {
				run = svc.runShortplayAssetAudioJob(&job)
			}
			if run.Status != shortplay.JobFailed {
				t.Fatalf("late submit = %+v", run)
			}
			if err := svc.finishShortplayJob(&job, run.Status, run.LastError, run.Payload); err != nil {
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
			if status != shortplay.ImageRunning {
				t.Fatalf("replacement asset overwritten by late failure: %s", status)
			}
			stored := loadShortplayJob(t, db, job.ID)
			if stored.LastError != "结果待核对，未自动重发" {
				t.Fatalf("recovery evidence overwritten: %q", stored.LastError)
			}
		})
	}
}

func TestW09SubmitFailureSettlesJobAndAssetTogether(t *testing.T) {
	for _, kind := range []string{shortplay.JobAssetImage, shortplay.JobAssetAudio} {
		t.Run(kind, func(t *testing.T) {
			svc, db := newShortplayWorkflowService(t)
			seedShortplay(t, db, "owner", "sp-submit-failure", "sp-submit-failure")
			setShortplaySettings(t, db, "sp-submit-failure", audioModelSettings)
			seedVoicedAsset(t, svc, "sp-submit-failure", 1, shortplay.AssetCharacter, "角色", "青年女声", "")
			job := model.ShortplayJob{ID: "submit-failure", ShortplayID: "sp-submit-failure", Kind: kind,
				Status: shortplay.JobQueued, DedupeKey: "submit-failure", PayloadJSON: shortplay.AssetImageJobPayload{AssetID: 1}.Encode()}
			if err := db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			svc.SetShortplayTaskHook(func(string, CreateTaskRequest) (*model.Task, error) {
				return nil, errors.New("injected admission failure")
			})
			if kind == shortplay.JobAssetImage {
				svc.tickShortplayAssetImages(context.Background())
			} else {
				svc.tickShortplayAssetAudio(context.Background())
			}
			stored := loadShortplayJob(t, db, job.ID)
			asset, err := svc.repo.ShortplayAssetByID(job.ShortplayID, 1)
			if err != nil {
				t.Fatal(err)
			}
			status := asset.ImageStatus
			if kind == shortplay.JobAssetAudio {
				status = asset.AudioStatus
			}
			if stored.Status != shortplay.JobFailed || status != shortplay.ImageFailed || stored.TaskID != "" {
				t.Fatalf("job=%+v asset status=%s", stored, status)
			}
		})
	}
}
