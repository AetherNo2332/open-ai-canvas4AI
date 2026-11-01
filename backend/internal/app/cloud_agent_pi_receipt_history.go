package app

import (
	"encoding/json"
	"reflect"
	"strings"

	"yingce/backend/internal/model"
)

type PiHistoricalToolReceipt struct {
	RunID              string          `json:"runId"`
	AssistantEntryID   string          `json:"assistantEntryId"`
	AssistantTimestamp int64           `json:"assistantTimestamp"`
	CallID             string          `json:"callId"`
	Result             json.RawMessage `json:"result"`
	IsError            bool            `json:"isError,omitempty"`
}

// The assistant entry and model-task acknowledgement commit in one transaction.
// Preserve that exact entry ID rather than guessing a batch from reused call IDs.
func piCheckpointAssistantEntryID(input PiMessageCheckpoint) string {
	var message any
	if json.Unmarshal(input.Message, &message) != nil {
		return ""
	}
	for _, raw := range input.SessionEntries {
		var entry struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Message any    `json:"message"`
		}
		if json.Unmarshal(raw, &entry) == nil && entry.Type == "message" && reflect.DeepEqual(entry.Message, message) {
			return entry.ID
		}
	}
	return ""
}

// piHistoricalFinishEnvelope is the shape of a stored Pi assistant message
// entry, reduced to the finish_run tool-call fields the receipt restore needs.
type piHistoricalFinishEnvelope struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Message struct {
		Role      string `json:"role"`
		Timestamp int64  `json:"timestamp"`
		Content   []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			Arguments struct {
				Summary string `json:"summary"`
			} `json:"arguments"`
		} `json:"content"`
	} `json:"message"`
}

// Only restore an exact finish_run call from the same conversation. Historical
// receipts are data for the model projection, never permission to execute a call.
func (s *Service) piHistoricalFinishReceipts(current *model.CloudAgentExecution, entries []PiAgentSessionEntry) ([]PiHistoricalToolReceipt, error) {
	result := []PiHistoricalToolReceipt{}
	states := map[string]*cloudAgentRuntime{}
	missing := piMissingHistoricalFinishCalls(entries)
	for _, entry := range entries {
		if entry.RunID == current.ID {
			continue
		}
		var envelope piHistoricalFinishEnvelope
		if json.Unmarshal(entry.Entry, &envelope) != nil || envelope.Type != "message" || envelope.Message.Role != "assistant" {
			continue
		}
		for _, part := range envelope.Message.Content {
			if part.Type != "toolCall" || part.Name != "finish_run" || !missing[entry.RunID+"\x00"+envelope.ID+"\x00"+part.ID] {
				continue
			}
			previous, err := s.piHistoricalRunState(current, entry.RunID, states)
			if err != nil {
				return nil, err
			}
			if previous == nil {
				continue
			}
			if previous.PiToolBatchAssistantEntryID == "" || envelope.ID != previous.PiToolBatchAssistantEntryID {
				continue
			}
			for _, call := range previous.Calls {
				if call.ID != part.ID || call.Function.Name != "finish_run" || cloudAgentToolCallSummary(call) != strings.TrimSpace(part.Arguments.Summary) {
					continue
				}
				if receipt := piToolReceipt(previous, previous.PiToolBatchTaskID, part.ID); receipt != nil {
					result = append(result, PiHistoricalToolReceipt{RunID: entry.RunID, AssistantEntryID: envelope.ID, AssistantTimestamp: envelope.Message.Timestamp, CallID: part.ID, Result: receipt.Result, IsError: receipt.IsError})
				}
			}
		}
	}
	return result, nil
}

// piHistoricalRunState loads (once per run) the terminal Pi run of the same
// conversation that owns a historical entry; nil means it cannot supply receipts.
func (s *Service) piHistoricalRunState(current *model.CloudAgentExecution, runID string, states map[string]*cloudAgentRuntime) (*cloudAgentRuntime, error) {
	if previous, loaded := states[runID]; loaded {
		return previous, nil
	}
	row, err := s.repo.CloudAgent(current.UserID, runID)
	if err != nil {
		if s.repo.IsMissing(err) {
			states[runID] = nil
			return nil, nil
		}
		return nil, err
	}
	if row.Engine != "pi" || !cloudAgentRunTerminal(row.Status) || firstNonEmpty(row.ConversationID, row.ID) != firstNonEmpty(current.ConversationID, current.ID) {
		states[runID] = nil
		return nil, nil
	}
	decoded, err := cloudAgentDecode(row)
	if err != nil {
		return nil, err
	}
	states[runID] = &decoded
	return &decoded, nil
}

// Match results only within their assistant batch; providers can reuse call IDs
// in later steps. Already complete history needs no database recovery or payload.
func piMissingHistoricalFinishCalls(entries []PiAgentSessionEntry) map[string]bool {
	missing := map[string]bool{}
	pending := map[string]string{}
	runID := ""
	for _, entry := range entries {
		var envelope struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Message struct {
				Role       string          `json:"role"`
				ToolCallID string          `json:"toolCallId"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(entry.Entry, &envelope) != nil || envelope.Type != "message" {
			continue
		}
		if envelope.Message.Role == "toolResult" && runID == entry.RunID {
			delete(missing, pending[envelope.Message.ToolCallID])
			continue
		}
		pending, runID = map[string]string{}, entry.RunID
		if envelope.Message.Role != "assistant" {
			continue
		}
		var parts []struct{ Type, ID, Name string }
		if json.Unmarshal(envelope.Message.Content, &parts) != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "toolCall" && part.Name == "finish_run" {
				key := entry.RunID + "\x00" + envelope.ID + "\x00" + part.ID
				pending[part.ID], missing[key] = key, true
			}
		}
	}
	return missing
}
