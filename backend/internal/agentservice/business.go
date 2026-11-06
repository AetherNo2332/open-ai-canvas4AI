package agentservice

import (
	"context"
	"encoding/json"
	"regexp"
	"sync"
)

// ToolSpec is a business tool a definition may expose. Exposing a tool never
// grants it: every execution is checked again by the adapter against the
// owner, the Session scope and the work authorization.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	// Charges marks tools that may create billed tasks; they run under the
	// Session's budget grant and require an approval when the definition asks.
	Charges bool
}

// ToolInvocation is one call, with the identity the server resolved.
type ToolInvocation struct {
	OwnerID       string
	SessionID     string
	Scope         Scope
	WorkItemID    string
	BudgetGrantID string
	CallID        string
	Name          string
	Arguments     json.RawMessage
	// TurnMessageID, Step and OperationID are the execution identity of this
	// call inside the Session: the operation key is the turn, the step and the
	// provider call ID, so a provider reusing call IDs cannot alias two
	// executions. An empty OperationID marks a direct call without a receipt.
	TurnMessageID string
	Step          int
	OperationID   string
	// AssignmentID and AssignmentEpoch carry the executing assignment; the
	// fence rejects the call once the work is stopped or reassigned.
	AssignmentID    string
	AssignmentEpoch int64
}

// ToolResult is what the model sees. Resources and the operation reference
// let reports point at business objects without copying them.
type ToolResult struct {
	Text      string   `json:"text"`
	Resources []string `json:"resources,omitempty"`
	Operation string   `json:"operation,omitempty"`
}

// BusinessAdapter connects one application. Business rules stay in the
// application's own services; the adapter only translates calls.
type BusinessAdapter interface {
	Application() string
	SpaceTypes() []string
	ValidateSpace(ctx context.Context, ownerID string, scope Scope) error
	Tools() []ToolSpec
	Execute(ctx context.Context, call ToolInvocation) (ToolResult, error)
}

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// AdapterRegistry maps applications to adapters. A new business registers an
// adapter; the Session core and supervisor do not change.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]BusinessAdapter
	tools    map[string]string // tool name -> application
}

func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: map[string]BusinessAdapter{}, tools: map[string]string{}}
}

func (r *AdapterRegistry) Register(adapter BusinessAdapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	app := adapter.Application()
	if app == "" {
		return NewError(CodeInvalidRequest, "业务适配器缺少业务名")
	}
	if _, exists := r.adapters[app]; exists {
		return NewError(CodeInvalidRequest, "该业务已有适配器")
	}
	for _, tool := range adapter.Tools() {
		if !toolNamePattern.MatchString(tool.Name) {
			return NewError(CodeInvalidRequest, "工具名无效："+tool.Name)
		}
		if owner, taken := r.tools[tool.Name]; taken && owner != app {
			return NewError(CodeInvalidRequest, "工具名已被其他业务占用："+tool.Name)
		}
	}
	for _, tool := range adapter.Tools() {
		r.tools[tool.Name] = app
	}
	r.adapters[app] = adapter
	return nil
}

func (r *AdapterRegistry) Adapter(application string) (BusinessAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[application]
	return adapter, ok
}

// Tool resolves a tool only within the application that owns it.
func (r *AdapterRegistry) Tool(application, name string) (ToolSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.tools[name] != application {
		return ToolSpec{}, false
	}
	for _, tool := range r.adapters[application].Tools() {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolSpec{}, false
}
