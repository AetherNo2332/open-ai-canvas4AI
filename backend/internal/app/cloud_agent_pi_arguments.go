package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"unicode/utf8"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// Pi must carry an exact billed call through preflight even when its JSON is
// invalid. Only envelope checks move here; legacy execution validation stays strict.
func validatePiToolBatchEnvelope(calls []cloudAgentCall) error {
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		if err := validateCloudAgentID(call.ID, "工具调用 ID", 160); err != nil || seen[call.ID] || call.Function.Name == "" || len(call.Function.Name) > 80 || !utf8.ValidString(call.Function.Name) {
			return errors.New("invalid Pi tool call")
		}
		if len(call.Function.Arguments) > 32000 || !utf8.ValidString(call.Function.Arguments) {
			return errors.New("invalid Pi tool argument envelope")
		}
		seen[call.ID] = true
	}
	return nil
}

func piSameToolArguments(left, right string) bool {
	var leftObject, rightObject map[string]any
	if decodeCloudAgentJSONObject(left, &leftObject) != nil || decodeCloudAgentJSONObject(right, &rightObject) != nil {
		// Invalid/non-object arguments cannot be normalized, repaired or replaced.
		return left == right
	}
	return reflect.DeepEqual(leftObject, rightObject)
}

func piHasRejectedArgumentState(state *cloudAgentRuntime, index int) bool {
	if state == nil || state.PiToolBatchTaskID == "" || index < 0 || index >= len(state.Calls) {
		return false
	}
	admission, ok := cloudAgentAdmissionFor(state, index)
	return ok && !admission.Allowed && admission.CallID == state.Calls[index].ID &&
		cloudAgentStrictArgumentObjectError(state.Calls[index].Function.Arguments) != nil
}

// Native reads have no canvas batch. A worker's raw-error annotation must match
// the acknowledged billed result before its paired failure can be persisted.
func (s *Service) validatePiNativeRawRead(repo *repository.Repository, userID string, state *cloudAgentRuntime, transcript []model.CloudAgentMessageRecord, callID string, isError bool) error {
	denied := func() error { return kernel.Forbidden("Pi native read argument rejection is not verified") }
	for index := len(transcript) - 1; index >= 0; index-- {
		if transcript[index].Kind != cloudAgentMessageKindPi {
			continue
		}
		var message struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string          `json:"type"`
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
				Invalid   json.RawMessage `json:"canvasInvalidArguments"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(transcript[index].MessageJSON), &message) != nil || message.Role != "assistant" {
			continue
		}
		for _, block := range message.Content {
			if block.Type != "toolCall" || block.ID != callID || block.Name != "read" {
				continue
			}
			if len(block.Invalid) == 0 {
				return nil
			}
			var raw *string
			var invalid map[string]json.RawMessage
			var arguments map[string]any
			if !isError || json.Unmarshal(block.Invalid, &invalid) != nil || len(invalid) != 1 || json.Unmarshal(invalid["rawArguments"], &raw) != nil || raw == nil ||
				json.Unmarshal(block.Arguments, &arguments) != nil || len(arguments) != 1 || arguments["__canvasInvalidToolArguments"] != true || cloudAgentStrictArgumentObjectError(*raw) == nil {
				return denied()
			}
			if state.PiAssistantCheckpointTaskID == "" {
				return denied()
			}
			task, err := repo.TaskForUser(userID, state.PiAssistantCheckpointTaskID)
			if err != nil || task.Status != model.TaskStatusSucceeded || task.ProjectID != state.Request.CanvasID {
				return denied()
			}
			var output struct {
				Calls          []cloudAgentCall `json:"toolCalls"`
				Legacy         []cloudAgentCall `json:"tool_calls"`
				StopReasonKind string           `json:"stopReasonKind"`
			}
			if json.Unmarshal([]byte(task.ResultJSON), &output) != nil {
				return denied()
			}
			stop := output.StopReasonKind
			if stop == "" {
				stop = cloudAgentStopKindUnknown
			}
			if cloudAgentStepStopDisposition(state, stop) != cloudAgentStepDispositionAccept {
				return denied()
			}
			calls := output.Calls
			if len(calls) == 0 {
				calls = output.Legacy
			}
			matches := 0
			for _, original := range calls {
				if original.ID == callID && original.Function.Name == "read" {
					if original.Function.Arguments != *raw || validatePiToolBatchEnvelope([]cloudAgentCall{original}) != nil {
						return denied()
					}
					matches++
				}
			}
			if matches != 1 {
				return denied()
			}
			return nil
		}
		return nil // Existing native pairing checks reject a missing current call.
	}
	return nil
}
