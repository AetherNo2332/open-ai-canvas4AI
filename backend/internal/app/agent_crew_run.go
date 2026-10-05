package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/skills"
)

type CrewTaskInput struct {
	TaskID       string   `json:"taskId"`
	Title        string   `json:"title"`
	Instructions string   `json:"instructions"`
	ArtifactIDs  []string `json:"artifactIds"`
	FocusNodeIDs []string `json:"focusNodeIds"`
}
type CrewDelegateTarget struct {
	MemberID   string                     `json:"memberId"`
	Name       string                     `json:"name"`
	Permission model.CrewMemberPermission `json:"permissionMode"`
}
type CrewMemberRuntime struct {
	ApprovalRequired bool                       `json:"approvalRequired,omitempty"`
	ResultsCollected bool                       `json:"resultsCollected,omitempty"`
	CrewRunID        string                     `json:"crewRunId"`
	MemberRunID      string                     `json:"memberRunId"`
	MemberID         string                     `json:"memberId"`
	Role             model.CrewMemberRole       `json:"role"`
	Permission       model.CrewMemberPermission `json:"permissionMode"`
	Attempt          int                        `json:"attempt"`
	Budget           CrewMemberBudget           `json:"budget"`
	Task             *CrewTaskInput             `json:"task,omitempty"`
	Members          []CrewDelegateTarget       `json:"members,omitempty"`
}
type CrewRunBudget struct {
	MaxCredits           float64 `json:"maxCredits"`
	MaxSteps             int     `json:"maxSteps"`
	MaxGenerationTasks   int     `json:"maxGenerationTasks"`
	MaxVideoSeconds      int     `json:"maxVideoSeconds"`
	MaxConcurrentMembers int     `json:"maxConcurrentMembers"`
}
type CreateCrewRunInput struct {
	Prompt               string   `json:"prompt"`
	SkillIDs             []string `json:"skillIds"`
	IdempotencyKey       string   `json:"idempotencyKey"`
	MaxConcurrentMembers int      `json:"maxConcurrentMembers"`
}
type CrewMemberRunView struct {
	ID         string                     `json:"id"`
	MemberID   string                     `json:"memberId"`
	AgentRunID string                     `json:"agentRunId"`
	Role       model.CrewMemberRole       `json:"role"`
	Name       string                     `json:"name"`
	Permission model.CrewMemberPermission `json:"permissionMode"`
	Status     model.MemberRunStatus      `json:"status"`
	Attempt    int                        `json:"attempt"`
	TaskID     string                     `json:"taskId,omitempty"`
	Summary    string                     `json:"summary,omitempty"`
	ErrorCode  string                     `json:"errorCode,omitempty"`
}
type CrewRunView struct {
	Approval          *CrewApprovalView   `json:"approval,omitempty"`
	ID                string              `json:"id"`
	CrewID            string              `json:"crewId"`
	CanvasID          string              `json:"canvasId"`
	CoordinatorRunID  string              `json:"coordinatorRunId"`
	Status            model.CrewRunStatus `json:"status"`
	Revision          int64               `json:"revision"`
	WorkspaceHash     string              `json:"workspaceHash"`
	WorkspaceRevision int64               `json:"workspaceRevision"`
	LatestSequence    int64               `json:"latestSequence"`
	Budget            CrewRunBudget       `json:"budget"`
	Members           []CrewMemberRunView `json:"members"`
	CreatedAt         time.Time           `json:"createdAt"`
}
type crewRunFrozenConfig struct {
	Workspace WorkspaceSnapshot            `json:"workspace"`
	Crew      CrewView                     `json:"crew"`
	Skills    map[string][]cloudAgentSkill `json:"memberSkills"`
}

func crewSystemPrompt(context *CrewMemberRuntime) string {
	if context == nil {
		return ""
	}
	encoded, _ := json.Marshal(context)
	return "\n\nCrew execution contract (server-controlled):\n" + string(encoded) + "\nEach member has its own conversation. Communicate only through structured delegate_task and task_result envelopes. Member transcript is private to its execution. Never write the canvas or submit generation directly; return proposals for Coordinator aggregation and user approval. A Coordinator delegates to the listed members, waits for their structured results and requests one final proposal approval."
}

func crewRunIDFor(userID, key string) string {
	return "cr" + strings.TrimPrefix(cloudAgentID(userID, "crew:"+key), "ag")
}
func crewMemberRunIDFor(runID, memberID string, attempt int) string {
	return "mr" + strings.TrimPrefix(cloudAgentID(runID, fmt.Sprintf("%s:%d", memberID, attempt)), "ag")
}

func (s *Service) GetCrewRun(userID, runID string) (*CrewRunView, error) {
	if userID == "" || runID == "" {
		return nil, BadAuthRequest("用户和 Crew Run ID 必填")
	}
	snapshot, err := s.repo.AgentCrewRunSnapshot(userID, runID)
	if err != nil {
		return nil, crewPublicError(err)
	}
	workspace, err := s.repo.AgentWorkspaceByID(userID, snapshot.Run.WorkspaceID)
	if err != nil {
		return nil, crewPublicError(err)
	}
	if workspace.CanvasID != snapshot.Run.CanvasID {
		return nil, NewAppError(404, "Crew Run 不存在")
	}
	if _, err := s.repo.CanvasProjectForUser(userID, workspace.CanvasID); err != nil {
		return nil, crewPublicError(err)
	}
	view := &CrewRunView{ID: runID, CrewID: snapshot.Run.CrewID, CanvasID: snapshot.Run.CanvasID, CoordinatorRunID: snapshot.Run.CoordinatorRunID, Status: snapshot.Run.Status, Revision: snapshot.Run.Revision, WorkspaceHash: snapshot.Run.WorkspaceHash, WorkspaceRevision: snapshot.Run.WorkspaceRevision, LatestSequence: snapshot.Run.LatestSequence, Members: []CrewMemberRunView{}, CreatedAt: snapshot.Run.CreatedAt}
	if err := json.Unmarshal([]byte(snapshot.Run.BudgetJSON), &view.Budget); err != nil {
		return nil, WrapAppError(500, "Crew 预算记录损坏", err)
	}
	if snapshot.Run.ProposalJSON != "" {
		var proposal CrewProposal
		if err := json.Unmarshal([]byte(snapshot.Run.ProposalJSON), &proposal); err != nil {
			return nil, err
		}
		view.Approval = &CrewApprovalView{ApprovalID: proposal.ApprovalID, SnapshotHash: proposal.SnapshotHash, Summary: proposal.Summary, Preview: proposal.Preview, Decision: proposal.Decision, OperationID: proposal.OperationID}
	}
	for _, row := range snapshot.Members {
		view.Members = append(view.Members, CrewMemberRunView{ID: row.ID, MemberID: row.MemberID, AgentRunID: row.AgentRunID, Role: row.Role, Name: row.Name, Permission: row.Permission, Status: row.Status, Attempt: row.Attempt, TaskID: row.TaskID, Summary: row.Summary, ErrorCode: row.ErrorCode})
	}
	return view, nil
}

func (s *Service) CreateCrewRun(userID, crewID string, input CreateCrewRunInput) (*CrewRunView, error) {
	if strings.TrimSpace(input.Prompt) == "" || !utf8.ValidString(input.Prompt) || len(input.Prompt) > 16000 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 120 || input.MaxConcurrentMembers < 0 || input.MaxConcurrentMembers > 8 {
		return nil, BadAuthRequest("Crew prompt、幂等键或并发限制无效")
	}
	if userID == "" {
		return nil, NewAppError(401, "请先登录")
	}
	raw, _ := json.Marshal(struct {
		CrewID string
		Input  CreateCrewRunInput
	}{crewID, input})
	inputHash := cloudAgentTextDigest(string(raw))
	runID := crewRunIDFor(userID, input.IdempotencyKey)
	if existing, err := s.repo.AgentCrewRunSnapshot(userID, runID); err == nil {
		if existing.Run.InputHash != inputHash {
			return nil, creationConflict("幂等键已用于不同 Crew 请求")
		}
		return s.GetCrewRun(userID, runID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	crew, err := s.GetCrew(userID, crewID)
	if err != nil {
		return nil, err
	}
	if crew.Status != "enabled" {
		return nil, BadAuthRequest("剧组尚未启用")
	}
	workspace, err := s.FreezeWorkspaceSnapshot(userID, crew.CanvasID)
	if err != nil {
		return nil, err
	}
	baseline, err := s.resolveRunSkillsWithLayers(userID, runID, input.SkillIDs, true, workspace.Skills, nil)
	if err != nil {
		return nil, err
	}
	baseLayer, userLayer := []skills.SkillSelectionItem{}, []skills.SkillSelectionItem{}
	for _, item := range skillSelectionItems(baseline) {
		if item.Source == skills.SkillSourceUser {
			userLayer = append(userLayer, item)
		} else {
			baseLayer = append(baseLayer, item)
		}
	}
	frozen := crewRunFrozenConfig{Workspace: workspace, Crew: *crew, Skills: map[string][]cloudAgentSkill{}}
	maxConcurrent := input.MaxConcurrentMembers
	if maxConcurrent == 0 {
		maxConcurrent = 2
	}
	budget := CrewRunBudget{MaxConcurrentMembers: maxConcurrent}
	members := []CrewMemberView{}
	targets := []CrewDelegateTarget{}
	coordinatorID := ""
	for _, member := range crew.Members {
		if !member.Enabled {
			continue
		}
		if err := s.validateCrewMember(userID, crew.CanvasID, member.CrewMemberInput); err != nil {
			return nil, err
		}
		members = append(members, member)
		budget.MaxCredits += member.Budget.MaxCredits
		budget.MaxSteps += member.Budget.MaxSteps
		budget.MaxGenerationTasks += member.Budget.MaxGenerationTasks
		budget.MaxVideoSeconds += member.Budget.MaxVideoSeconds
		if member.Role == model.CrewMemberRoleCoordinator {
			coordinatorID = cloudAgentID(userID, runID+":"+member.ID+":1")
		} else {
			targets = append(targets, CrewDelegateTarget{MemberID: member.ID, Name: member.Name, Permission: member.Permission})
		}
	}
	if coordinatorID == "" {
		return nil, BadAuthRequest("Crew Coordinator 不可用")
	}
	now := time.Now()
	snapshot := model.AgentCrewRunSnapshot{Run: model.AgentCrewRun{ID: runID, UserID: userID, CrewID: crewID, WorkspaceID: workspace.WorkspaceID, CanvasID: crew.CanvasID, CoordinatorRunID: coordinatorID, IdempotencyKey: input.IdempotencyKey, InputHash: inputHash, Status: model.CrewRunQueued, WorkspaceHash: workspace.AgentsMDHash, WorkspaceRevision: workspace.Revision, CreatedAt: now}}
	admissions := []repository.CloudAgentAdmission{}
	for _, member := range members {
		layer := []skills.SkillSelectionItem{}
		for _, selection := range member.Skills {
			if selection.Enabled {
				layer = append(layer, skills.SkillSelectionItem{SkillID: selection.SkillID, VersionID: selection.SkillVersionID, ContentHash: selection.ContentHash, Source: skills.SkillSourceCrewMember})
			}
		}
		resolved, err := skills.ResolveSkillSelection(baseLayer, layer, userLayer)
		if err != nil {
			return nil, err
		}
		selected, err := s.freezeRunSkillSnapshots(userID, resolved)
		if err != nil {
			return nil, err
		}
		frozen.Skills[member.ID] = selected
		memberRunID := crewMemberRunIDFor(runID, member.ID, 1)
		context := &CrewMemberRuntime{CrewRunID: runID, MemberRunID: memberRunID, MemberID: member.ID, Role: member.Role, Permission: member.Permission, Attempt: 1, Budget: member.Budget}
		parentID := ""
		status := model.MemberRunQueued
		if member.Role == model.CrewMemberRoleCoordinator {
			context.Members = targets
		} else {
			parentID = coordinatorID
			status = model.MemberRunWaiting
		}
		request := CloudAgentRequest{CanvasID: crew.CanvasID, Prompt: input.Prompt, Model: member.Model.Model, ChannelID: member.Model.ChannelID, LogicalModelID: member.Model.LogicalModelID, ChannelModelKey: member.Model.ChannelModelKey, PermissionMode: "read_only", ContextScope: []string{"canvas"}, FocusNodeIDs: member.FocusNodeIDs, IdempotencyKey: runID + ":" + member.ID + ":1"}
		request.Budget.MaxCredits = member.Budget.MaxCredits
		request.Budget.MaxSteps = member.Budget.MaxSteps
		var admission repository.CloudAgentAdmission
		_, err = s.createCloudAgentRunScoped(userID, request, "", &cloudAgentRunScope{Workspace: &workspace, Skills: selected, Crew: context, ConversationID: runID + ":" + member.ID + ":1", ParentID: parentID, Prepared: &admission})
		if err != nil {
			return nil, err
		}
		admission.Execution.Title = member.Name
		admissions = append(admissions, admission)
		snapshot.Members = append(snapshot.Members, model.AgentCrewMemberRun{ID: memberRunID, CrewRunID: runID, MemberID: member.ID, AgentRunID: admission.Execution.ID, Role: member.Role, Name: member.Name, Permission: member.Permission, Position: member.Position, Status: status, Attempt: 1})
	}
	snapshotRaw, err := json.Marshal(frozen)
	if err != nil {
		return nil, err
	}
	snapshot.Run.SnapshotJSON = string(snapshotRaw)
	budgetRaw, _ := json.Marshal(budget)
	snapshot.Run.BudgetJSON = string(budgetRaw)
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	s.storageMu.Lock()
	err = s.admitCrewStorage(&snapshot, admissions, policy)
	s.storageMu.Unlock()
	if err != nil {
		if existing, readErr := s.repo.AgentCrewRunSnapshot(userID, runID); readErr == nil && existing.Run.InputHash == inputHash {
			return s.GetCrewRun(userID, runID)
		}
		return nil, err
	}
	return s.GetCrewRun(userID, runID)
}

func (s *Service) admitCrewStorage(snapshot *model.AgentCrewRunSnapshot, admissions []repository.CloudAgentAdmission, policy RuntimePolicySetting) error {
	usage, err := s.repo.UserStorageUsage(snapshot.Run.UserID)
	if err != nil {
		return err
	}
	incoming := int64(len(snapshot.Run.SnapshotJSON))
	for _, item := range admissions {
		incoming += int64(len(item.Task.Prompt) + len(item.Task.InputJSON) + len(item.Execution.StateJSON))
	}
	if err := validateTaskStorageQuotaWithPolicy(usage, incoming, policy.Resource); err != nil {
		return err
	}
	return s.repo.CreateAgentCrewRunBundle(snapshot, admissions, policy.Task.ActiveTaskLimit)
}
