package app

import (
	"context"
	"errors"

	"gorm.io/gorm"
	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
)

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
// that the Session exists for the owner, its scope matches the call, the work
// and assignment are still live, the tool belongs to the Session's
// application, and the space is still the owner's. An identified call gets a
// durable receipt; a refused call reaches the model as its tool result.
func (s *Service) executeBusinessTool(ctx context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	session, err := s.agentSessionForOwner(call.OwnerID, call.SessionID)
	if err != nil {
		return as.ToolResult{}, err
	}
	scope := as.Scope{Application: session.Application, SpaceType: session.SpaceType, SpaceID: session.SpaceID}
	if call.Scope.Normalized() != scope {
		return as.ToolResult{}, as.NewError(as.CodeScopeMismatch, "工具调用的业务范围与会话不一致")
	}
	work, err := s.fenceAgentToolExecution(call)
	if err != nil {
		return as.ToolResult{}, err
	}
	adapter, ok := s.agentAdapters().Adapter(scope.Application)
	if !ok {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "该业务没有可用工具")
	}
	if _, ok := s.agentAdapters().Tool(scope.Application, call.Name); !ok {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不属于当前业务")
	}
	if work != nil && work.AuthorizationJSON != "" {
		authorization, err := ao.DecodeAuthorization(work.AuthorizationJSON)
		if err != nil {
			return as.ToolResult{}, err
		}
		if !authorization.AllowsTool(call.Name) {
			return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不在工作授权范围内")
		}
	}
	if err := adapter.ValidateSpace(ctx, call.OwnerID, scope); err != nil {
		return as.ToolResult{}, err
	}
	return s.dispatchAgentToolExecution(ctx, call, adapter)
}

// dispatchAgentToolExecution runs the adapter, normalizing missing business
// objects into refusals before the receipt records an outcome.
func (s *Service) dispatchAgentToolExecution(ctx context.Context, call as.ToolInvocation, adapter as.BusinessAdapter) (as.ToolResult, error) {
	execute := func() (as.ToolResult, error) {
		result, err := adapter.Execute(ctx, call)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return as.ToolResult{}, as.NewError(as.CodeNotFound, "业务对象不存在")
		}
		return result, err
	}
	if call.OperationID == "" {
		return execute()
	}
	return s.executeAgentOperation(ctx, call, execute)
}
