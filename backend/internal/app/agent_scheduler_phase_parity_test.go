package app

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
)

func TestAgentParityRuntimePhasePreservesStorageErrorAndLeaseLoss(t *testing.T) {
	t.Run("storage update error remains an internal error", func(t *testing.T) {
		s, db, run := piAgentTestLeasedFixture(t)
		session, _, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		owner := fmt.Sprintf("%s@%d", run.LeaseOwner, session.LeaseEpoch)
		storageErr := errors.New("injected runtime phase storage failure")
		injected := false
		const callbackName = "parity:runtime_phase_storage_failure"
		if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
			updates, ok := tx.Statement.Dest.(map[string]any)
			if tx.Statement.Table == "cloud_agent_executions" && ok && updates["runtime_phase"] != nil {
				injected = true
				tx.AddError(storageErr)
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer db.Callback().Update().Remove(callbackName)
		err = s.PiRuntimePhase(run.UserID, run.ID, owner, PiRuntimePhaseRequest{Phase: "waiting_tool", Kind: "tool", WaitID: "pending-operation"})
		if !injected {
			t.Fatal("storage failure did not reach the runtime phase update")
		}
		if !errors.Is(err, storageErr) {
			t.Fatalf("storage error was replaced with a lease failure: %v", err)
		}
		// Unstructured storage errors follow the HTTP handler's default 500 path.
		var appErr *kernel.AppError
		if errors.As(err, &appErr) {
			t.Fatalf("storage error became a public business error: %+v", appErr)
		}
		current, err := s.repo.CloudAgentControlRow(run.UserID, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.RuntimePhase != run.RuntimePhase || current.Revision != run.Revision {
			t.Fatal("failed runtime phase update changed persisted state")
		}
	})
	for _, kind := range []string{"empty owner", "wrong owner", "wrong epoch"} {
		t.Run(kind+" remains lease lost", func(t *testing.T) {
			s, _, run := piAgentTestLeasedFixture(t)
			session, _, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			owner := ""
			switch kind {
			case "wrong owner":
				owner = fmt.Sprintf("other-worker@%d", session.LeaseEpoch)
			case "wrong epoch":
				owner = fmt.Sprintf("%s@%d", run.LeaseOwner, session.LeaseEpoch+1)
			}
			err = s.PiRuntimePhase(run.UserID, run.ID, owner, PiRuntimePhaseRequest{Phase: "waiting_tool", Kind: "tool"})
			var appErr *kernel.AppError
			if !errors.As(err, &appErr) || appErr.Status != 403 || appErr.Reason != kernel.ReasonAgentLeaseLost {
				t.Fatalf("invalid lease no longer rejected with lease-lost 403: %v", err)
			}
		})
	}
}
