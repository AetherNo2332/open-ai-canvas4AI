package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Supervisor tool names. The tools are deterministic work commands: the model
// chooses them, the server resolves every identity (owner, root work, grant)
// from the Session, and nothing the model says can widen the authorization.
const (
	supervisorToolListAgents    = "list_business_agents"
	supervisorToolDelegate      = "delegate_work"
	supervisorToolReadStatus    = "read_work_status"
	supervisorToolAppendInput   = "append_work_input"
	supervisorToolRequestUser   = "request_user_input"
	supervisorToolFinishWork    = "finish_work"
	supervisorToolSessionsList  = "agent_sessions_list"
	supervisorToolSessionOpen   = "agent_session_open"
	supervisorToolSessionSend   = "agent_session_send"
	supervisorToolWorkRead      = "agent_work_read"
	supervisorToolRequestUserV2 = "agent_request_user"
	supervisorWorkInputPrefix   = "supervisor-turn:"
)

// supervisorToolDefinitions is the fixed tool set of a supervisor Session.
func supervisorToolDefinitions() []cp.ToolDefinition {
	return []cp.ToolDefinition{
		{Name: supervisorToolListAgents, Description: "列出本工作已授权业务空间可用的业务 Agent", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: supervisorToolDelegate, Description: "把一个子工作派给业务 Agent：动作键、定义 ID、目标空间、交接说明与子预算（microcredits）",
			Parameters: json.RawMessage(`{"type":"object","properties":{"actionKey":{"type":"string"},"definitionId":{"type":"string"},"application":{"type":"string"},"spaceType":{"type":"string"},"spaceId":{"type":"string"},"handoff":{"type":"string"},"budgetMicrocredits":{"type":"integer"}},"required":["actionKey","definitionId","application","spaceType","spaceId","handoff","budgetMicrocredits"]}`)},
		{Name: supervisorToolReadStatus, Description: "读取本工作树的状态、派工与已应用报告", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: supervisorToolAppendInput, Description: "向已有子工作补充要求：子工作 ID 与正文",
			Parameters: json.RawMessage(`{"type":"object","properties":{"workItemId":{"type":"string"},"text":{"type":"string"}},"required":["workItemId","text"]}`)},
		{Name: supervisorToolRequestUser, Description: "向用户提出具体问题并把工作置为等待用户",
			Parameters: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`)},
		{Name: supervisorToolFinishWork, Description: "全部子工作完成后汇总结果并结束根工作：结果摘要",
			Parameters: json.RawMessage(`{"type":"object","properties":{"resultSummary":{"type":"string"}},"required":["resultSummary"]}`)},
		{Name: supervisorToolSessionsList, Description: "列出当前总控工作树中可访问的执行会话",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: supervisorToolSessionOpen, Description: "打开当前总控工作树中一个已授权的执行会话",
			Parameters: json.RawMessage(`{"type":"object","properties":{"sessionId":{"type":"string"}},"required":["sessionId"]}`)},
		{Name: supervisorToolSessionSend, Description: "向当前总控工作树中一个已授权的执行会话发送要求",
			Parameters: json.RawMessage(`{"type":"object","properties":{"sessionId":{"type":"string"},"text":{"type":"string"}},"required":["sessionId","text"]}`)},
		{Name: supervisorToolWorkRead, Description: "读取当前总控工作树中一个已授权工作的状态和报告",
			Parameters: json.RawMessage(`{"type":"object","properties":{"workItemId":{"type":"string"},"sessionId":{"type":"string"}}}`)},
		{Name: supervisorToolRequestUserV2, Description: "向用户提出具体问题并暂停当前总控工作",
			Parameters: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`)},
	}
}

// executeSupervisorTool translates one supervisor tool call into a work
// command. The root work, owner and grant come from the Session, never from
// the model; the delegation scope is validated against the work authorization
// by DelegateWork itself.
func (s *Service) executeSupervisorTool(ctx context.Context, session *model.AgentSession, call as.ToolInvocation) (as.ToolResult, error) {
	if session.Role != as.RoleSupervisor || session.WorkItemID == "" {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "总控工具只属于总控会话")
	}
	root, err := s.repo.AgentWorkForOwner(call.OwnerID, session.WorkItemID)
	if err != nil {
		return as.ToolResult{}, workNotFound(err)
	}
	if ao.WorkTerminal(root.Status) && !supervisorReadOnlyTool(call.Name) {
		return as.ToolResult{}, as.NewError(as.CodeWorkStopped, "工作已结束")
	}
	switch call.Name {
	case supervisorToolListAgents:
		return s.supervisorListAgents(root)
	case supervisorToolDelegate:
		return s.supervisorDelegate(call, root)
	case supervisorToolReadStatus:
		return s.supervisorReadStatus(call.OwnerID, root.ID)
	case supervisorToolAppendInput:
		return s.supervisorAppendInput(call, root)
	case supervisorToolRequestUser:
		return s.supervisorRequestUser(call, root)
	case supervisorToolFinishWork:
		return s.supervisorFinishWork(call, root)
	case supervisorToolSessionsList:
		return s.supervisorSessionsList(call.OwnerID, root)
	case supervisorToolSessionOpen:
		return s.supervisorSessionOpen(call, root)
	case supervisorToolSessionSend:
		return s.supervisorSessionSend(call, root)
	case supervisorToolWorkRead:
		return s.supervisorWorkRead(call, root)
	case supervisorToolRequestUserV2:
		return s.supervisorRequestUser(call, root)
	}
	return as.ToolResult{}, as.NewError(as.CodeForbidden, "总控工具不存在")
}

func supervisorReadOnlyTool(name string) bool {
	return name == supervisorToolReadStatus || name == supervisorToolSessionsList || name == supervisorToolSessionOpen || name == supervisorToolWorkRead
}

func (s *Service) supervisorTargetSession(ownerID string, root *model.AgentWorkItem, sessionID string) (*model.AgentSession, *model.AgentWorkItem, error) {
	target, err := s.agentSessionForOwner(ownerID, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, nil, err
	}
	if target.Role != as.RoleBusiness || strings.TrimSpace(target.WorkItemID) == "" {
		return nil, nil, as.NewError(as.CodeForbidden, "目标会话不属于当前总控工作树")
	}
	work, err := s.repo.AgentWorkForOwner(ownerID, target.WorkItemID)
	if err != nil {
		return nil, nil, workNotFound(err)
	}
	if work.RootWorkID != root.RootWorkID || work.ID == root.ID {
		return nil, nil, as.NewError(as.CodeForbidden, "目标会话不属于当前总控工作树")
	}
	if work.ActiveAssignmentID == "" {
		return nil, nil, as.NewError(as.CodeStaleAssignment, "目标会话没有有效派工")
	}
	assignment, err := s.repo.AgentAssignmentForOwner(ownerID, work.ActiveAssignmentID)
	if err != nil || assignment.ExecutorSessionID != target.ID || assignment.Status != ao.AssignmentRunning {
		return nil, nil, as.NewError(as.CodeStaleAssignment, "目标会话的派工已失效")
	}
	return target, work, nil
}

func (s *Service) supervisorSessionsList(ownerID string, root *model.AgentWorkItem) (as.ToolResult, error) {
	var children []model.AgentWorkItem
	if err := s.repo.InAgentWork(ownerID, root.RootWorkID, func(tx *repository.WorkTx) error {
		rows, err := tx.Descendants(ownerID, root.RootWorkID)
		if err != nil {
			return err
		}
		children = rows
		return nil
	}); err != nil {
		return as.ToolResult{}, workNotFound(err)
	}
	lines := make([]string, 0, len(children))
	for _, child := range children {
		if child.ID == root.ID || child.SupervisorSessionID != "" {
			continue
		}
		assignment, err := s.repo.AgentLatestAssignmentForWork(ownerID, child.ID)
		if err != nil || assignment.ExecutorSessionID == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("会话 %s：工作 %s，业务 %s，空间 %s/%s，状态 %s", assignment.ExecutorSessionID, child.ID, child.Application, child.SpaceType, child.SpaceID, child.Status))
	}
	if len(lines) == 0 {
		return as.ToolResult{Text: "当前工作树没有可访问的执行会话。"}, nil
	}
	return as.ToolResult{Text: strings.Join(lines, "\n")}, nil
}

func (s *Service) supervisorSessionOpen(call as.ToolInvocation, root *model.AgentWorkItem) (as.ToolResult, error) {
	var args struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.SessionID) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "打开会话需要 sessionId")
	}
	target, work, err := s.supervisorTargetSession(call.OwnerID, root, args.SessionID)
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("会话 %s：工作 %s，业务 %s，空间 %s/%s，状态 %s，目标 %s", target.ID, work.ID, target.Application, target.SpaceType, target.SpaceID, work.Status, work.Goal), Operation: target.ID}, nil
}

func (s *Service) supervisorSessionSend(call as.ToolInvocation, root *model.AgentWorkItem) (as.ToolResult, error) {
	var args struct {
		SessionID string `json:"sessionId"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.SessionID) == "" || strings.TrimSpace(args.Text) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "发送会话消息需要 sessionId 与正文")
	}
	target, work, err := s.supervisorTargetSession(call.OwnerID, root, args.SessionID)
	if err != nil {
		return as.ToolResult{}, err
	}
	source := supervisorWorkInputSource(call)
	receipt, err := s.appendWorkInput(call.OwnerID, work.ID, as.ActorSupervisor, source, strings.TrimSpace(args.Text), source)
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("已向会话 %s（工作 %s）发送要求，工作版本 %d。", target.ID, work.ID, receipt.WorkVersion), Operation: work.ID}, nil
}

func (s *Service) supervisorWorkRead(call as.ToolInvocation, root *model.AgentWorkItem) (as.ToolResult, error) {
	var args struct {
		WorkItemID string `json:"workItemId"`
		SessionID  string `json:"sessionId"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "读取工作参数无效")
	}
	workID := strings.TrimSpace(args.WorkItemID)
	if strings.TrimSpace(args.SessionID) != "" {
		_, target, err := s.supervisorTargetSession(call.OwnerID, root, args.SessionID)
		if err != nil {
			return as.ToolResult{}, err
		}
		workID = target.ID
	}
	if workID == "" {
		workID = root.ID
	}
	work, err := s.repo.AgentWorkForOwner(call.OwnerID, workID)
	if err != nil || work.RootWorkID != root.RootWorkID {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "目标工作不属于当前总控工作树")
	}
	return s.supervisorReadStatus(call.OwnerID, work.ID)
}

func (s *Service) supervisorListAgents(root *model.AgentWorkItem) (as.ToolResult, error) {
	auth, err := ao.DecodeAuthorization(root.AuthorizationJSON)
	if err != nil {
		return as.ToolResult{}, err
	}
	entries := []string{}
	for _, granted := range auth.Scopes {
		definitions := s.agentRegistry().List(granted.Application)
		for _, def := range definitions {
			if def.Role != as.RoleBusiness {
				continue
			}
			entries = append(entries, fmt.Sprintf("%s（%s，空间 %s）：工具 %s", def.ID, def.Title, granted.SpaceID, strings.Join(def.Tools, "、")))
		}
	}
	if len(entries) == 0 {
		return as.ToolResult{Text: "本工作没有已授权的业务空间。"}, nil
	}
	return as.ToolResult{Text: "已授权业务：\n" + strings.Join(entries, "\n")}, nil
}

type supervisorDelegateArgs struct {
	ActionKey          string `json:"actionKey"`
	DefinitionID       string `json:"definitionId"`
	Application        string `json:"application"`
	SpaceType          string `json:"spaceType"`
	SpaceID            string `json:"spaceId"`
	Handoff            string `json:"handoff"`
	BudgetMicrocredits int64  `json:"budgetMicrocredits"`
}

func (s *Service) supervisorDelegate(call as.ToolInvocation, root *model.AgentWorkItem) (as.ToolResult, error) {
	var args supervisorDelegateArgs
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "派工参数无效")
	}
	assignment, err := s.DelegateWork(call.OwnerID, DelegateRequest{
		WorkItemID: root.ID, SourceMessageID: supervisorWorkInputSource(call), ActionKey: args.ActionKey, DefinitionID: args.DefinitionID,
		Scope:   as.Scope{Application: args.Application, SpaceType: args.SpaceType, SpaceID: args.SpaceID},
		Handoff: args.Handoff, BudgetMicrocredits: args.BudgetMicrocredits,
	})
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("已派工：子工作 %s，执行会话 %s，预算 %d。", assignment.WorkItemID, assignment.ExecutorSessionID, args.BudgetMicrocredits), Operation: assignment.WorkItemID}, nil
}

func (s *Service) supervisorReadStatus(ownerID, rootID string) (as.ToolResult, error) {
	var text string
	err := s.repo.InAgentWork(ownerID, "", func(tx *repository.WorkTx) error {
		root, err := tx.Work(ownerID, rootID)
		if err != nil {
			return err
		}
		children, err := tx.Descendants(ownerID, root.RootWorkID)
		if err != nil {
			return err
		}
		lines := []string{fmt.Sprintf("根工作 %s：状态 %s，目标 %s。", root.ID, root.Status, root.Goal)}
		for _, child := range children {
			line := fmt.Sprintf("子工作 %s（%s）：状态 %s", child.ID, child.Application, child.Status)
			if child.ResultSummary != "" {
				line += "，结果：" + child.ResultSummary
			}
			lines = append(lines, line)
		}
		reports, err := s.ListReports(ownerID, root.ID)
		if err != nil {
			return err
		}
		for _, report := range reports {
			state := "已应用"
			if !report.Applied {
				state = "审计保留"
			}
			lines = append(lines, fmt.Sprintf("报告 %s（%s，%s）：%s", report.ReportID, report.Status, state, report.Summary))
		}
		text = strings.Join(lines, "\n")
		return nil
	})
	if err != nil {
		return as.ToolResult{}, workNotFound(err)
	}
	return as.ToolResult{Text: text}, nil
}

type supervisorAppendInputArgs struct {
	WorkItemID string `json:"workItemId"`
	Text       string `json:"text"`
}

func (s *Service) supervisorAppendInput(call as.ToolInvocation, root *model.AgentWorkItem) (as.ToolResult, error) {
	var args supervisorAppendInputArgs
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Text) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "补充要求需要子工作与正文")
	}
	source := supervisorWorkInputSource(call)
	receipt, err := s.appendWorkInput(call.OwnerID, args.WorkItemID, as.ActorSupervisor, source, args.Text, "supervisor:"+source, root.RootWorkID)
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("已补充到子工作 %s，工作版本 %d。", receipt.WorkItemID, receipt.WorkVersion), Operation: receipt.WorkItemID}, nil
}

func (s *Service) supervisorRequestUser(call as.ToolInvocation, root *model.AgentWorkItem) (as.ToolResult, error) {
	var args struct {
		Question string `json:"question"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Question) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "需要具体问题")
	}
	err := s.repo.InAgentWork(call.OwnerID, root.RootWorkID, func(tx *repository.WorkTx) error {
		current, err := tx.Work(call.OwnerID, root.ID)
		if err != nil {
			return err
		}
		return tx.UpdateWork(call.OwnerID, current.ID, current.Version, map[string]any{"status": ao.WorkWaitingUser})
	})
	if err != nil {
		return as.ToolResult{}, workNotFound(err)
	}
	return as.ToolResult{Text: "已向用户提问，工作进入等待用户状态：" + strings.TrimSpace(args.Question)}, nil
}

func (s *Service) supervisorFinishWork(call as.ToolInvocation, root *model.AgentWorkItem) (as.ToolResult, error) {
	var args struct {
		ResultSummary string `json:"resultSummary"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.ResultSummary) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "需要结果摘要")
	}
	err := s.repo.InAgentWork(call.OwnerID, root.RootWorkID, func(tx *repository.WorkTx) error {
		current, err := tx.Work(call.OwnerID, root.ID)
		if err != nil {
			return err
		}
		children, err := tx.Descendants(call.OwnerID, current.RootWorkID)
		if err != nil {
			return err
		}
		summaries := make([]ao.ChildWorkSummary, 0, len(children))
		for _, child := range children {
			summaries = append(summaries, ao.ChildWorkSummary{ID: child.ID, Status: child.Status})
		}
		if err := ao.CheckRootCompletion(summaries); err != nil {
			return err
		}
		if err := tx.UpdateWork(call.OwnerID, current.ID, current.Version, map[string]any{"status": ao.WorkCompleted, "result_summary": strings.TrimSpace(args.ResultSummary)}); err != nil {
			return err
		}
		grant, err := tx.Work(call.OwnerID, current.ID)
		if err != nil {
			return err
		}
		return tx.CloseBudgets(call.OwnerID, []string{grant.BudgetGrantID}, time.Now())
	})
	if err != nil {
		return as.ToolResult{}, workNotFound(err)
	}
	return as.ToolResult{Text: "工作已完成：" + strings.TrimSpace(args.ResultSummary)}, nil
}

// supervisorWorkInputSource derives the input idempotency key from the tool
// call identity, so a replayed supervisor turn appends the input once.
func supervisorWorkInputSource(call as.ToolInvocation) string {
	if call.OperationID != "" {
		return supervisorWorkInputPrefix + agentApprovalCallIdentity(call)
	}
	return supervisorWorkInputPrefix + call.CallID
}
