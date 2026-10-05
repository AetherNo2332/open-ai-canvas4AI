package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func (s *Service) CommitCrewProposal(userID, runID, approvalID, expectedSnapshotHash, idempotencyKey string) (map[string]any, error) {
	if !crewValidText(idempotencyKey, 120) {
		return nil, BadAuthRequest("Crew 提交幂等键无效")
	}
	if _, err := s.GetCrewRun(userID, runID); err != nil {
		return nil, err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	hash := cloudAgentTextDigest(approvalID + "\x00" + expectedSnapshotHash + "\x00" + idempotencyKey)
	var result map[string]any
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	err = s.repo.MutateAgentCrewRun(userID, runID, func(snapshot *model.AgentCrewRunSnapshot, repo *repository.Repository) error {
		if snapshot.Run.ApprovalID != approvalID {
			return creationConflict("审批身份不一致")
		}
		var proposal CrewProposal
		if err := json.Unmarshal([]byte(snapshot.Run.ProposalJSON), &proposal); err != nil {
			return err
		}
		if proposal.OperationID != "" {
			if proposal.CommitHash != hash {
				return creationConflict("审批已用于另一提交")
			}
			result = proposal.CommitResult
			return nil
		}
		if snapshot.Run.Status != model.CrewRunWaitingApproval || proposal.Decision != "approve" {
			return creationConflict("Crew 提案未批准")
		}
		if expectedSnapshotHash != proposal.SnapshotHash {
			return creationConflict("审批快照不一致")
		}
		canvas, err := repo.CanvasProjectForUser(userID, snapshot.Run.CanvasID)
		if err != nil {
			return err
		}
		if canvas.Revision != proposal.CanvasRevision {
			return creationConflict("画布 revision 已变化，请重新生成提案")
		}
		for _, m := range snapshot.Members {
			if m.Role == model.CrewMemberRoleCoordinator && (m.Permission != model.CrewPermissionPropose || m.Status == model.MemberRunFailed || m.Status == model.MemberRunCancelled) {
				return creationConflict("Coordinator 状态或权限已变化")
			}
		}
		if len(proposal.Ops) > 0 {
			raw, _ := json.Marshal(CrewCanvasProposal{SnapshotHash: proposal.SnapshotHash, Ops: proposal.Ops})
			call := cloudAgentCall{ID: idempotencyKey}
			call.Function.Name = "canvas_apply_ops"
			call.Function.Arguments = string(raw)
			written, writeErr := applyCloudAgentCanvas(repo, userID, snapshot.Run.CanvasID, call, policy, cloudAgentMutationRecorderForRun(snapshot.Run.CoordinatorRunID))
			if writeErr != nil {
				if cloudAgentSnapshotConflict(writeErr) {
					return creationConflict("画布已变化，请重新生成提案")
				}
				return writeErr
			}
			proposal.CommitResult = written.(map[string]any)
		} else {
			proposal.CommitResult = map[string]any{"summary": proposal.Summary, "canvasId": snapshot.Run.CanvasID}
		}
		proposal.OperationID, proposal.CommitHash = idempotencyKey, hash
		raw, err := json.Marshal(proposal)
		if err != nil {
			return err
		}
		snapshot.Run.ProposalJSON = string(raw)
		snapshot.Run.Status = model.CrewRunCompleted
		result = proposal.CommitResult
		coordinator, err := repo.CloudAgent(userID, snapshot.Run.CoordinatorRunID)
		if err != nil {
			return err
		}
		if err := repo.MutateCloudAgent(userID, coordinator.ID, coordinator.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			if state.CallIndex < len(state.Calls) && state.Calls[state.CallIndex].Function.Name == "crew_propose" {
				call := state.Calls[state.CallIndex]
				cloudAgentRecordToolResult(current, &state, call, result, nil)
				skipRemainingCloudAgentCalls(current.ID, &state)
			}
			state.Crew.ApprovalRequired = false
			state.event(current.ID, "run_completed", map[string]any{"summary": proposal.Summary})
			current.Status = "completed"
			current.CleanupPending = true
			return cloudAgentSave(current, &state)
		}); err != nil {
			return err
		}
		for i := range snapshot.Members {
			if snapshot.Members[i].Role == model.CrewMemberRoleCoordinator {
				snapshot.Members[i].Status = model.MemberRunCompleted
			}
		}
		return nil
	})
	return result, err
}
