package tasks

import (
	"context"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/repository"
)

// A host's explicit acknowledgement ends LOCAL waiting. It never asserts that
// the provider cancelled a request, and never submits another generation.
func (s *Service) ResolveUncertainty(ctx context.Context, p identity.Principal, taskID string, req commands.Command) (commands.Receipt, error) {
	if p.Role != "owner" || p.ID != p.OwnerID || !p.HasScope("canvas.write") {
		return commands.Receipt{}, &commands.Error{Reason: "permission_denied", Message: "仅主机可核对接单状态并结束等待"}
	}
	if len(req.Operations) > 0 {
		return commands.Receipt{}, commands.BadAuthRequest("状态核对不能附带其他操作")
	}
	s.materializeMu.Lock()
	defer s.materializeMu.Unlock()
	receipt, _, err := s.commands.ExecuteHost(ctx, p, req, "task_submit", map[string]any{"acknowledgedSubmissionUncertainty": taskID}, false, func(tx *repository.Repository, doc commands.Document) (commands.Document, map[string]bool, error) {
		binding, e := tx.CanvasTaskBinding(taskID)
		if e != nil || binding.OwnerID != p.OwnerID || binding.CanvasID != req.CanvasID || binding.Epoch != req.DocumentEpoch {
			return nil, nil, commands.BadAuthRequest("任务不属于此画布世代")
		}
		if e = tx.CloseCanvasSubmissionUncertainty(p.OwnerID, taskID); e != nil {
			return nil, nil, commands.BadAuthRequest("任务当前无需核对接单状态")
		}
		return doc, map[string]bool{}, nil
	})
	return receipt, err
}
