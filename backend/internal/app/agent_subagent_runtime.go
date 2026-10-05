package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const (
	maxDynamicSubagents              = 4
	maxDynamicSubagentDepth          = 1
	maxDynamicSubagentNameRunes      = 80
	maxDynamicSubagentRoleRunes      = 80
	maxDynamicSubagentObjectiveRunes = 4000
)

type SubagentRuntime struct {
	LinkID      string `json:"linkId"`
	ParentRunID string `json:"parentRunId"`
	ChildRunID  string `json:"childRunId"`
	DisplayName string `json:"displayName"`
	RoleLabel   string `json:"roleLabel"`
	Objective   string `json:"objective"`
	Depth       int    `json:"depth"`
}

func subagentSystemPrompt(runtime *SubagentRuntime) string {
	if runtime == nil {
		return ""
	}
	return fmt.Sprintf("\n\n动态子代理身份：你是 %s，角色标签为 %s。父 Agent 目标：%s。你只能通过 send_parent_message 和 finish_subagent 向父 Agent 回报，不能创建子代理或直接写入画布。", runtime.DisplayName, runtime.RoleLabel, runtime.Objective)
}

type spawnSubagentInput struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	Objective    string `json:"objective"`
	Instructions string `json:"instructions"`
	MaxSteps     int    `json:"maxSteps"`
}

type subagentMessageInput struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func (s *Service) appendSubagentMessage(userID string, runtime *SubagentRuntime, direction, kind, text, idempotencyKey string) (*model.AgentSubagentMessage, error) {
	if runtime == nil || runtime.LinkID == "" {
		return nil, kernel.Forbidden("子代理通信身份缺失")
	}
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 4000 {
		return nil, BadAuthRequest("子代理消息不能为空且不能超过 4000 字符")
	}
	seq, err := s.repo.NextAgentSubagentMessageSequence(runtime.LinkID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{"text": text, "kind": kind})
	if err != nil {
		return nil, err
	}
	message := &model.AgentSubagentMessage{ID: kernel.NewID(), LinkID: runtime.LinkID, UserID: userID, ParentRunID: runtime.ParentRunID, ChildRunID: runtime.ChildRunID, Direction: direction, Kind: kind, Sequence: seq, PayloadJSON: string(payload), IdempotencyKey: idempotencyKey}
	if err := s.repo.AppendAgentSubagentMessage(message); err != nil {
		return nil, err
	}
	return message, nil
}

func (s *Service) deliverParentSubagentMessage(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
	if state == nil || state.Subagent == nil {
		return nil, kernel.Forbidden("仅子代理可以发送父消息")
	}
	var input subagentMessageInput
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		return nil, BadAuthRequest("send_parent_message 参数无效")
	}
	if input.Kind == "" {
		input.Kind = "progress"
	}
	message, err := s.appendSubagentMessage(run.UserID, state.Subagent, "child_to_parent", input.Kind, input.Text, "child-message:"+run.ID+":"+call.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.InterjectCloudAgent(run.UserID, state.Subagent.ParentRunID, message.ID, input.Text); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": message.ID, "accepted": true}, nil
}

func (s *Service) sendSubagentInstruction(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
	if state == nil || state.Subagent != nil || state.Request.SubagentEnabled == false {
		return nil, kernel.Forbidden("仅允许已授权的父 Agent 发送子代理消息")
	}
	var input struct {
		LinkID string `json:"linkId"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		return nil, BadAuthRequest("message_subagent 参数无效")
	}
	link, err := s.repo.AgentSubagentLinkForUser(run.UserID, strings.TrimSpace(input.LinkID))
	if err != nil || link.ParentRunID != run.ID {
		return nil, kernel.NotFound("子代理不存在")
	}
	runtime := &SubagentRuntime{LinkID: link.ID, ParentRunID: link.ParentRunID, ChildRunID: link.ChildRunID}
	message, err := s.appendSubagentMessage(run.UserID, runtime, "parent_to_child", "parent_reply", input.Text, "parent-message:"+run.ID+":"+call.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.InterjectCloudAgent(run.UserID, link.ChildRunID, message.ID, input.Text); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": message.ID, "accepted": true}, nil
}

func (s *Service) finishDynamicSubagent(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
	if state == nil || state.Subagent == nil {
		return nil, kernel.Forbidden("仅子代理可以完成子代理任务")
	}
	var input struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		return nil, BadAuthRequest("finish_subagent 参数无效")
	}
	message, err := s.appendSubagentMessage(run.UserID, state.Subagent, "child_to_parent", "final_result", input.Summary, "child-finish:"+run.ID+":"+call.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.InterjectCloudAgent(run.UserID, state.Subagent.ParentRunID, message.ID, input.Summary); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateAgentSubagentLinkStatus(run.UserID, state.Subagent.LinkID, repository.SubagentStatusCompleted); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": message.ID, "accepted": true}, nil
}

func validateSpawnSubagentInput(input spawnSubagentInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Role = strings.TrimSpace(input.Role)
	input.Objective = strings.TrimSpace(input.Objective)
	if input.Name == "" || input.Role == "" || input.Objective == "" {
		return BadAuthRequest("子代理名称、角色和目标不能为空")
	}
	if len([]rune(input.Name)) > maxDynamicSubagentNameRunes || len([]rune(input.Role)) > maxDynamicSubagentRoleRunes || len([]rune(input.Objective)) > maxDynamicSubagentObjectiveRunes {
		return BadAuthRequest("子代理名称、角色或目标过长")
	}
	if input.MaxSteps < 0 || input.MaxSteps > 20 {
		return BadAuthRequest("子代理 maxSteps 必须在 0 到 20 之间")
	}
	return nil
}

func (s *Service) spawnDynamicSubagent(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
	if run == nil || state == nil || !state.Request.SubagentEnabled || state.Subagent != nil || state.Crew != nil {
		return nil, kernel.Forbidden("当前运行没有父 Agent 子代理权限")
	}
	var input spawnSubagentInput
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		return nil, BadAuthRequest("spawn_subagent 参数无效")
	}
	if err := validateSpawnSubagentInput(input); err != nil {
		return nil, err
	}
	key := "subagent:" + run.ID + ":" + call.ID
	if existing, err := s.repo.AgentSubagentLinkByIdempotency(run.UserID, key); err == nil {
		return map[string]any{"linkId": existing.ID, "childRunId": existing.ChildRunID, "displayName": existing.DisplayName, "role": existing.RoleLabel, "accepted": true, "replayed": true}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if state.Workspace == nil {
		return nil, BadAuthRequest("父 Agent Workspace 快照缺失")
	}
	active, err := s.repo.AgentSubagentLinkByParent(run.UserID, run.ID, []string{repository.SubagentStatusQueued, repository.SubagentStatusRunning})
	if err != nil {
		return nil, err
	}
	if len(active) >= maxDynamicSubagents {
		return nil, BadAuthRequest(fmt.Sprintf("单个父 Agent 最多同时创建 %d 个子代理", maxDynamicSubagents))
	}
	childReq := state.Request
	childReq.Prompt = strings.TrimSpace(input.Instructions)
	if childReq.Prompt == "" {
		childReq.Prompt = input.Objective
	}
	childReq.IdempotencyKey = key
	childReq.SubagentEnabled = false
	childReq.PermissionMode = "read_only"
	childReq.Budget.MaxSteps = input.MaxSteps
	childID := cloudAgentID(run.UserID, key)
	linkID := kernel.NewID()
	runtime := &SubagentRuntime{LinkID: linkID, ParentRunID: run.ID, ChildRunID: childID, DisplayName: strings.TrimSpace(input.Name), RoleLabel: strings.TrimSpace(input.Role), Objective: strings.TrimSpace(input.Objective), Depth: maxDynamicSubagentDepth}
	prepared := repository.CloudAgentAdmission{}
	child, err := s.createCloudAgentRunScoped(run.UserID, childReq, "", &cloudAgentRunScope{Workspace: state.Workspace, Skills: state.Skills, ConversationID: run.ID + ":subagent:" + call.ID, ParentID: run.ID, Subagent: runtime, Prepared: &prepared})
	if err != nil {
		return nil, err
	}
	runtime.ChildRunID = child.ID
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	if err := s.createCloudAgentRunWithinStorageQuota(prepared.Execution, prepared.Task, prepared.Order, policy, prepared.Skills); err != nil {
		return nil, err
	}
	link := &model.AgentSubagentLink{ID: linkID, UserID: run.UserID, CanvasID: run.CanvasID, ParentRunID: run.ID, ChildRunID: child.ID, IdempotencyKey: key, DisplayName: runtime.DisplayName, RoleLabel: runtime.RoleLabel, Objective: runtime.Objective, Instructions: childReq.Prompt, Depth: runtime.Depth, Status: repository.SubagentStatusQueued}
	if err := s.repo.CreateAgentSubagentLink(link); err != nil {
		return nil, err
	}
	if link.ID != runtime.LinkID {
		return nil, BadAuthRequest("子代理 Link 身份不一致")
	}
	return map[string]any{"linkId": link.ID, "childRunId": child.ID, "displayName": link.DisplayName, "role": link.RoleLabel, "objective": link.Objective, "accepted": true}, nil
}
