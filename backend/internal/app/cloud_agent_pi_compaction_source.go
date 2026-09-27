package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
)

type cloudAgentPiCompactionSource struct {
	Messages           []map[string]any
	FirstKeptEntryID   string
	ActiveLeafID       string
	SourceDigest       string
	SourceBytes        int
	CompactedTurnCount int
}

type cloudAgentPiCompactionEntry struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	ParentID         *string         `json:"parentId"`
	Message          json.RawMessage `json:"message"`
	Summary          string          `json:"summary"`
	FirstKeptEntryID string          `json:"firstKeptEntryId"`
}

type cloudAgentPiContextMessage struct {
	EntryID string
	Value   map[string]any
}

// cloudAgentPiActiveBranch resolves the persisted Pi tree to one root-to-leaf path.
// It rejects missing parents and cycles instead of summarizing an ambiguous transcript.
func cloudAgentPiActiveBranch(entries []model.CloudAgentPiEntry, activeLeafID string) ([]cloudAgentPiCompactionEntry, error) {
	if activeLeafID == "" {
		if len(entries) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("Pi compaction active leaf is missing")
	}
	byID := make(map[string]model.CloudAgentPiEntry, len(entries))
	for _, entry := range entries {
		if entry.EntryID == "" || entry.EntryID != strings.TrimSpace(entry.EntryID) {
			return nil, fmt.Errorf("Pi compaction entry identity is invalid")
		}
		if _, exists := byID[entry.EntryID]; exists {
			return nil, fmt.Errorf("Pi compaction entry ID is duplicated")
		}
		byID[entry.EntryID] = entry
	}
	if _, exists := byID[activeLeafID]; !exists {
		return nil, fmt.Errorf("Pi compaction active leaf does not exist")
	}
	seen := make(map[string]bool, len(entries))
	reversed := make([]cloudAgentPiCompactionEntry, 0, len(entries))
	for entryID := activeLeafID; entryID != ""; {
		if seen[entryID] {
			return nil, fmt.Errorf("Pi compaction entry tree contains a cycle")
		}
		seen[entryID] = true
		stored, exists := byID[entryID]
		if !exists {
			return nil, fmt.Errorf("Pi compaction entry parent does not exist")
		}
		var entry cloudAgentPiCompactionEntry
		if err := json.Unmarshal([]byte(stored.EntryJSON), &entry); err != nil || entry.ID != stored.EntryID {
			return nil, fmt.Errorf("Pi compaction entry JSON is invalid")
		}
		parentID := ""
		if entry.ParentID != nil {
			parentID = *entry.ParentID
		}
		if parentID != stored.ParentID {
			return nil, fmt.Errorf("Pi compaction entry parent identity does not match")
		}
		reversed = append(reversed, entry)
		entryID = parentID
	}
	branch := make([]cloudAgentPiCompactionEntry, len(reversed))
	for index := range reversed {
		branch[len(reversed)-1-index] = reversed[index]
	}
	return branch, nil
}

// cloudAgentPiCompactionSourceForBranch projects the active Pi branch into the same
// canonical message shape used by the existing Go compactor. A previous Pi compaction
// contributes its structured checkpoint plus only the messages Pi still exposes after
// its firstKeptEntryId; abandoned branches and already-compacted history are excluded.
func cloudAgentPiCompactionSourceForBranch(branch []cloudAgentPiCompactionEntry, activeLeafID string) (cloudAgentPiCompactionSource, error) {
	var source cloudAgentPiCompactionSource
	source.ActiveLeafID = activeLeafID
	if len(branch) == 0 || branch[len(branch)-1].ID != activeLeafID {
		return source, fmt.Errorf("Pi compaction source does not end at its active leaf")
	}
	latestCompaction := -1
	for index := range branch {
		if branch[index].Type == "compaction" {
			latestCompaction = index
		}
	}
	visible := make([]cloudAgentPiContextMessage, 0, len(branch)+2)
	if latestCompaction >= 0 {
		compaction := branch[latestCompaction]
		if strings.TrimSpace(compaction.Summary) == "" || compaction.FirstKeptEntryID == "" {
			return source, fmt.Errorf("Pi compaction boundary is incomplete")
		}
		checkpointMessage := map[string]any{"role": "user", "content": compaction.Summary}
		if _, err := agentcontext.ParseFrame(compaction.Summary); err == nil {
			checkpointMessage[cloudAgentContextSourceKey] = "checkpoint"
		}
		visible = append(visible, cloudAgentPiContextMessage{EntryID: compaction.ID, Value: checkpointMessage})
		visible = append(visible, cloudAgentPiContextMessage{EntryID: compaction.ID, Value: map[string]any{
			"role": "assistant", "content": agentcontext.Acknowledgement,
		}})
		keepFrom := latestCompaction
		if compaction.FirstKeptEntryID != compaction.ID {
			keepFrom = -1
			for index := 0; index < latestCompaction; index++ {
				if branch[index].ID == compaction.FirstKeptEntryID {
					keepFrom = index
					break
				}
			}
			if keepFrom < 0 {
				return source, fmt.Errorf("Pi compaction kept entry is not on the active branch")
			}
		}
		for index := keepFrom; index < latestCompaction; index++ {
			message, ok, err := cloudAgentPiEntryMessage(branch[index])
			if err != nil {
				return source, err
			}
			if ok {
				visible = append(visible, message)
			}
		}
		for index := latestCompaction + 1; index < len(branch); index++ {
			message, ok, err := cloudAgentPiEntryMessage(branch[index])
			if err != nil {
				return source, err
			}
			if ok {
				visible = append(visible, message)
			}
		}
	} else {
		for _, entry := range branch {
			message, ok, err := cloudAgentPiEntryMessage(entry)
			if err != nil {
				return source, err
			}
			if ok {
				visible = append(visible, message)
			}
		}
	}
	if len(visible) == 0 {
		return source, fmt.Errorf("Pi compaction active branch has no context messages")
	}
	source.Messages = make([]map[string]any, 0, len(visible))
	for _, item := range visible {
		source.Messages = append(source.Messages, item.Value)
	}
	source.FirstKeptEntryID = cloudAgentPiFirstKeptEntryID(visible)
	if source.FirstKeptEntryID == "" {
		return source, fmt.Errorf("Pi compaction could not find a complete-turn retention boundary")
	}
	source.CompactedTurnCount = cloudAgentConversationTurnCount(source.Messages)
	encoded, err := json.Marshal(source.Messages)
	if err != nil {
		return source, err
	}
	source.SourceBytes = len(encoded)
	digest := sha256.Sum256(encoded)
	source.SourceDigest = hex.EncodeToString(digest[:])
	return source, nil
}

func cloudAgentPiFirstKeptEntryID(messages []cloudAgentPiContextMessage) string {
	completed := make([]string, 0, len(messages))
	pending := ""
	for _, item := range messages {
		message := item.Value
		role := stringField(message, "role")
		if role == "user" {
			if stringField(message, cloudAgentContextSourceKey) == "checkpoint" {
				continue
			}
			pending = item.EntryID
			continue
		}
		if role != "assistant" || pending == "" {
			continue
		}
		if cloudAgentPiHasToolCalls(message["tool_calls"]) {
			continue
		}
		completed = append(completed, pending)
		pending = ""
	}
	start := max(0, len(completed)-cloudAgentContextKeepPairs)
	if start < len(completed) {
		return completed[start]
	}
	if pending != "" {
		return pending
	}
	for _, message := range messages {
		if stringField(message.Value, "role") == "user" {
			return message.EntryID
		}
	}
	if len(messages) > 0 {
		return messages[0].EntryID
	}
	return ""
}

func cloudAgentPiHasToolCalls(value any) bool {
	switch calls := value.(type) {
	case []any:
		return len(calls) > 0
	case []map[string]any:
		return len(calls) > 0
	default:
		return false
	}
}

func cloudAgentPiEntryMessage(entry cloudAgentPiCompactionEntry) (cloudAgentPiContextMessage, bool, error) {
	if entry.Type != "message" {
		return cloudAgentPiContextMessage{}, false, nil
	}
	var message struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		IsError    bool            `json:"isError"`
	}
	if err := json.Unmarshal(entry.Message, &message); err != nil {
		return cloudAgentPiContextMessage{}, false, fmt.Errorf("Pi compaction message is invalid")
	}
	content, err := cloudAgentPiText(message.Content)
	if err != nil {
		return cloudAgentPiContextMessage{}, false, err
	}
	role := message.Role
	value := map[string]any{"content": content}
	switch role {
	case "user":
		value["role"] = "user"
	case "assistant":
		value["role"] = "assistant"
		var blocks []map[string]any
		if err := json.Unmarshal(message.Content, &blocks); err == nil {
			calls := make([]map[string]any, 0)
			for _, block := range blocks {
				if block["type"] != "toolCall" {
					continue
				}
				args, err := json.Marshal(block["arguments"])
				if err != nil {
					return cloudAgentPiContextMessage{}, false, err
				}
				calls = append(calls, map[string]any{
					"id": stringValue(block["id"]), "type": "function",
					"function": map[string]any{"name": stringValue(block["name"]), "arguments": string(args)},
				})
			}
			if len(calls) > 0 {
				value["tool_calls"] = calls
			}
		}
	case "toolResult":
		value["role"] = "tool"
		value["tool_call_id"] = message.ToolCallID
		value["name"] = message.ToolName
		value["is_error"] = message.IsError
	default:
		return cloudAgentPiContextMessage{}, false, nil
	}
	return cloudAgentPiContextMessage{EntryID: entry.ID, Value: value}, true, nil
}

func cloudAgentPiText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("Pi compaction content is invalid")
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block["type"] == "text" {
			if value := stringValue(block["text"]); value != "" {
				parts = append(parts, value)
			}
		}
	}
	return strings.Join(parts, "\n"), nil
}
