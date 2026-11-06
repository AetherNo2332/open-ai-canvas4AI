package agentservice

import (
	"slices"
	"sort"
	"strings"
	"sync"
)

// Definition is registered by server code only; neither the model nor the
// browser can add definitions or widen their tools.
type Definition struct {
	ID          string
	Version     string
	Role        string
	Application string
	SpaceTypes  []string
	Title       string
	Tools       []string
}

func (d Definition) View() DefinitionView {
	return DefinitionView{ID: d.ID, Version: d.Version, Role: d.Role, Application: d.Application, SpaceTypes: slices.Clone(d.SpaceTypes), Title: d.Title, Tools: slices.Clone(d.Tools)}
}

type Registry struct {
	mu          sync.RWMutex
	definitions map[string]Definition
}

func NewRegistry() *Registry { return &Registry{definitions: map[string]Definition{}} }

func (r *Registry) Register(def Definition) error {
	if strings.TrimSpace(def.ID) == "" || strings.TrimSpace(def.Version) == "" || strings.TrimSpace(def.Application) == "" || len(def.SpaceTypes) == 0 {
		return NewError(CodeInvalidRequest, "Agent 定义缺少 ID、版本、业务或空间类型")
	}
	if def.Role != RoleBusiness && def.Role != RoleSupervisor {
		return NewError(CodeInvalidRequest, "Agent 定义角色无效")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.definitions[def.ID]; exists {
		return NewError(CodeInvalidRequest, "Agent 定义已注册")
	}
	def.SpaceTypes, def.Tools = slices.Clone(def.SpaceTypes), slices.Clone(def.Tools)
	r.definitions[def.ID] = def
	return nil
}

// Resolve returns the definition only when the scope is complete and matches
// what the definition serves. Unknown definitions fail before any cost.
func (r *Registry) Resolve(definitionID string, scope Scope) (Definition, error) {
	scope = scope.Normalized()
	r.mu.RLock()
	def, ok := r.definitions[strings.TrimSpace(definitionID)]
	r.mu.RUnlock()
	if !ok {
		return Definition{}, NewError(CodeInvalidRequest, "Agent 定义不存在")
	}
	if scope.Application == "" || scope.SpaceType == "" || scope.SpaceID == "" {
		return Definition{}, NewError(CodeInvalidRequest, "会话范围必须包含业务、空间类型与空间 ID")
	}
	if scope.Application != def.Application || !slices.Contains(def.SpaceTypes, scope.SpaceType) {
		return Definition{}, NewError(CodeScopeMismatch, "Agent 定义不服务该业务空间")
	}
	return def, nil
}

// Definition returns a registered definition by ID.
func (r *Registry) Definition(id string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.definitions[strings.TrimSpace(id)]
	return def, ok
}

// List returns definitions of one application, or all when application is empty.
func (r *Registry) List(application string) []DefinitionView {
	application = strings.TrimSpace(application)
	r.mu.RLock()
	defer r.mu.RUnlock()
	views := []DefinitionView{}
	for _, def := range r.definitions {
		if application == "" || def.Application == application {
			views = append(views, def.View())
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}
