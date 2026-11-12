package app

import (
	"errors"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCloudAgentApprovalCASConflict(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		name := "one concurrent checkpoint"
		if persistent {
			name = "continued contention"
		}
		t.Run(name, func(t *testing.T) {
			s, db, args := agentMediaFixture(t)
			run, _ := agentMediaRun(t, s, args, "request_approval")
			if _, err := s.PiToolAdvance("user", run.ID, "worker-a", agentPiToolTaskID(t, s, run.ID), "media-call"); err != nil {
				t.Fatal(err)
			}
			waiting, err := s.CloudAgentRun("user", run.ID)
			if err != nil || waiting.Approval == nil {
				t.Fatalf("approval not prepared: %v", err)
			}
			attempts := 0
			callback := "test:approval-cas-contention"
			if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if _, execution := tx.Statement.Model.(*model.CloudAgentExecution); execution {
					attempts++
					if persistent || attempts == 1 {
						tx.AddError(repository.ErrCreationConflict)
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			err = s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", "")
			db.Callback().Update().Remove(callback)
			if persistent {
				var failure *kernel.AppError
				if !errors.As(err, &failure) || failure.Status != 409 || attempts > 4 {
					t.Fatalf("continued contention must be bounded and return conflict: attempts=%d err=%v", attempts, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("one concurrent checkpoint must not lose the approval: %v", err)
			}
			after, err := s.CloudAgentRun("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			decisions := 0
			for _, event := range after.Events {
				if event.Type == "approval_decided" {
					decisions++
				}
			}
			if decisions != 1 {
				t.Fatalf("approval must commit exactly once, got %d decision events", decisions)
			}
		})
	}
}
