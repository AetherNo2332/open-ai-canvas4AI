package creationplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Structured text is the trusted Go-only text contract (P1): messages with
// tool calls and tool results, tool definitions and a tool choice. It never
// travels over HTTP; Agent runtimes build it after their own scope checks.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"

	ToolChoiceAuto     = "auto"
	ToolChoiceRequired = "required"
	ToolChoiceNone     = "none"

	structuredMessagesMax = 400
	structuredToolsMax    = 64
)

// ToolCall is one call the model asked for. Arguments is the raw JSON object.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// StructuredMessage keeps tool semantics: assistant messages may carry calls,
// tool messages answer exactly one call by ID.
type StructuredMessage struct {
	Role       string     `json:"role"`
	Text       string     `json:"text,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
}

// ToolDefinition is a function tool; Parameters is a JSON Schema object.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type StructuredTextRequest struct {
	Model           ModelSelection      `json:"model"`
	System          string              `json:"system"`
	Messages        []StructuredMessage `json:"messages"`
	Tools           []ToolDefinition    `json:"tools"`
	ToolChoice      string              `json:"toolChoice"`
	MaxOutputTokens int                 `json:"maxOutputTokens"`
	References      []ReferenceResource `json:"references,omitempty"`
	Business        BusinessRef         `json:"business"`
	IdempotencyKey  string              `json:"idempotencyKey"`
	TraceID         string              `json:"traceId,omitempty"`
}

// StructuredUsage is the provider-reported usage when available.
type StructuredUsage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	Available    bool  `json:"available"`
}

// StructuredTextResult is the result of a succeeded structured task.
type StructuredTextResult struct {
	Text      string          `json:"text"`
	Reasoning string          `json:"reasoning,omitempty"`
	ToolCalls []ToolCall      `json:"toolCalls"`
	Usage     StructuredUsage `json:"usage"`
}

// Validate checks the structure before any model resolution or cost: every
// tool result answers an earlier call, tool names are unique, and the model
// selection names exactly one route.
func (r StructuredTextRequest) Validate() error {
	if err := r.Model.validate(); err != nil {
		return err
	}
	key := strings.TrimSpace(r.IdempotencyKey)
	if key == "" || utf8.RuneCountInString(key) > 160 {
		return NewError(CodeInvalidRequest, "幂等键必填，最长 160")
	}
	if strings.TrimSpace(r.Business.Application) == "" {
		return NewError(CodeInvalidRequest, "缺少业务归属")
	}
	if len(r.Messages) == 0 || len(r.Messages) > structuredMessagesMax || len(r.Tools) > structuredToolsMax || r.MaxOutputTokens < 0 {
		return NewError(CodeInvalidRequest, "消息或工具数量超出范围")
	}
	tools := map[string]bool{}
	for _, tool := range r.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" || tools[name] || (len(tool.Parameters) > 0 && !json.Valid(tool.Parameters)) {
			return NewError(CodeInvalidRequest, "工具定义缺少名称、重名或参数结构无效")
		}
		tools[name] = true
	}
	switch r.ToolChoice {
	case ToolChoiceAuto, ToolChoiceRequired, ToolChoiceNone:
	default:
		return NewError(CodeInvalidRequest, "工具选择无效")
	}
	if r.ToolChoice == ToolChoiceRequired && len(tools) == 0 {
		return NewError(CodeInvalidRequest, "要求调用工具时必须提供工具")
	}
	return validateStructuredMessages(r.Messages)
}

func validateStructuredMessages(messages []StructuredMessage) error {
	open, seen := map[string]bool{}, map[string]bool{}
	for _, message := range messages {
		switch message.Role {
		case RoleUser, RoleSystem:
			if strings.TrimSpace(message.Text) == "" || len(message.ToolCalls) > 0 {
				return NewError(CodeInvalidRequest, "用户或系统消息必须有正文且不能带工具调用")
			}
		case RoleAssistant:
			if strings.TrimSpace(message.Text) == "" && len(message.ToolCalls) == 0 {
				return NewError(CodeInvalidRequest, "助手消息必须有正文或工具调用")
			}
			for _, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || seen[call.ID] || (len(call.Arguments) > 0 && !json.Valid(call.Arguments)) {
					return NewError(CodeInvalidRequest, "工具调用缺少标识、名称、重复或参数无效")
				}
				open[call.ID], seen[call.ID] = true, true
			}
		case RoleTool:
			if !open[message.ToolCallID] {
				return NewError(CodeInvalidRequest, "工具结果没有对应的工具调用")
			}
			delete(open, message.ToolCallID)
		default:
			return NewError(CodeInvalidRequest, "消息角色无效")
		}
	}
	return nil
}

func (m ModelSelection) validate() error {
	logical := strings.TrimSpace(m.LogicalModelID) != ""
	system := strings.TrimSpace(m.ChannelID) != "" || strings.TrimSpace(m.ModelKey) != ""
	if logical == system || (system && (strings.TrimSpace(m.ChannelID) == "" || strings.TrimSpace(m.ModelKey) == "")) {
		return NewError(CodeInvalidRequest, "模型选择必须是逻辑模型或系统渠道模型二选一")
	}
	return nil
}

// Fingerprint identifies the request content; the trace ID is excluded.
func (r StructuredTextRequest) Fingerprint() string {
	stable := r
	stable.TraceID = ""
	stable.IdempotencyKey = strings.TrimSpace(stable.IdempotencyKey)
	encoded, _ := json.Marshal(stable)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
