package mediaversions

import (
	"errors"
	"strings"
	"testing"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestLegacyAdmissionDistinguishesExplicitNewAttemptAndOriginalSaveRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, intent, recovery, stage, reason string
		status                                model.TaskStatus
		empty                                 bool
	}{
		{name: "ended requires intent", intent: "create", status: model.TaskStatusFailed, empty: true, reason: "new_version_required"},
		{name: "old output plus save recovery", intent: "new_version", status: model.TaskStatusFailed, recovery: "opaque", stage: "upload", reason: "materialization_pending"},
		{name: "completed media stage", intent: "new_version", status: model.TaskStatusSucceeded, stage: "completed"},
		{name: "completed with retained checkpoint", intent: "new_version", status: model.TaskStatusSucceeded, stage: "completed", recovery: "encrypted completed checkpoint"},
		{name: "unfinished checkpoint on succeeded task", intent: "new_version", status: model.TaskStatusSucceeded, stage: "upload", recovery: "encrypted pending checkpoint", reason: "materialization_pending"},
		{name: "failed with checkpoint", intent: "new_version", status: model.TaskStatusFailed, stage: "completed", recovery: "encrypted failed checkpoint", reason: "materialization_pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, scope, node := versionFixture(t, "image")
			if tc.empty {
				delete(metadata(node), "storageKey")
				delete(metadata(node), "content")
			}
			metadata(node)["taskId"] = "admission-task"
			versionTx(t, repo, func(tx *repository.Repository) error {
				return tx.InsertPreparedCanvasTask(&model.Task{ID: "admission-task", UserID: "owner", Status: tc.status, MediaStage: tc.stage, MediaRecoveryJSON: tc.recovery})
			})
			err := CheckAdmission(repo, scope, node, tc.intent)
			var rejected *Error
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &rejected) || rejected.Reason != tc.reason {
				t.Fatalf("admission=%v want %s", err, tc.reason)
			}
		})
	}
}

func TestLegacyFailedAttemptImportsHistoryWithoutFakeReadyMedia(t *testing.T) {
	repo, scope, node := versionFixture(t, "image")
	delete(metadata(node), "content")
	delete(metadata(node), "storageKey")
	metadata(node)["taskId"] = "failed-legacy"
	versionTx(t, repo, func(tx *repository.Repository) error {
		if err := tx.InsertPreparedCanvasTask(&model.Task{ID: "failed-legacy", UserID: "owner", Status: model.TaskStatusFailed, InputJSON: `{"config":{"apiKey":"private","url":"https://private?token=private"}}`}); err != nil {
			return err
		}
		_, err := Begin(tx, scope, node, model.Task{ID: "retry-task", UserID: "owner", Status: model.TaskStatusQueued}, "new_version")
		return err
	})
	rows, err := repo.CanvasMediaVersions(scope, 0, 20)
	if err != nil || len(rows) != 2 || rows[1].Status != "failed" || rows[1].ResourceID != "" || rows[0].Sequence != 2 || ReadState(node).CurrentVersionID != "" {
		t.Fatalf("failed baseline: %+v %v", rows, err)
	}
	if strings.Contains(rows[1].RequestJSON, "private") {
		t.Fatal("legacy credential/url persisted in version")
	}
}
