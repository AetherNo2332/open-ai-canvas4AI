package tasks

import (
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
)

// Node metadata is presentation and can be changed by history. Accepted task
// bindings remain immutable; only persisted task status releases the target.
func validateActiveTarget(tx *repository.Repository, p identity.Principal, req SubmitRequest) error {
	bindings, err := tx.ActiveCanvasTaskBindings(p.OwnerID, req.Command.CanvasID, req.Command.DocumentEpoch)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		var old Consumer
		if err = json.Unmarshal([]byte(binding.ConsumerJSON), &old); err != nil {
			return err
		}
		sameTarget := binding.TargetNodeID == req.TargetNodeID && (old.RowID == "" || req.Consumer.RowID == "" || old.RowID == req.Consumer.RowID)
		sameOutput := req.Consumer.OutputNodeID != "" && (old.OutputNodeID == req.Consumer.OutputNodeID || binding.TargetNodeID == req.Consumer.OutputNodeID)
		inputIsOutput := old.OutputNodeID != "" && old.OutputNodeID == req.TargetNodeID
		if sameTarget || sameOutput || inputIsOutput {
			return commands.BadAuthRequest("目标仍有未完成任务，请先处理已有任务")
		}
	}
	return nil
}
