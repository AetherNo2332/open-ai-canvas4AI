package app

import (
	"errors"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCloudAgentApprovalRetriesRevisionConflict(t *testing.T) {
	for _, mode := range []string{"once", "persistent", "storage"} {
		t.Run(mode, func(t *testing.T) {
			s, db, root := permissionTestRun(t, "request_approval")
			agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
			_, state := agentSettleToolStep(t, s, db, root.ID, "write-1", "canvas_apply_ops", permissionAddNodeArgs(t, db, "conflict-node"))
			if state.Approval == nil {
				t.Fatal("missing approval")
			}
			id := state.Approval.ID
			attempts := 0
			storageErr := errors.New("injected storage failure")
			const callback = "approval_revision_conflict"
			if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if _, ok := tx.Statement.Model.(*model.CloudAgentExecution); !ok {
					return
				}
				attempts++
				if mode == "storage" {
					tx.AddError(storageErr)
				} else if mode == "persistent" || attempts == 1 {
					tx.AddError(repository.ErrCreationConflict)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
			err := s.DecideCloudAgentApproval("user", root.ID, id, "approve", "")
			if mode == "storage" {
				if !errors.Is(err, storageErr) || attempts != 1 {
					t.Fatalf("storage failure was retried/swallowed: attempts=%d err=%v", attempts, err)
				}
				return
			}
			if mode == "persistent" {
				var apiErr *kernel.AppError
				if !errors.As(err, &apiErr) || apiErr.Status != 409 || attempts != 3 {
					t.Fatalf("persistent conflict: attempts=%d err=%v", attempts, err)
				}
				return
			}
			if err != nil || attempts < 2 {
				t.Fatalf("revision conflict escaped: attempts=%d err=%v", attempts, err)
			}
			if err := s.DecideCloudAgentApproval("user", root.ID, id, "approve", ""); err != nil {
				t.Fatal(err)
			}
			_, saved := agentInterjectionState(t, s, root.ID)
			count := 0
			for _, event := range saved.Events {
				if event.Type == "approval_decided" {
					count++
				}
			}
			if saved.Decisions[id] != "approve" || count != 1 {
				t.Fatalf("approval replay changed outcome: decision=%s events=%d", saved.Decisions[id], count)
			}
			if permissionCanvasHas(t, db, "conflict-node") {
				t.Fatal("decision executed the write before Pi resumed")
			}
		})
	}
}
