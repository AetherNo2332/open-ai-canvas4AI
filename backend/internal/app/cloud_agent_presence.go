package app

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/canvas"
	"yingce/backend/internal/model"
)

type AgentCanvasPresence = canvas.AgentPresence

// cloudAgentPresenceNodeIDs extracts the node IDs a tool call addresses. It only
// reads arguments already validated by the tool contract; unknown shapes yield
// no IDs, which still lets write tools announce an "editing" phase.
func cloudAgentPresenceNodeIDs(call cloudAgentCall) []string {
	var args any
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
		return nil
	}
	ids := []string{}
	add := func(value any) {
		switch typed := value.(type) {
		case string:
			ids = append(ids, typed)
		case []any:
			for _, item := range typed {
				if id, ok := item.(string); ok {
					ids = append(ids, id)
				}
			}
		}
	}
	var walk func(value any, depth int)
	walk = func(value any, depth int) {
		if depth > 4 {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for key, item := range typed {
				lower := strings.ToLower(key)
				if strings.HasSuffix(lower, "nodeid") || strings.HasSuffix(lower, "nodeids") {
					add(item)
				} else if key == "ops" {
					for _, op := range asSlice(item) {
						if object, ok := op.(map[string]any); ok {
							add(object["id"])
							walk(object, depth+1)
						}
					}
				}
			}
		case []any:
			for _, item := range typed {
				walk(item, depth+1)
			}
		}
	}
	walk(args, 0)
	return ids
}

func asSlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func cloudAgentPresencePhase(name string) string {
	if cloudAgentWrite(name) {
		return "editing"
	}
	if cloudAgentReadToolReadOnly(name) && (strings.HasPrefix(name, "canvas_") || strings.HasPrefix(name, "previs_")) {
		return "reading"
	}
	return ""
}

// publishCloudAgentToolPresence is best-effort and ephemeral: it is sent before
// a tool executes so open windows can mark the nodes the Agent is touching.
func (s *Service) publishCloudAgentToolPresence(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) {
	if s == nil || s.canvasRealtime == nil || run == nil || state == nil || state.Request.CanvasID == "" {
		return
	}
	phase := cloudAgentPresencePhase(call.Function.Name)
	if phase == "" {
		return
	}
	ids := cloudAgentPresenceNodeIDs(call)
	if phase == "reading" && len(ids) == 0 {
		return
	}
	s.canvasRealtime.AgentPresence(run.UserID, state.Request.CanvasID, AgentCanvasPresence{RunID: run.ID, NodeIDs: ids, Phase: phase})
}

func (s *Service) publishCloudAgentIdle(userID, runID, canvasID string) {
	if s == nil || s.canvasRealtime == nil || canvasID == "" {
		return
	}
	s.canvasRealtime.AgentPresence(userID, canvasID, AgentCanvasPresence{RunID: runID, Phase: "idle"})
}
