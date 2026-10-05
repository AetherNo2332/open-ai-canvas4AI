package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const (
	maxDynamicSubagents              = 4
	maxDynamicSubagentConcurrent     = 3
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

func appendSubagentMessage(repo *repository.Repository, userID string, runtime *SubagentRuntime, direction, kind, text, idempotencyKey string) (*model.AgentSubagentMessage, error) {
	if runtime == nil || runtime.LinkID == "" {
		return nil, kernel.Forbidden("子代理通信身份缺失")
	}
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) > 4000 {
		return nil, BadAuthRequest("子代理消息不能为空且不能超过 4000 字符")
	}
	switch kind {
	case "progress", "question", "partial_result", "final_result", "error", "cancel", "parent_reply":
	default:
		return nil, BadAuthRequest("子代理消息类型无效")
	}
	payload, err := json.Marshal(map[string]string{"text": text, "kind": kind})
	if err != nil {
		return nil, err
	}
	message := &model.AgentSubagentMessage{ID: kernel.NewID(), LinkID: runtime.LinkID, UserID: userID, ParentRunID: runtime.ParentRunID, ChildRunID: runtime.ChildRunID, Direction: direction, Kind: kind, PayloadJSON: string(payload), IdempotencyKey: idempotencyKey}
	if err := repo.AppendAgentSubagentMessage(message); err != nil {
		return nil, err
	}
	return message, nil
}

func deliverSubagentMessage(repo *repository.Repository, userID, receiverID string, runtime *SubagentRuntime, message *model.AgentSubagentMessage, text string) error {
	if message.AcknowledgedAt != nil {
		return nil
	}
	parent, err := repo.CloudAgent(userID, receiverID)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecode(parent)
	if err != nil {
		return err
	}
	if !cloudAgentAcceptsInterjection(parent.Status) {
		return creationConflict("接收运行已结束")
	}
	return repo.MutateCloudAgent(userID, receiverID, parent.Revision, func(current *model.CloudAgentExecution, txRepo *repository.Repository) error {
		label := fmt.Sprintf("%s（%s，%s）", runtime.DisplayName, runtime.RoleLabel, message.Kind)
		if message.Direction == "parent_to_child" {
			label = "父 Agent"
		}
		state.PendingInterjections = append(state.PendingInterjections, cloudAgentInterjection{ID: message.ID, Text: label + "：" + strings.TrimSpace(text), Source: "subagent", CreatedAt: time.Now()})
		state.event(receiverID, "subagent_message", map[string]any{"messageId": message.ID, "linkId": message.LinkID, "childRunId": message.ChildRunID, "displayName": runtime.DisplayName, "roleLabel": runtime.RoleLabel, "kind": message.Kind, "text": strings.TrimSpace(text), "sequence": message.Sequence})
		if err := cloudAgentSave(current, &state); err != nil {
			return err
		}
		return txRepo.AcknowledgeAgentSubagentMessage(userID, message.ID)
	})
}

func (s *Service) deliverParentSubagentMessage(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
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
	message, err := appendSubagentMessage(repo, run.UserID, state.Subagent, "child_to_parent", input.Kind, input.Text, "child-message:"+run.ID+":"+call.ID)
	if err != nil {
		return nil, err
	}
	if err := deliverSubagentMessage(repo, run.UserID, state.Subagent.ParentRunID, state.Subagent, message, input.Text); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": message.ID, "accepted": true}, nil
}

func (s *Service) sendSubagentInstruction(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
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
	link, err := repo.AgentSubagentLinkForUser(run.UserID, strings.TrimSpace(input.LinkID))
	if err != nil || link.ParentRunID != run.ID {
		return nil, kernel.NotFound("子代理不存在")
	}
	runtime := &SubagentRuntime{LinkID: link.ID, ParentRunID: link.ParentRunID, ChildRunID: link.ChildRunID, DisplayName: link.DisplayName, RoleLabel: link.RoleLabel}
	message, err := appendSubagentMessage(repo, run.UserID, runtime, "parent_to_child", "parent_reply", input.Text, "parent-message:"+run.ID+":"+call.ID)
	if err != nil {
		return nil, err
	}
	if err := deliverSubagentMessage(repo, run.UserID, link.ChildRunID, runtime, message, input.Text); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": message.ID, "accepted": true}, nil
}

func (s *Service) finishDynamicSubagent(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
	if state == nil || state.Subagent == nil {
		return nil, kernel.Forbidden("仅子代理可以完成子代理任务")
	}
	var input struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		return nil, BadAuthRequest("finish_subagent 参数无效")
	}
	message, err := appendSubagentMessage(repo, run.UserID, state.Subagent, "child_to_parent", "final_result", input.Summary, "child-finish:"+run.ID+":"+call.ID)
	if err != nil {
		return nil, err
	}
	if err := deliverSubagentMessage(repo, run.UserID, state.Subagent.ParentRunID, state.Subagent, message, input.Summary); err != nil {
		return nil, err
	}
	if err := repo.UpdateAgentSubagentLinkStatus(run.UserID, state.Subagent.LinkID, repository.SubagentStatusCompleted); err != nil {
		return nil, err
	}
	return map[string]any{"messageId": message.ID, "accepted": true}, nil
}

func (s *Service) cancelDynamicSubagents(ctx context.Context, userID, parentRunID string) error {
	links, err := s.repo.AgentSubagentLinkByParent(userID, parentRunID, []string{repository.SubagentStatusQueued, repository.SubagentStatusRunning})
	if err != nil {
		return err
	}
	for _, link := range links {
		if err := s.CancelCloudAgent(ctx, userID, link.ChildRunID); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := s.repo.UpdateAgentSubagentLinkStatus(userID, link.ID, repository.SubagentStatusCancelled); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ReconcileDynamicSubagents(ctx context.Context) error {
	return s.reconcileDynamicSubagents(ctx, "", "")
}

func (s *Service) reconcileDynamicSubagents(ctx context.Context, userID, parentRunID string) error {
	links, err := s.repo.UnsettledAgentSubagentLinks(userID, parentRunID)
	if err != nil {
		return err
	}
	for _, link := range links {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent, err := s.repo.CloudAgent(link.UserID, link.ParentRunID)
		if err != nil {
			return err
		}
		child, err := s.repo.CloudAgent(link.UserID, link.ChildRunID)
		if err != nil {
			return err
		}
		if cloudAgentRunTerminal(parent.Status) && !cloudAgentRunTerminal(child.Status) {
			if err := s.CancelCloudAgent(ctx, link.UserID, child.ID); err != nil {
				return err
			}
			continue
		}
		if !cloudAgentRunTerminal(child.Status) {
			continue
		}
		if err := s.repo.WithAgentSubagentFamily(link.UserID, parent.ID, child.ID, func(repo *repository.Repository) error {
			current, err := repo.AgentSubagentLinkForUser(link.UserID, link.ID)
			if err != nil {
				return err
			}
			if cloudAgentRunTerminal(current.Status) {
				return nil
			}
			runtime := &SubagentRuntime{LinkID: link.ID, ParentRunID: parent.ID, ChildRunID: child.ID, DisplayName: link.DisplayName, RoleLabel: link.RoleLabel}
			kind, text := "error", child.FailureMessage
			if text == "" {
				text = "子代理未提交结构化结果，已停止"
			}
			if child.Status == "cancelled" {
				kind, text = "cancel", "子代理已取消"
			}
			message, err := appendSubagentMessage(repo, link.UserID, runtime, "child_to_parent", kind, text, "terminal:"+child.ID)
			if err != nil {
				return err
			}
			latestParent, err := repo.CloudAgent(link.UserID, parent.ID)
			if err != nil {
				return err
			}
			if !cloudAgentRunTerminal(latestParent.Status) {
				if err := deliverSubagentMessage(repo, link.UserID, parent.ID, runtime, message, text); err != nil {
					return err
				}
			}
			return repo.UpdateAgentSubagentLinkStatus(link.UserID, link.ID, child.Status)
		}); err != nil {
			return err
		}
	}
	return nil
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
	if len([]rune(input.Instructions)) > maxDynamicSubagentObjectiveRunes || strings.ContainsRune(input.Instructions, 0) {
		return BadAuthRequest("子代理指令过长或包含无效字符")
	}
	if input.MaxSteps < 0 || input.MaxSteps > 20 {
		return BadAuthRequest("子代理 maxSteps 必须在 0 到 20 之间")
	}
	return nil
}

func (s *Service) spawnDynamicSubagent(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (map[string]any, error) {
	if run == nil || state == nil || !state.Request.SubagentEnabled || state.Subagent != nil {
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
	views, err := agentSubagentViews(s.repo, run.UserID, run.ID)
	if err != nil {
		return nil, err
	}
	active := 0
	for _, view := range views {
		if !cloudAgentRunTerminal(view.Status) {
			active++
		}
	}
	if len(views) >= maxDynamicSubagents {
		return nil, BadAuthRequest(fmt.Sprintf("单个父 Agent 最多创建 %d 个子代理", maxDynamicSubagents))
	}
	if active >= maxDynamicSubagentConcurrent {
		return nil, BadAuthRequest(fmt.Sprintf("单个父 Agent 最多同时运行 %d 个子代理", maxDynamicSubagentConcurrent))
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
	if childReq.Budget.MaxSteps == 0 {
		childReq.Budget.MaxSteps = 20
	}
	childID := cloudAgentID(run.UserID, key)
	linkID := kernel.NewID()
	runtime := &SubagentRuntime{LinkID: linkID, ParentRunID: run.ID, ChildRunID: childID, DisplayName: strings.TrimSpace(input.Name), RoleLabel: strings.TrimSpace(input.Role), Objective: strings.TrimSpace(input.Objective), Depth: maxDynamicSubagentDepth}
	prepared := repository.CloudAgentAdmission{}
	child, err := s.createCloudAgentRunScoped(run.UserID, childReq, "", &cloudAgentRunScope{Workspace: state.Workspace, Skills: state.Skills, ConversationID: childID, ParentID: run.ID, Subagent: runtime, Prepared: &prepared})
	if err != nil {
		return nil, err
	}
	if child.ID != childID {
		return nil, BadAuthRequest("子代理运行身份生成不一致")
	}
	runtime.ChildRunID = child.ID
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	budgetSnapshot, err := json.Marshal(childReq.Budget)
	if err != nil {
		return nil, err
	}
	contextSnapshot, err := json.Marshal(map[string]any{"workspace": state.Workspace, "skills": state.Skills})
	if err != nil {
		return nil, err
	}
	link := &model.AgentSubagentLink{ID: linkID, UserID: run.UserID, CanvasID: run.CanvasID, ParentRunID: run.ID, ChildRunID: child.ID, TaskID: prepared.Task.ID, IdempotencyKey: key, DisplayName: runtime.DisplayName, RoleLabel: runtime.RoleLabel, Objective: runtime.Objective, Instructions: childReq.Prompt, Depth: runtime.Depth, BudgetJSON: string(budgetSnapshot), ContextSnapshotJSON: string(contextSnapshot), Status: repository.SubagentStatusQueued}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	usage, err := s.repo.UserStorageUsage(run.UserID)
	if err != nil {
		return nil, err
	}
	incomingBytes := int64(len(prepared.Task.Prompt) + len(prepared.Task.InputJSON) + len(prepared.Task.Error))
	if err := validateTaskStorageQuotaWithPolicy(usage, incomingBytes, policy.Resource); err != nil {
		return nil, err
	}
	if err := s.repo.CreateDynamicSubagentAdmission(prepared, link, policy.Task.ActiveTaskLimit, maxDynamicSubagents, maxDynamicSubagentConcurrent); err != nil {
		if existing, replayErr := s.repo.AgentSubagentLinkByIdempotency(run.UserID, key); replayErr == nil {
			return map[string]any{"linkId": existing.ID, "childRunId": existing.ChildRunID, "displayName": existing.DisplayName, "role": existing.RoleLabel, "accepted": true, "replayed": true}, nil
		}
		if errors.Is(err, repository.ErrTaskStateConflict) {
			return nil, creationConflict("子代理数量或并发上限已达到")
		}
		if errors.Is(err, repository.ErrCreationConflict) {
			return nil, creationConflict("父运行已结束或状态变化，不能创建子代理")
		}
		return nil, err
	}
	if link.ID != runtime.LinkID {
		return nil, BadAuthRequest("子代理 Link 身份不一致")
	}
	return map[string]any{"linkId": link.ID, "childRunId": child.ID, "displayName": link.DisplayName, "role": link.RoleLabel, "objective": link.Objective, "accepted": true}, nil
}
