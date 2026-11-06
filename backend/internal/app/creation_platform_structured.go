package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// structuredOperation names platform structured text tasks. They are internal
// model steps of a server-side runtime, so retry from the task center stays
// with the runtime that owns the turn.
const structuredOperation = "agent_structured_step"

// SubmitStructuredText is the trusted Go entry of the structured text contract
// (P1). It is not exposed over HTTP: an Agent runtime calls it after checking
// Session scope and tools. The request becomes the worker's canonical agent
// request, so tool calls and tool results reach the provider as real tool
// messages, and it is admitted, billed and replayed like any platform submit.
func (s *Service) SubmitStructuredText(_ context.Context, caller cp.Caller, req cp.StructuredTextRequest, grantID string) (cp.SubmitReceipt, error) {
	if err := requireCaller(caller); err != nil {
		return cp.SubmitReceipt{}, err
	}
	if err := req.Validate(); err != nil {
		return cp.SubmitReceipt{}, err
	}
	application, err := platformApplication(req.Business.Application)
	if err != nil {
		return cp.SubmitReceipt{}, err
	}
	p := &creationPlatform{s: s}
	grantID = strings.TrimSpace(grantID)
	fingerprint := platformBudgetFingerprint(req.Fingerprint(), grantID)
	key := strings.TrimSpace(req.IdempotencyKey)
	lookup := func() (*cp.SubmitReceipt, error) {
		return p.existingPlatformSubmission(caller.UserID, application, key, fingerprint)
	}
	if replay, err := lookup(); replay != nil || err != nil {
		return derefReceipt(replay), err
	}
	var task *model.Task
	err = func() error {
		taskReq, err := p.structuredTaskRequest(caller.UserID, req)
		if err != nil {
			return err
		}
		var order *model.BillingOrder
		if task, order, err = p.prepareResolvedTask(caller.UserID, taskReq); err != nil {
			return err
		}
		business, _ := json.Marshal(req.Business)
		record := &model.PlatformSubmission{
			UserID: caller.UserID, Application: application, IdempotencyKey: key, Fingerprint: fingerprint, Capability: string(cp.CapabilityText),
			BusinessJSON: string(business), ProjectRef: req.Business.ProjectID, ObjectType: req.Business.ObjectType, ObjectID: req.Business.ObjectID,
			OperationID: req.Business.OperationID, BudgetGrantID: grantID,
		}
		return p.commitPlatformTask(task, order, record)
	}()
	if err != nil {
		replay, lookupErr := replayConcurrentWinner(lookup, err)
		return derefReceipt(replay), lookupErr
	}
	snapshot, err := p.snapshot(caller.UserID, task.ID)
	if err != nil {
		return cp.SubmitReceipt{}, err
	}
	return cp.SubmitReceipt{Snapshot: snapshot}, nil
}

// structuredTaskRequest translates the contract into the worker's canonical
// agent request. The prompt is the last user text, kept only for task lists.
func (p *creationPlatform) structuredTaskRequest(userID string, req cp.StructuredTextRequest) (CreateTaskRequest, error) {
	canonical := canonicalAgentRequest{SystemPrompt: strings.TrimSpace(req.System), ToolChoice: req.ToolChoice}
	if req.ToolChoice == cp.ToolChoiceNone {
		// The canonical request has no "none"; offering no tools is equivalent.
		canonical.ToolChoice = "auto"
	}
	prompt := ""
	for _, message := range req.Messages {
		canonical.Messages = append(canonical.Messages, structuredCanonicalMessages(message)...)
		if message.Role == cp.RoleUser {
			prompt = strings.TrimSpace(message.Text)
		}
	}
	if req.ToolChoice != cp.ToolChoiceNone {
		for _, tool := range req.Tools {
			parameters := map[string]any{"type": "object"}
			if len(tool.Parameters) > 0 {
				if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
					return CreateTaskRequest{}, cp.NewError(cp.CodeInvalidRequest, "工具参数结构无效")
				}
			}
			canonical.Tools = append(canonical.Tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": parameters}})
		}
	}
	if prompt == "" {
		prompt = "Agent 模型步"
	}
	config := map[string]any{}
	if req.Model.LogicalModelID == "" {
		config["channelId"], config["model"] = strings.TrimSpace(req.Model.ChannelID), strings.TrimSpace(req.Model.ModelKey)
	}
	textOptions := map[string]any{"stream": false}
	if req.MaxOutputTokens > 0 {
		textOptions["maxOutputTokens"] = req.MaxOutputTokens
	}
	input := map[string]any{
		"mode": "text", "prompt": prompt, "config": config, "textOptions": textOptions,
		"agentRequests": map[string]any{"canonical": canonical},
		"metadata":      map[string]any{"source": "creation_platform_structured", "application": req.Business.Application},
	}
	if err := p.attachPlatformReferences(userID, req.References, input); err != nil {
		return CreateTaskRequest{}, err
	}
	return CreateTaskRequest{
		Type: "canvas_text", Operation: structuredOperation, Prompt: prompt,
		Model: strings.TrimSpace(req.Model.ModelKey), LogicalModelID: strings.TrimSpace(req.Model.LogicalModelID),
		Input: input, TraceID: req.TraceID,
	}, nil
}

func structuredCanonicalMessages(message cp.StructuredMessage) []map[string]any {
	switch message.Role {
	case cp.RoleTool:
		return []map[string]any{{"role": "tool", "tool_call_id": message.ToolCallID, "content": message.Text}}
	case cp.RoleAssistant:
		out := []map[string]any{}
		if strings.TrimSpace(message.Text) != "" {
			out = append(out, map[string]any{"role": "assistant", "content": message.Text})
		}
		for _, call := range message.ToolCalls {
			arguments := strings.TrimSpace(string(call.Arguments))
			if arguments == "" {
				arguments = "{}"
			}
			out = append(out, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": arguments})
		}
		return out
	}
	return []map[string]any{{"role": message.Role, "content": message.Text}}
}

// StructuredResult reads a structured step: its contract snapshot and, once
// succeeded, the text, tool calls and provider usage.
func (s *Service) StructuredResult(_ context.Context, caller cp.Caller, taskID string) (cp.StructuredTextResult, cp.TaskSnapshot, error) {
	if err := requireCaller(caller); err != nil {
		return cp.StructuredTextResult{}, cp.TaskSnapshot{}, err
	}
	p := &creationPlatform{s: s}
	snapshot, err := p.snapshot(caller.UserID, strings.TrimSpace(taskID))
	if err != nil {
		return cp.StructuredTextResult{}, cp.TaskSnapshot{}, err
	}
	if snapshot.Execution != cp.ExecutionSucceeded {
		return cp.StructuredTextResult{}, snapshot, nil
	}
	task, err := s.repo.TaskForUser(caller.UserID, snapshot.TaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cp.StructuredTextResult{}, cp.TaskSnapshot{}, cp.NewError(cp.CodeNotFound, "任务不存在")
	}
	if err != nil {
		return cp.StructuredTextResult{}, cp.TaskSnapshot{}, err
	}
	result, err := structuredResultOf(task.ResultJSON)
	return result, snapshot, err
}

func structuredResultOf(raw string) (cp.StructuredTextResult, error) {
	var output struct {
		Text      string `json:"text"`
		Reasoning string `json:"reasoning"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments any    `json:"arguments"`
			} `json:"function"`
		} `json:"toolCalls"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		return cp.StructuredTextResult{}, err
	}
	result := cp.StructuredTextResult{Text: output.Text, Reasoning: output.Reasoning, ToolCalls: []cp.ToolCall{}}
	for _, call := range output.ToolCalls {
		arguments := json.RawMessage("{}")
		switch value := call.Function.Arguments.(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				arguments = json.RawMessage(value)
			}
		case nil:
		default:
			encoded, _ := json.Marshal(value)
			arguments = encoded
		}
		if !json.Valid(arguments) {
			arguments = json.RawMessage("{}")
		}
		result.ToolCalls = append(result.ToolCalls, cp.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	if output.Usage != nil {
		result.Usage = cp.StructuredUsage{InputTokens: int64(intValue(output.Usage["input"])), OutputTokens: int64(intValue(output.Usage["output"])), Available: true}
	}
	return result, nil
}
