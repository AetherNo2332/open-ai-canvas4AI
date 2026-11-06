package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/workbench"
)

// dramaAgentAdapter lets the drama Agent read its project and drive the
// existing production service. Every rule (models, budget, review) stays in
// the drama service; the adapter only translates calls.
type dramaAgentAdapter struct{ s *Service }

func (dramaAgentAdapter) Application() string  { return as.ApplicationDrama }
func (dramaAgentAdapter) SpaceTypes() []string { return []string{as.SpaceDramaProject} }

func (a dramaAgentAdapter) ValidateSpace(_ context.Context, ownerID string, scope as.Scope) error {
	return a.s.validateBuiltinAgentSpace(ownerID, scope)
}

func (dramaAgentAdapter) Tools() []as.ToolSpec {
	return []as.ToolSpec{
		{Name: "drama_summary", Description: "读取当前短剧项目的制作状态、分集与待办", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "drama_set_running", Description: "开始或暂停自动制作；开始前由短剧服务校验模型与预算", Parameters: json.RawMessage(`{"type":"object","properties":{"running":{"type":"boolean"}},"required":["running"]}`), Charges: true},
	}
}

func (a dramaAgentAdapter) Execute(_ context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	switch call.Name {
	case "drama_summary":
		summary, err := a.s.DramaSummary(call.OwnerID, call.Scope.SpaceID)
		if err != nil {
			return as.ToolResult{}, err
		}
		return as.ToolResult{Text: fmt.Sprintf("短剧《%s》：状态 %s，阶段 %s，共 %d 集，%d 个角色/场景设定。", summary.Project.Name, summary.Production.Status, summary.Production.Stage, len(summary.Episodes), summary.CastCount)}, nil
	case "drama_set_running":
		var args struct {
			Running bool `json:"running"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "参数无效")
		}
		summary, err := a.s.SetDramaRunning(call.OwnerID, call.Scope.SpaceID, args.Running)
		if err != nil {
			return as.ToolResult{}, err
		}
		return as.ToolResult{Text: "制作状态：" + summary.Production.Status, Operation: "drama:" + call.Scope.SpaceID}, nil
	}
	return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不存在")
}

// ecommerceAgentAdapter submits workbench generations through the existing
// workbench bridge, keyed by the Session's batch so a replayed call returns
// the same task.
type ecommerceAgentAdapter struct{ s *Service }

func (ecommerceAgentAdapter) Application() string  { return as.ApplicationEcommerce }
func (ecommerceAgentAdapter) SpaceTypes() []string { return []string{as.SpaceWorkbenchBatch} }

func (a ecommerceAgentAdapter) ValidateSpace(_ context.Context, ownerID string, scope as.Scope) error {
	return a.s.validateBuiltinAgentSpace(ownerID, scope)
}

func (ecommerceAgentAdapter) Tools() []as.ToolSpec {
	return []as.ToolSpec{{Name: "ecommerce_generate_image", Description: "在当前电商批次里生成一张商品图", Charges: true,
		Parameters: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"channelId":{"type":"string"},"model":{"type":"string"},"parameters":{"type":"object"},"index":{"type":"integer"},"count":{"type":"integer"}},"required":["prompt","index","count"]}`)}}
}

func (a ecommerceAgentAdapter) Execute(_ context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	if call.Name != "ecommerce_generate_image" {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不存在")
	}
	var args struct {
		Prompt     string         `json:"prompt"`
		ChannelID  string         `json:"channelId"`
		Model      string         `json:"model"`
		Parameters map[string]any `json:"parameters"`
		Index      int            `json:"index"`
		Count      int            `json:"count"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Prompt) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "参数无效")
	}
	req := WorkbenchTaskInput{Kind: WorkbenchKindImage, Prompt: args.Prompt, Model: args.Model, ChannelID: args.ChannelID, Parameters: args.Parameters,
		Batch: &workbench.TaskBatch{ID: call.Scope.SpaceID, Index: args.Index, Count: args.Count}}
	task, err := a.s.createWorkbenchTaskUnderBudget(call.OwnerID, req, call.BudgetGrantID)
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: "已提交生成任务，状态 " + string(task.Status), Operation: task.ID}, nil
}

// agentAdapters is the registry of business adapters; the canvas Agent keeps
// its own executor (R-A0-1) and has no adapter here.
func (s *Service) agentAdapters() *as.AdapterRegistry {
	s.agentAdaptersOnce.Do(func() {
		registry := as.NewAdapterRegistry()
		for _, adapter := range []as.BusinessAdapter{dramaAgentAdapter{s: s}, ecommerceAgentAdapter{s: s}} {
			if err := registry.Register(adapter); err != nil {
				panic(err)
			}
		}
		s.agentBusinesses = registry
	})
	return s.agentBusinesses
}

// registerAgentBusiness connects a new internal business: its adapter and its
// definition. The Session core and supervisor are unchanged.
func (s *Service) registerAgentBusiness(adapter as.BusinessAdapter, def as.Definition) error {
	if def.Application != adapter.Application() {
		return as.NewError(as.CodeInvalidRequest, "定义与适配器的业务不一致")
	}
	if err := s.agentAdapters().Register(adapter); err != nil {
		return err
	}
	return s.agentRegistry().Register(def)
}

// executeBusinessTool runs one tool call after re-checking, for this call,
// that the Session exists for the owner, its scope matches the call, the tool
// belongs to the Session's application, and the space is still the owner's.
func (s *Service) executeBusinessTool(ctx context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	session, err := s.agentSessionForOwner(call.OwnerID, call.SessionID)
	if err != nil {
		return as.ToolResult{}, err
	}
	scope := as.Scope{Application: session.Application, SpaceType: session.SpaceType, SpaceID: session.SpaceID}
	if call.Scope.Normalized() != scope {
		return as.ToolResult{}, as.NewError(as.CodeScopeMismatch, "工具调用的业务范围与会话不一致")
	}
	adapter, ok := s.agentAdapters().Adapter(scope.Application)
	if !ok {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "该业务没有可用工具")
	}
	if _, ok := s.agentAdapters().Tool(scope.Application, call.Name); !ok {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不属于当前业务")
	}
	if err := adapter.ValidateSpace(ctx, call.OwnerID, scope); err != nil {
		return as.ToolResult{}, err
	}
	result, err := adapter.Execute(ctx, call)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return as.ToolResult{}, as.NewError(as.CodeNotFound, "业务对象不存在")
	}
	return result, err
}
