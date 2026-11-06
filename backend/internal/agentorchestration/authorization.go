package agentorchestration

import (
	"encoding/json"
	"slices"
	"strings"
)

// WorkAuthorization is the typed authorization snapshot of one work tree. The
// server writes it once when the root work starts, inside the user's explicit
// request and the server's own product policy; nothing a model, a report or a
// later message says can widen it. Children carry a copy taken at delegation
// time, so a restarted executor reads the same grant.
type WorkAuthorization struct {
	// Version is the authorization version. A changed scope or budget bumps it
	// and expires every approval bound to the previous version.
	Version int64 `json:"version"`
	// LimitMicrocredits is the root budget the user authorized; the server
	// rejects anything above its own product ceiling before any cost.
	LimitMicrocredits int64        `json:"limitMicrocredits"`
	Scopes            []ScopeGrant `json:"scopes"`
	// AllowedTools narrows the charged tools the tree may execute; empty means
	// every tool of the authorized definitions.
	AllowedTools []string `json:"allowedTools,omitempty"`
	// ApprovalThresholdMicrocredits is the per-operation quote above which the
	// executor must obtain the user's approval before spending.
	ApprovalThresholdMicrocredits int64 `json:"approvalThresholdMicrocredits"`
}

// ScopeGrant is one business space this work tree may enter.
type ScopeGrant struct {
	Application string `json:"application"`
	SpaceType   string `json:"spaceType"`
	SpaceID     string `json:"spaceId"`
}

// ValidateRootAuthorization checks a new root authorization against the
// product policy. It fails before any cost is possible.
func ValidateRootAuthorization(auth WorkAuthorization, ceiling int64) error {
	if auth.Version < 1 {
		return NewError(CodeInvalidRequest, "授权版本无效")
	}
	if auth.LimitMicrocredits <= 0 {
		return NewError(CodeInvalidRequest, "工作需要正的授权额度")
	}
	if auth.LimitMicrocredits > ceiling {
		return NewError(CodeBudgetExceeded, "授权额度超过产品上限")
	}
	if len(auth.Scopes) == 0 {
		return NewError(CodeInvalidRequest, "工作需要至少一个授权业务空间")
	}
	for _, scope := range auth.Scopes {
		if strings.TrimSpace(scope.Application) == "" || strings.TrimSpace(scope.SpaceType) == "" || strings.TrimSpace(scope.SpaceID) == "" {
			return NewError(CodeInvalidRequest, "授权空间必须包含业务、空间类型与空间 ID")
		}
	}
	if auth.ApprovalThresholdMicrocredits < 0 {
		return NewError(CodeInvalidRequest, "审批阈值无效")
	}
	return nil
}

// AllowsScope reports whether the tree may delegate into this exact space.
func (a WorkAuthorization) AllowsScope(scope ScopeGrant) bool {
	for _, granted := range a.Scopes {
		if granted.Application == scope.Application && granted.SpaceType == scope.SpaceType && granted.SpaceID == scope.SpaceID {
			return true
		}
	}
	return false
}

// AllowsTool reports whether the tree may execute this tool. An empty list
// keeps the definition's own tool set as the boundary.
func (a WorkAuthorization) AllowsTool(name string) bool {
	if len(a.AllowedTools) == 0 {
		return true
	}
	return slices.Contains(a.AllowedTools, name)
}

// EncodeAuthorization stores the snapshot; DecodeAuthorization reads it back.
func EncodeAuthorization(auth WorkAuthorization) (string, error) {
	raw, err := json.Marshal(auth)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func DecodeAuthorization(raw string) (WorkAuthorization, error) {
	auth := WorkAuthorization{}
	if strings.TrimSpace(raw) == "" {
		return auth, NewError(CodeInvalidRequest, "工作缺少授权快照")
	}
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return auth, NewError(CodeInvalidRequest, "授权快照损坏")
	}
	return auth, nil
}

// CheckRootCompletion lets a root work complete only when every descendant is
// completed; anything still running, waiting or failed keeps the root open so
// the summary cannot claim facts the tree has not produced.
func CheckRootCompletion(children []ChildWorkSummary) error {
	for _, child := range children {
		if child.Status != WorkCompleted {
			return NewError(CodeInvalidRequest, "子工作 "+child.ID+" 尚未完成（"+child.Status+"），不能结束根工作")
		}
	}
	return nil
}

// ChildWorkSummary is the state a completion check needs, decoupled from the
// persistence layer.
type ChildWorkSummary struct {
	ID     string
	Status string
}
