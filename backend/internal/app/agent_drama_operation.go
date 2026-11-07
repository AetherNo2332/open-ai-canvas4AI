package app

import (
	"context"
	"time"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/repository"
)

// Shot edits and their success receipt commit together. If the outer Pi/history
// reply is lost, replay returns this receipt before touching a newer user edit.
func (a dramaAgentAdapter) updateShotAtomically(call as.ToolInvocation) (as.ToolResult, error) {
	var result as.ToolResult
	err := a.s.repo.Transaction(func(tx *repository.Repository) error {
		stored, err := tx.LockAgentOperation(call.OwnerID, call.OperationID)
		if err != nil {
			return err
		}
		if stored.ArgumentsHash != agentArgumentsHash(call.Arguments) {
			return as.NewError(as.CodeIdempotencyConflict, "同一操作标识被用于不同参数")
		}
		if stored.Status == agentOperationSucceeded {
			result, err = storedAgentToolResult(stored)
			return err
		}
		local := &Service{repo: tx, dataDir: a.s.dataDir}
		if _, err := local.fenceAgentToolExecution(call); err != nil {
			return err
		}
		inner := call
		inner.OperationID = ""
		result, err = (dramaAgentAdapter{s: local}).Execute(context.Background(), inner)
		if err != nil {
			return err
		}
		status, payload := agentOperationOutcome(result, nil)
		return tx.FinishAgentOperation(call.OwnerID, call.OperationID, status, payload, result.Operation, time.Now())
	})
	return result, err
}
