package app

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type MemberTaskResult struct {
	Proposal    *CrewCanvasProposal `json:"proposal,omitempty"`
	TaskID      string              `json:"taskId"`
	Summary     string              `json:"summary"`
	ArtifactIDs []string            `json:"artifactIds"`
	ErrorCode   string              `json:"errorCode,omitempty"`
}

func crewRunTerminal(status model.CrewRunStatus) bool {
	return status == model.CrewRunCompleted || status == model.CrewRunFailed || status == model.CrewRunCancelled
}

func crewValidText(text string, limit int) bool {
	return strings.TrimSpace(text) != "" && utf8.ValidString(text) && len(text) <= limit
}

// These entry points are internal orchestration methods, never HTTP authority.
func (s *Service) DispatchCrewTask(runID, memberID string, task CrewTaskInput) error {
	run, err := s.repo.AgentCrewRunInternal(runID)
	if err != nil {
		return err
	}
	return s.repo.MutateAgentCrewRun(run.UserID, runID, func(snapshot *model.AgentCrewRunSnapshot, repo *repository.Repository) error {
		return dispatchCrewTask(repo, snapshot, memberID, task)
	})
}

func crewValidateRefs(repo *repository.Repository, run model.AgentCrewRun, refs []string) error {
	if len(refs) > 32 {
		return BadAuthRequest("Crew 引用数量超限")
	}
	canvas, err := repo.CanvasProjectForUser(run.UserID, run.CanvasID)
	if err != nil {
		return crewPublicError(err)
	}
	var content struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(canvas.PayloadJSON), &content); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, node := range content.Nodes {
		known[node.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range refs {
		if !known[id] || seen[id] {
			return BadAuthRequest("Crew 引用不属于当前画布或重复")
		}
		seen[id] = true
	}
	return nil
}

func dispatchCrewTask(repo *repository.Repository, snapshot *model.AgentCrewRunSnapshot, memberID string, task CrewTaskInput) error {
	if crewRunTerminal(snapshot.Run.Status) || snapshot.Run.Status == model.CrewRunWaitingApproval {
		return creationConflict("Crew 已结束或等待审批")
	}
	if !crewValidText(task.TaskID, 120) || !crewValidText(task.Title, 240) || !crewValidText(task.Instructions, 12000) || len(task.FocusNodeIDs) > 8 {
		return BadAuthRequest("委派任务参数无效")
	}
	if err := crewValidateRefs(repo, snapshot.Run, task.ArtifactIDs); err != nil {
		return err
	}
	if err := crewValidateRefs(repo, snapshot.Run, task.FocusNodeIDs); err != nil {
		return err
	}
	raw, _ := json.Marshal(task)
	hash := cloudAgentTextDigest(string(raw))
	var target *model.AgentCrewMemberRun
	active := 0
	for i := range snapshot.Members {
		m := &snapshot.Members[i]
		if m.MemberID == memberID && m.Role == model.CrewMemberRoleMember {
			target = m
		}
		if m.Role == model.CrewMemberRoleMember && (m.Status == model.MemberRunQueued || m.Status == model.MemberRunRunning) {
			active++
		}
		if m.TaskID == task.TaskID && m.MemberID != memberID {
			return creationConflict("taskId 已用于其他成员")
		}
	}
	if target == nil {
		return BadAuthRequest("成员不属于此 Crew")
	}
	if target.TaskID != "" {
		if target.TaskHash == hash {
			return nil
		}
		return creationConflict("成员任务已经冻结")
	}
	var budget CrewRunBudget
	if err := json.Unmarshal([]byte(snapshot.Run.BudgetJSON), &budget); err != nil {
		return err
	}
	if active >= budget.MaxConcurrentMembers {
		return creationConflict("Crew 并发槽已满")
	}
	execution, err := repo.CloudAgent(snapshot.Run.UserID, target.AgentRunID)
	if err != nil {
		return err
	}
	if execution.Status != "waiting_member" {
		return creationConflict("成员执行状态不允许委派")
	}
	err = repo.MutateCloudAgent(execution.UserID, execution.ID, execution.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		if state.Crew == nil || state.Crew.MemberRunID != target.ID {
			return BadAuthRequest("Crew 执行上下文不一致")
		}
		state.Crew.Task = &task
		state.Request.crew = state.Crew
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "Structured Crew task: " + string(raw)})
		current.Status = "queued"
		return cloudAgentSave(current, &state)
	})
	if err != nil {
		return err
	}
	target.TaskID, target.TaskHash, target.TaskJSON, target.Status = task.TaskID, hash, string(raw), model.MemberRunQueued
	snapshot.Run.Status = model.CrewRunRunning
	return repo.AppendAgentCrewMessage(&model.AgentCrewMessage{ID: newID(), CrewRunID: snapshot.Run.ID, MemberRunID: target.ID, TaskID: task.TaskID, Kind: "task", PayloadJSON: string(raw), ParameterHash: hash})
}

func (s *Service) CompleteMemberRun(memberRunID string, result MemberTaskResult) error {
	member, err := s.repo.AgentCrewMemberRunInternal(memberRunID)
	if err != nil {
		return err
	}
	run, err := s.repo.AgentCrewRunInternal(member.CrewRunID)
	if err != nil {
		return err
	}
	return s.repo.MutateAgentCrewRun(run.UserID, run.ID, func(snapshot *model.AgentCrewRunSnapshot, repo *repository.Repository) error {
		if err := completeCrewMember(repo, snapshot, memberRunID, result); err != nil {
			return err
		}
		execution, err := repo.CloudAgent(run.UserID, member.AgentRunID)
		if err != nil {
			return err
		}
		if cloudAgentRunTerminal(execution.Status) {
			return nil
		}
		return repo.MutateCloudAgent(run.UserID, execution.ID, execution.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			current.Status = "completed"
			current.CleanupPending = true
			state.event(current.ID, "run_completed", map[string]any{"summary": "成员任务已提交"})
			return cloudAgentSave(current, &state)
		})
	})
}

func completeCrewMember(repo *repository.Repository, snapshot *model.AgentCrewRunSnapshot, memberRunID string, result MemberTaskResult) error {
	if crewRunTerminal(snapshot.Run.Status) {
		return creationConflict("Crew 已结束")
	}
	if !crewValidText(result.TaskID, 120) || !crewValidText(result.Summary, 4000) || len(result.ErrorCode) > 80 {
		return BadAuthRequest("成员结果无效")
	}
	if err := crewValidateRefs(repo, snapshot.Run, result.ArtifactIDs); err != nil {
		return err
	}
	raw, _ := json.Marshal(result)
	for i := range snapshot.Members {
		m := &snapshot.Members[i]
		if m.ID != memberRunID {
			continue
		}
		if m.Role != model.CrewMemberRoleMember || m.TaskID != result.TaskID {
			return BadAuthRequest("成员结果与委派任务不符")
		}
		if result.Proposal != nil {
			if m.Permission != model.CrewPermissionPropose || result.ErrorCode != "" {
				return BadAuthRequest("成员没有提案权限或结果失败")
			}
			proposalRaw, _ := json.Marshal(result.Proposal)
			call := cloudAgentCall{}
			call.Function.Name = "canvas_apply_ops"
			call.Function.Arguments = string(proposalRaw)
			if _, err := prepareCloudAgentCanvasMutation(repo, snapshot.Run.UserID, snapshot.Run.CanvasID, call); err != nil {
				return err
			}
		}
		if m.ResultJSON != "" {
			if m.ResultJSON == string(raw) {
				return nil
			}
			return creationConflict("成员结果已确认")
		}
		if m.Status != model.MemberRunQueued && m.Status != model.MemberRunRunning {
			return creationConflict("成员已经结束")
		}
		m.ResultJSON, m.Summary, m.ErrorCode, m.Status = string(raw), result.Summary, result.ErrorCode, model.MemberRunCompleted
		if result.ErrorCode != "" {
			m.Status = model.MemberRunFailed
		}
		return repo.AppendAgentCrewMessage(&model.AgentCrewMessage{ID: newID(), CrewRunID: snapshot.Run.ID, MemberRunID: m.ID, TaskID: m.TaskID, Kind: "result", PayloadJSON: string(raw), ParameterHash: cloudAgentTextDigest(string(raw))})
	}
	return BadAuthRequest("成员不属于此 Crew")
}

func crewResults(snapshot *model.AgentCrewRunSnapshot) ([]MemberTaskResult, bool, error) {
	results := []MemberTaskResult{}
	pending := false
	for _, m := range snapshot.Members {
		if m.Role != model.CrewMemberRoleMember || m.TaskID == "" {
			continue
		}
		if m.ResultJSON == "" {
			if m.Status == model.MemberRunFailed || m.Status == model.MemberRunCancelled {
				results = append(results, MemberTaskResult{TaskID: m.TaskID, Summary: "成员任务未完成", ErrorCode: m.ErrorCode, ArtifactIDs: []string{}})
				continue
			}
			pending = true
			continue
		}
		var result MemberTaskResult
		if err := json.Unmarshal([]byte(m.ResultJSON), &result); err != nil {
			return nil, false, err
		}
		results = append(results, result)
	}
	return results, pending, nil
}

func executeCrewTool(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	context := state.Crew
	if context == nil {
		return nil, BadAuthRequest("Crew 工具未授权")
	}
	var result any
	err := repo.MutateAgentCrewRun(run.UserID, context.CrewRunID, func(snapshot *model.AgentCrewRunSnapshot, tx *repository.Repository) error {
		if snapshot.Run.CanvasID != run.CanvasID {
			return BadAuthRequest("Crew 画布不匹配")
		}
		authorized := false
		for _, m := range snapshot.Members {
			if m.ID == context.MemberRunID && m.AgentRunID == run.ID && m.Attempt == context.Attempt {
				authorized = true
			}
		}
		if !authorized {
			return BadAuthRequest("Crew 执行身份不匹配")
		}
		switch call.Function.Name {
		case "crew_propose":
			if context.Role != model.CrewMemberRoleCoordinator || context.Permission != model.CrewPermissionPropose {
				return BadAuthRequest("Coordinator 没有提案权限")
			}
			proposal, err := aggregateCrewResults(tx, snapshot)
			if err != nil {
				return err
			}
			context.ApprovalRequired = true
			result = map[string]any{"approvalId": proposal.ApprovalID, "preview": proposal.Preview}
		case "delegate_task":
			if context.Role != model.CrewMemberRoleCoordinator {
				return BadAuthRequest("仅 Coordinator 可以委派")
			}
			var args struct {
				MemberID string `json:"memberId"`
				CrewTaskInput
			}
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return BadAuthRequest("委派参数无效")
			}
			if err := dispatchCrewTask(tx, snapshot, args.MemberID, args.CrewTaskInput); err != nil {
				return err
			}
			result = map[string]any{"taskId": args.TaskID, "memberId": args.MemberID, "accepted": true}
			context.ResultsCollected = false
		case "task_result":
			if context.Role != model.CrewMemberRoleMember {
				return BadAuthRequest("仅成员可以提交结果")
			}
			var args MemberTaskResult
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return BadAuthRequest("成员结果参数无效")
			}
			if err := completeCrewMember(tx, snapshot, context.MemberRunID, args); err != nil {
				return err
			}
			result = map[string]any{"taskId": args.TaskID, "accepted": true}
		case "crew_wait":
			if context.Role != model.CrewMemberRoleCoordinator {
				return BadAuthRequest("仅 Coordinator 可以等待成员")
			}
			results, pending, err := crewResults(snapshot)
			if err != nil {
				return err
			}
			if pending {
				return creationConflict("成员仍在执行")
			}
			result = map[string]any{"results": results}
			context.ResultsCollected = true
			for _, memberResult := range results {
				if memberResult.Proposal != nil {
					context.ApprovalRequired = true
				}
			}
		}
		return nil
	})
	return result, err
}
