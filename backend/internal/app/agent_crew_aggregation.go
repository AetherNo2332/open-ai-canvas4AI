package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
)

type CrewCanvasOperation = agentCanvasOp
type CrewCanvasProposal = agentCanvasArgs

type CrewProposal struct {
	CanvasRevision int64                     `json:"canvasRevision"`
	ApprovalID     string                    `json:"approvalId"`
	SnapshotHash   string                    `json:"snapshotHash"`
	Ops            []CrewCanvasOperation     `json:"ops"`
	Summary        string                    `json:"summary"`
	Preview        cloudAgentApprovalPreview `json:"preview"`
	Results        []MemberTaskResult        `json:"results"`
	Decision       string                    `json:"decision,omitempty"`
	Reason         string                    `json:"reason,omitempty"`
	OperationID    string                    `json:"operationId,omitempty"`
	CommitHash     string                    `json:"commitHash,omitempty"`
	CommitResult   map[string]any            `json:"commitResult,omitempty"`
}

func (s *Service) AggregateMemberResults(runID string) (CrewProposal, error) {
	run, err := s.repo.AgentCrewRunInternal(runID)
	if err != nil {
		return CrewProposal{}, err
	}
	var proposal CrewProposal
	err = s.repo.MutateAgentCrewRun(run.UserID, runID, func(snapshot *model.AgentCrewRunSnapshot, repo *repository.Repository) error {
		var err error
		proposal, err = aggregateCrewResults(repo, snapshot)
		return err
	})
	return proposal, err
}

func aggregateCrewResults(repo *repository.Repository, snapshot *model.AgentCrewRunSnapshot) (CrewProposal, error) {
	if snapshot.Run.ProposalJSON != "" {
		var proposal CrewProposal
		err := json.Unmarshal([]byte(snapshot.Run.ProposalJSON), &proposal)
		return proposal, err
	}
	if crewRunTerminal(snapshot.Run.Status) {
		return CrewProposal{}, creationConflict("Crew 已结束")
	}
	coordinatorAllowed := false
	for _, m := range snapshot.Members {
		if m.Role == model.CrewMemberRoleCoordinator {
			coordinatorAllowed = m.Permission == model.CrewPermissionPropose && m.Status != model.MemberRunFailed && m.Status != model.MemberRunCancelled
		}
	}
	if !coordinatorAllowed {
		return CrewProposal{}, BadAuthRequest("Coordinator 没有提案权限")
	}
	results, pending, err := crewResults(snapshot)
	if err != nil {
		return CrewProposal{}, err
	}
	if pending {
		return CrewProposal{}, creationConflict("成员尚未完成")
	}
	proposal := CrewProposal{ApprovalID: newID(), Ops: []CrewCanvasOperation{}, Results: results}
	summaries := []string{}
	seen := map[string]string{}
	for _, result := range results {
		summaries = append(summaries, result.Summary)
		if result.Proposal == nil {
			continue
		}
		if proposal.SnapshotHash != "" && result.Proposal.SnapshotHash != proposal.SnapshotHash {
			return CrewProposal{}, creationConflict("成员提案快照不一致")
		}
		proposal.SnapshotHash = result.Proposal.SnapshotHash
		for _, op := range result.Proposal.Ops {
			raw, _ := json.Marshal(op)
			if old, exists := seen[op.ID]; exists {
				if old != string(raw) {
					return CrewProposal{}, creationConflict("成员提案修改同一对象且内容冲突")
				}
				continue
			}
			seen[op.ID] = string(raw)
			proposal.Ops = append(proposal.Ops, op)
		}
	}
	proposal.Summary = strings.Join(summaries, "\n")
	canvas, err := repo.CanvasProjectForUser(snapshot.Run.UserID, snapshot.Run.CanvasID)
	if err != nil {
		return CrewProposal{}, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return CrewProposal{}, err
	}
	proposal.CanvasRevision = canvas.Revision
	if proposal.SnapshotHash == "" {
		proposal.SnapshotHash = cloudAgentCanvasHash(doc)
	}
	if len(proposal.Summary) > 16000 || len(proposal.Ops) > 20 {
		return CrewProposal{}, BadAuthRequest("Crew 聚合提案超限")
	}
	if len(proposal.Ops) > 0 {
		raw, _ := json.Marshal(CrewCanvasProposal{SnapshotHash: proposal.SnapshotHash, Ops: proposal.Ops})
		call := cloudAgentCall{}
		call.Function.Name = "canvas_apply_ops"
		call.Function.Arguments = string(raw)
		plan, err := prepareCloudAgentCanvasMutation(repo, snapshot.Run.UserID, snapshot.Run.CanvasID, call)
		if err != nil {
			return CrewProposal{}, creationConflict("画布已变化或聚合提案不再有效")
		}
		proposal.Preview = plan.Preview
	}
	snapshot.Run.ApprovalID = proposal.ApprovalID
	snapshot.Run.Status = model.CrewRunWaitingApproval
	raw, err := json.Marshal(proposal)
	if err != nil {
		return CrewProposal{}, err
	}
	snapshot.Run.ProposalJSON = string(raw)
	return proposal, nil
}
