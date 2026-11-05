package agentservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"
)

// Memory scopes. Session notes are private to one Session; business_space
// notes are explicitly published to one exact space; user_preference notes are
// written by the user only. Nothing else is injected into a model input.
const (
	MemorySession        = "session"
	MemoryBusinessSpace  = "business_space"
	MemoryUserPreference = "user_preference"
)

// Message kinds and the actor that produced a message. Actor kinds are written
// by the server, never taken from a request body.
const (
	MessageKindMessage = "message"
	MessageKindSummary = "summary"

	ActorUser       = "user"
	ActorSupervisor = "supervisor"
	ActorExecutor   = "executor"
	ActorSystem     = "system"
)

// Context block kinds, in the order they appear in a snapshot.
const (
	BlockPlatformRules  = "platform_rules"
	BlockDefinition     = "definition"
	BlockPreference     = "memory_user_preference"
	BlockSpaceDocument  = "memory_business_space"
	BlockSessionNote    = "memory_session"
	BlockSummary        = "summary"
	BlockHistory        = "history"
	DefaultHistoryBytes = 96 << 10
)

// HistoryMessage is one persisted Session message as the builder sees it.
type HistoryMessage struct {
	ID        string
	OwnerID   string
	SessionID string
	Sequence  int64
	Kind      string
	Actor     string
	Role      string
	Text      string
}

// MemoryNote is one scoped note as the builder sees it.
type MemoryNote struct {
	ID        string
	OwnerID   string
	Kind      string
	SessionID string
	Scope     Scope
	Revision  int64
	Text      string
}

// ContextInput is everything the composition root read for one build. The
// builder trusts none of it: every item must carry the Session's identity.
type ContextInput struct {
	OwnerID         string
	SessionID       string
	SessionRevision int64
	Scope           Scope
	Definition      Definition
	PlatformRules   string
	Messages        []HistoryMessage
	Notes           []MemoryNote
	HistoryBytes    int
}

// ContextBlock is one traceable piece of a model input. ContextOnly is always
// true: context is reference material and never grants a tool, a write or a
// charge.
type ContextBlock struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	SessionID   string `json:"sessionId"`
	Scope       Scope  `json:"scope"`
	Revision    int64  `json:"revision"`
	Visibility  string `json:"visibility"`
	Role        string `json:"role,omitempty"`
	Actor       string `json:"actor,omitempty"`
	Text        string `json:"text"`
	ContextOnly bool   `json:"contextOnly"`
}

// ContextSnapshot is the frozen, auditable context of one model input.
type ContextSnapshot struct {
	OwnerID           string         `json:"ownerId"`
	SessionID         string         `json:"sessionId"`
	Scope             Scope          `json:"scope"`
	DefinitionID      string         `json:"definitionId"`
	DefinitionVersion string         `json:"definitionVersion"`
	SessionRevision   int64          `json:"sessionRevision"`
	Blocks            []ContextBlock `json:"blocks"`
	Tools             []string       `json:"tools"`
	Hash              string         `json:"hash"`
}

// BuildContext assembles the snapshot. Any item whose owner, Session or scope
// differs from the Session being built is a scope_mismatch: a query bug must
// surface instead of silently mixing another Session into the input. History
// starts at the latest summary of this Session and keeps the newest messages
// that fit the byte budget.
func BuildContext(in ContextInput) (ContextSnapshot, error) {
	scope := in.Scope.Normalized()
	if strings.TrimSpace(in.OwnerID) == "" || strings.TrimSpace(in.SessionID) == "" || scope.Application == "" || scope.SpaceType == "" || scope.SpaceID == "" || in.Definition.ID == "" {
		return ContextSnapshot{}, NewError(CodeInvalidRequest, "上下文缺少会话身份，拒绝组装")
	}
	if scope.Application != in.Definition.Application || !slices.Contains(in.Definition.SpaceTypes, scope.SpaceType) {
		return ContextSnapshot{}, NewError(CodeScopeMismatch, "会话范围与 Agent 定义不一致")
	}
	base := ContextBlock{SessionID: in.SessionID, Scope: scope, Revision: in.SessionRevision, ContextOnly: true}
	blocks := []ContextBlock{}
	if rules := strings.TrimSpace(in.PlatformRules); rules != "" {
		blocks = append(blocks, base.with(BlockPlatformRules, "platform", "platform", rules))
	}
	blocks = append(blocks, base.with(BlockDefinition, "definition:"+in.Definition.ID+"@"+in.Definition.Version, "definition", in.Definition.Title))
	notes, err := contextNotes(in, scope, base)
	if err != nil {
		return ContextSnapshot{}, err
	}
	history, err := contextHistory(in, base)
	if err != nil {
		return ContextSnapshot{}, err
	}
	blocks = append(append(blocks, notes...), history...)
	snapshot := ContextSnapshot{
		OwnerID: in.OwnerID, SessionID: in.SessionID, Scope: scope, DefinitionID: in.Definition.ID, DefinitionVersion: in.Definition.Version,
		SessionRevision: in.SessionRevision, Blocks: blocks, Tools: slices.Clone(in.Definition.Tools),
	}
	raw, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(raw)
	snapshot.Hash = hex.EncodeToString(sum[:])
	return snapshot, nil
}

func (b ContextBlock) with(kind, source, visibility, text string) ContextBlock {
	b.Kind, b.Source, b.Visibility, b.Text = kind, source, visibility, text
	return b
}

func contextNotes(in ContextInput, scope Scope, base ContextBlock) ([]ContextBlock, error) {
	order := map[string]int{MemoryUserPreference: 0, MemoryBusinessSpace: 1, MemorySession: 2}
	notes := slices.Clone(in.Notes)
	slices.SortStableFunc(notes, func(a, b MemoryNote) int { return order[a.Kind] - order[b.Kind] })
	blocks := make([]ContextBlock, 0, len(notes))
	for _, note := range notes {
		if note.OwnerID != in.OwnerID {
			return nil, NewError(CodeScopeMismatch, "记忆不属于当前用户")
		}
		block := base.with("", "note:"+note.ID, "", note.Text)
		block.Revision = note.Revision
		switch note.Kind {
		case MemorySession:
			if note.SessionID != in.SessionID {
				return nil, NewError(CodeScopeMismatch, "会话笔记不属于当前会话")
			}
			block.Kind, block.Visibility = BlockSessionNote, "session"
		case MemoryBusinessSpace:
			if note.Scope.Normalized() != scope {
				return nil, NewError(CodeScopeMismatch, "业务文档不属于当前业务空间")
			}
			block.Kind, block.Visibility = BlockSpaceDocument, "space"
		case MemoryUserPreference:
			block.Kind, block.Visibility = BlockPreference, "user"
		default:
			return nil, NewError(CodeScopeMismatch, "未知的记忆范围")
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func contextHistory(in ContextInput, base ContextBlock) ([]ContextBlock, error) {
	messages := slices.Clone(in.Messages)
	slices.SortFunc(messages, func(a, b HistoryMessage) int { return int(a.Sequence - b.Sequence) })
	start := 0
	for index, message := range messages {
		if message.OwnerID != in.OwnerID || message.SessionID != in.SessionID {
			return nil, NewError(CodeScopeMismatch, "历史消息不属于当前会话")
		}
		if message.Kind == MessageKindSummary {
			start = index
		}
	}
	messages = messages[start:]
	budget := in.HistoryBytes
	if budget <= 0 {
		budget = DefaultHistoryBytes
	}
	// Keep the summary (if any) and walk back from the newest message.
	kept := []HistoryMessage{}
	used := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		size := len(message.Text)
		if message.Kind != MessageKindSummary && used+size > budget {
			continue
		}
		used += size
		kept = append(kept, message)
	}
	slices.Reverse(kept)
	blocks := make([]ContextBlock, 0, len(kept))
	for _, message := range kept {
		kind := BlockHistory
		if message.Kind == MessageKindSummary {
			kind = BlockSummary
		}
		block := base.with(kind, "message:"+message.ID, "session", message.Text)
		block.Role, block.Actor, block.Revision = message.Role, message.Actor, message.Sequence
		blocks = append(blocks, block)
	}
	return blocks, nil
}

// ValidateNoteText bounds a note before it can enter later model inputs.
func ValidateNoteText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > 2000 {
		return "", NewError(CodeInvalidRequest, "笔记不能为空，且不超过 2000 字")
	}
	return text, nil
}

// RenderText is the plain-text form of a snapshot used as the model's system
// context: every block is labelled with its source so audits can trace it.
func RenderText(snapshot ContextSnapshot) string {
	var out strings.Builder
	for _, block := range snapshot.Blocks {
		if block.Kind == BlockHistory {
			continue
		}
		out.WriteString("[" + block.Kind + " " + block.Source + "]\n" + block.Text + "\n\n")
	}
	return strings.TrimSpace(out.String())
}
