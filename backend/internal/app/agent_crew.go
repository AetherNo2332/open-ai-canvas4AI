package app

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/skills"
)

type CrewModelConfig struct {
	Model           string `json:"model"`
	LogicalModelID  string `json:"logicalModelId,omitempty"`
	ChannelID       string `json:"channelId,omitempty"`
	ChannelModelKey string `json:"channelModelKey,omitempty"`
}
type CrewMemberBudget struct {
	MaxCredits         float64 `json:"maxCredits"`
	MaxSteps           int     `json:"maxSteps"`
	MaxGenerationTasks int     `json:"maxGenerationTasks"`
	MaxVideoSeconds    int     `json:"maxVideoSeconds"`
}
type CrewMemberInput struct {
	Name         string                     `json:"name"`
	Role         model.CrewMemberRole       `json:"role"`
	Model        CrewModelConfig            `json:"modelConfig"`
	Permission   model.CrewMemberPermission `json:"permissionMode"`
	FocusNodeIDs []string                   `json:"focusNodeIds"`
	Budget       CrewMemberBudget           `json:"budget"`
	Enabled      bool                       `json:"enabled"`
	Position     int                        `json:"position"`
}
type CreateCrewInput struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Status      string            `json:"status"`
	Members     []CrewMemberInput `json:"members"`
}
type UpdateCrewInput struct {
	Name                string `json:"name"`
	Description         string `json:"description"`
	Status              string `json:"status"`
	CoordinatorMemberID string `json:"coordinatorMemberId,omitempty"`
}
type CrewMemberSkillView struct {
	SkillSelection
	SkillName   string `json:"skillName"`
	ContentHash string `json:"contentHash"`
	FileCount   int    `json:"fileCount"`
	TotalBytes  int64  `json:"totalBytes"`
	Source      string `json:"source"`
}
type CrewMemberView struct {
	ID string `json:"id"`
	CrewMemberInput
	Skills []CrewMemberSkillView `json:"skills"`
}
type CrewView struct {
	ID                  string           `json:"id"`
	WorkspaceID         string           `json:"workspaceId"`
	CanvasID            string           `json:"canvasId"`
	Name                string           `json:"name"`
	Description         string           `json:"description"`
	Status              string           `json:"status"`
	CoordinatorMemberID string           `json:"coordinatorMemberId"`
	Revision            int64            `json:"revision"`
	Members             []CrewMemberView `json:"members"`
}

func crewPublicError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NewAppError(404, "剧组或画布不存在")
	}
	return err
}

func (s *Service) crewSnapshotForUser(userID, crewID string) (*model.AgentCrewSnapshot, *model.AgentWorkspace, error) {
	if userID == "" || crewID == "" {
		return nil, nil, BadAuthRequest("用户和剧组 ID 必填")
	}
	snapshot, err := s.repo.AgentCrewSnapshot(userID, crewID)
	if err != nil {
		return nil, nil, crewPublicError(err)
	}
	workspace, err := s.repo.AgentWorkspaceByID(userID, snapshot.Crew.WorkspaceID)
	if err != nil {
		return nil, nil, crewPublicError(err)
	}
	if _, err := s.repo.CanvasProjectForUser(userID, workspace.CanvasID); err != nil {
		return nil, nil, crewPublicError(err)
	}
	return snapshot, workspace, nil
}

func crewMemberInput(row model.AgentCrewMember) (CrewMemberInput, error) {
	input := CrewMemberInput{Name: row.Name, Role: row.Role, Permission: row.PermissionMode, Enabled: row.Enabled, Position: row.Position, FocusNodeIDs: []string{}}
	for _, part := range []struct {
		raw    string
		target any
	}{{row.ModelConfigJSON, &input.Model}, {row.BudgetJSON, &input.Budget}, {row.FocusNodeIDsJSON, &input.FocusNodeIDs}} {
		if err := json.Unmarshal([]byte(part.raw), part.target); err != nil {
			return input, WrapAppError(500, "剧组成员配置损坏", err)
		}
	}
	return input, nil
}

func (s *Service) GetCrew(userID, crewID string) (*CrewView, error) {
	snapshot, workspace, err := s.crewSnapshotForUser(userID, crewID)
	if err != nil {
		return nil, err
	}
	view := &CrewView{ID: snapshot.Crew.ID, WorkspaceID: workspace.ID, CanvasID: workspace.CanvasID, Name: snapshot.Crew.Name, Description: snapshot.Crew.Description, Status: snapshot.Crew.Status, CoordinatorMemberID: snapshot.Crew.CoordinatorMemberID, Revision: snapshot.Crew.Revision, Members: []CrewMemberView{}}
	for _, row := range snapshot.Members {
		input, err := crewMemberInput(row)
		if err != nil {
			return nil, err
		}
		member := CrewMemberView{ID: row.ID, CrewMemberInput: input, Skills: []CrewMemberSkillView{}}
		for _, binding := range snapshot.Skills {
			if binding.MemberID != row.ID {
				continue
			}
			skill, err := s.repo.Skill(binding.SkillID)
			if err != nil {
				return nil, err
			}
			version, err := s.repo.SkillVersion(binding.SkillVersionID)
			if err != nil {
				return nil, err
			}
			member.Skills = append(member.Skills, CrewMemberSkillView{SkillSelection: SkillSelection{SkillID: binding.SkillID, SkillVersionID: binding.SkillVersionID, Position: binding.Position, Enabled: binding.Enabled}, SkillName: skill.Name, ContentHash: version.ContentHash, FileCount: version.FileCount, TotalBytes: version.TotalBytes, Source: "crew_member"})
		}
		view.Members = append(view.Members, member)
	}
	return view, nil
}

func (s *Service) ListCrews(userID, canvasID string) ([]CrewView, error) {
	workspace, err := s.GetAgentWorkspace(userID, canvasID)
	if err != nil {
		return nil, err
	}
	ids, err := s.repo.AgentCrewIDs(userID, workspace.WorkspaceID)
	if err != nil {
		return nil, err
	}
	views := make([]CrewView, 0, len(ids))
	for _, id := range ids {
		view, err := s.GetCrew(userID, id)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func validateCrewIdentity(name, description, status string) error {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || len(name) > 120 || !utf8.ValidString(description) || len(description) > 4096 || (status != "enabled" && status != "disabled") {
		return BadAuthRequest("剧组名称、描述或状态无效")
	}
	return nil
}

func (s *Service) validateCrewMember(userID, canvasID string, input CrewMemberInput) error {
	if strings.TrimSpace(input.Name) == "" || !utf8.ValidString(input.Name) || len(input.Name) > 120 || input.Position < 0 || input.Position > 1024 || (input.Role != model.CrewMemberRoleMember && input.Role != model.CrewMemberRoleCoordinator) {
		return BadAuthRequest("成员名称、角色或排序无效")
	}
	if input.Permission != model.CrewPermissionReadOnly && input.Permission != model.CrewPermissionPropose {
		return BadAuthRequest("成员只允许 read_only 或 propose；write 仅在审批提交时授予")
	}
	if math.IsNaN(input.Budget.MaxCredits) || math.IsInf(input.Budget.MaxCredits, 0) || input.Budget.MaxCredits <= 0 || input.Budget.MaxCredits > 100000 || input.Budget.MaxSteps < 0 || input.Budget.MaxSteps > cloudAgentMaxStepsLimit || input.Budget.MaxGenerationTasks < 0 || input.Budget.MaxGenerationTasks > 100 || input.Budget.MaxVideoSeconds < 0 || input.Budget.MaxVideoSeconds > 86400 {
		return BadAuthRequest("成员预算超出允许范围")
	}
	if strings.TrimSpace(input.Model.Model) == "" || len(input.Model.Model) > 200 || len(input.Model.LogicalModelID) > 80 || len(input.Model.ChannelID) > 80 || len(input.Model.ChannelModelKey) > 200 || (input.Model.LogicalModelID != "" && input.Model.ChannelID != "") || (input.Model.ChannelModelKey != "" && input.Model.ChannelID == "") {
		return BadAuthRequest("成员模型配置无效")
	}
	if len(input.FocusNodeIDs) > 8 {
		return BadAuthRequest("最多选择 8 个焦点节点")
	}
	if len(input.FocusNodeIDs) > 0 {
		canvas, err := s.repo.CanvasProjectForUser(userID, canvasID)
		if err != nil {
			return crewPublicError(err)
		}
		var payload struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal([]byte(canvas.PayloadJSON), &payload); err != nil {
			return BadAuthRequest("画布内容无法解析")
		}
		known := map[string]bool{}
		for _, node := range payload.Nodes {
			known[node.ID] = true
		}
		seen := map[string]bool{}
		for _, id := range input.FocusNodeIDs {
			if !known[id] || seen[id] || id == "" {
				return BadAuthRequest("焦点节点不存在或重复")
			}
			seen[id] = true
		}
	}
	return nil
}

func validateCrewAggregate(snapshot *model.AgentCrewSnapshot) error {
	if err := validateCrewIdentity(snapshot.Crew.Name, snapshot.Crew.Description, snapshot.Crew.Status); err != nil {
		return err
	}
	if len(snapshot.Members) > 16 {
		return BadAuthRequest("一个剧组最多 16 名成员")
	}
	positions := map[int]bool{}
	coordinators := 0
	coordinatorID := ""
	for _, member := range snapshot.Members {
		if positions[member.Position] {
			return BadAuthRequest("成员排序重复")
		}
		positions[member.Position] = true
		if member.Role == model.CrewMemberRoleCoordinator && member.Enabled {
			coordinators++
			coordinatorID = member.ID
		}
	}
	if coordinators > 1 || (snapshot.Crew.Status == "enabled" && coordinators != 1) {
		return BadAuthRequest("启用的剧组须有且仅有一名启用的 Coordinator")
	}
	snapshot.Crew.CoordinatorMemberID = coordinatorID
	return nil
}

func newCrewMember(crewID, id string, input CrewMemberInput) model.AgentCrewMember {
	focus := input.FocusNodeIDs
	if focus == nil {
		focus = []string{}
	}
	modelJSON, _ := json.Marshal(input.Model)
	budgetJSON, _ := json.Marshal(input.Budget)
	focusJSON, _ := json.Marshal(focus)
	return model.AgentCrewMember{ID: id, CrewID: crewID, Name: strings.TrimSpace(input.Name), Role: input.Role, ModelConfigJSON: string(modelJSON), PermissionMode: input.Permission, FocusNodeIDsJSON: string(focusJSON), BudgetJSON: string(budgetJSON), Enabled: input.Enabled, Position: input.Position}
}

func (s *Service) CreateCrew(userID, canvasID string, input CreateCrewInput) (*CrewView, error) {
	workspace, err := s.GetAgentWorkspace(userID, canvasID)
	if err != nil {
		return nil, err
	}
	snapshot := model.AgentCrewSnapshot{Crew: model.AgentCrew{ID: newID(), UserID: userID, WorkspaceID: workspace.WorkspaceID, Name: strings.TrimSpace(input.Name), Description: input.Description, Status: input.Status}}
	for _, member := range input.Members {
		if err := s.validateCrewMember(userID, canvasID, member); err != nil {
			return nil, err
		}
		snapshot.Members = append(snapshot.Members, newCrewMember(snapshot.Crew.ID, newID(), member))
	}
	if err := validateCrewAggregate(&snapshot); err != nil {
		return nil, err
	}
	if err := s.repo.CreateAgentCrew(&snapshot); err != nil {
		return nil, crewPublicError(err)
	}
	return s.GetCrew(userID, snapshot.Crew.ID)
}

func (s *Service) mutateCrew(userID, crewID string, revision int64, mutate func(*model.AgentCrewSnapshot) error) (*CrewView, error) {
	if revision < 0 {
		return nil, BadAuthRequest("revision 不得为负")
	}
	if _, _, err := s.crewSnapshotForUser(userID, crewID); err != nil {
		return nil, err
	}
	_, err := s.repo.MutateAgentCrew(userID, crewID, revision, func(snapshot *model.AgentCrewSnapshot) error {
		if err := mutate(snapshot); err != nil {
			return err
		}
		return validateCrewAggregate(snapshot)
	})
	if err != nil {
		return nil, crewPublicError(err)
	}
	return s.GetCrew(userID, crewID)
}

func (s *Service) UpdateCrew(userID, crewID string, revision int64, input UpdateCrewInput) (*CrewView, error) {
	return s.mutateCrew(userID, crewID, revision, func(snapshot *model.AgentCrewSnapshot) error {
		snapshot.Crew.Name = strings.TrimSpace(input.Name)
		snapshot.Crew.Description = input.Description
		snapshot.Crew.Status = input.Status
		if input.CoordinatorMemberID != "" {
			found := false
			for i := range snapshot.Members {
				member := &snapshot.Members[i]
				if member.ID == input.CoordinatorMemberID {
					if !member.Enabled {
						return BadAuthRequest("Coordinator 必须启用")
					}
					member.Role = model.CrewMemberRoleCoordinator
					found = true
				} else {
					member.Role = model.CrewMemberRoleMember
				}
			}
			if !found {
				return BadAuthRequest("Coordinator 不属于此剧组")
			}
		}
		return nil
	})
}

func (s *Service) AddCrewMember(userID, crewID string, revision int64, input CrewMemberInput) (*CrewView, error) {
	_, workspace, err := s.crewSnapshotForUser(userID, crewID)
	if err != nil {
		return nil, err
	}
	if err := s.validateCrewMember(userID, workspace.CanvasID, input); err != nil {
		return nil, err
	}
	return s.mutateCrew(userID, crewID, revision, func(snapshot *model.AgentCrewSnapshot) error {
		snapshot.Members = append(snapshot.Members, newCrewMember(crewID, newID(), input))
		return nil
	})
}

func (s *Service) memberCrewForUser(userID, memberID string) (string, *model.AgentWorkspace, error) {
	crewID, err := s.repo.AgentCrewIDForMember(userID, memberID)
	if err != nil {
		return "", nil, crewPublicError(err)
	}
	_, workspace, err := s.crewSnapshotForUser(userID, crewID)
	return crewID, workspace, err
}

func (s *Service) UpdateCrewMember(userID, memberID string, revision int64, input CrewMemberInput) (*CrewView, error) {
	crewID, workspace, err := s.memberCrewForUser(userID, memberID)
	if err != nil {
		return nil, err
	}
	if err := s.validateCrewMember(userID, workspace.CanvasID, input); err != nil {
		return nil, err
	}
	return s.mutateCrew(userID, crewID, revision, func(snapshot *model.AgentCrewSnapshot) error {
		for i, row := range snapshot.Members {
			if row.ID == memberID {
				updated := newCrewMember(crewID, memberID, input)
				updated.CreatedAt = row.CreatedAt
				snapshot.Members[i] = updated
				return nil
			}
		}
		return NewAppError(404, "成员不存在")
	})
}

func (s *Service) DeleteCrewMember(userID, memberID string, revision int64) (*CrewView, error) {
	crewID, _, err := s.memberCrewForUser(userID, memberID)
	if err != nil {
		return nil, err
	}
	return s.mutateCrew(userID, crewID, revision, func(snapshot *model.AgentCrewSnapshot) error {
		members := snapshot.Members[:0]
		found := false
		for _, row := range snapshot.Members {
			if row.ID == memberID {
				found = true
			} else {
				members = append(members, row)
			}
		}
		if !found {
			return NewAppError(404, "成员不存在")
		}
		snapshot.Members = members
		bindings := snapshot.Skills[:0]
		for _, row := range snapshot.Skills {
			if row.MemberID != memberID {
				bindings = append(bindings, row)
			}
		}
		snapshot.Skills = bindings
		return nil
	})
}

func (s *Service) ReplaceCrewMemberSkills(userID, memberID string, revision int64, selections []SkillSelection) (*CrewView, error) {
	crewID, _, err := s.memberCrewForUser(userID, memberID)
	if err != nil {
		return nil, err
	}
	rows := make([]model.AgentCrewMemberSkill, 0, len(selections))
	facts := []skills.SkillCapacityFacts{}
	seen, positions := map[string]bool{}, map[int]bool{}
	for _, item := range selections {
		if item.SkillID == "" || item.SkillVersionID == "" || item.Position < 0 || seen[item.SkillID] || positions[item.Position] {
			return nil, BadAuthRequest("技能引用或排序无效")
		}
		seen[item.SkillID] = true
		positions[item.Position] = true
		row, fact, err := s.validateAgentSkillDefaultItem(AgentSkillDefaultItem{SkillID: item.SkillID, SkillVersionID: item.SkillVersionID, Position: item.Position, Enabled: boolToInt(item.Enabled)})
		if err != nil {
			return nil, err
		}
		rows = append(rows, model.AgentCrewMemberSkill{MemberID: memberID, SkillID: row.SkillID, SkillVersionID: row.SkillVersionID, Position: row.Position, Enabled: row.Enabled})
		if row.Enabled {
			facts = append(facts, fact)
		}
	}
	if err := skills.AdmitSkillCapacity(facts, skills.SkillCapacityBudgets{MaxFiles: skills.SkillRunMaxFiles, MaxTotalBytes: skills.SkillRunMaxTotalBytes, MaxContextBytes: skills.SkillRunMaxContextBytes}); err != nil {
		return nil, err
	}
	return s.mutateCrew(userID, crewID, revision, func(snapshot *model.AgentCrewSnapshot) error {
		exists := false
		for _, member := range snapshot.Members {
			if member.ID == memberID {
				exists = true
			}
		}
		if !exists {
			return NewAppError(404, "成员不存在")
		}
		bindings := snapshot.Skills[:0]
		for _, row := range snapshot.Skills {
			if row.MemberID != memberID {
				bindings = append(bindings, row)
			}
		}
		snapshot.Skills = append(bindings, rows...)
		return nil
	})
}

func (s *Service) DeleteCrew(userID, crewID string, revision int64) (int64, error) {
	if revision < 0 {
		return 0, BadAuthRequest("revision 不得为负")
	}
	if _, _, err := s.crewSnapshotForUser(userID, crewID); err != nil {
		return 0, err
	}
	next, err := s.repo.DeleteAgentCrew(userID, crewID, revision)
	return next, crewPublicError(err)
}
