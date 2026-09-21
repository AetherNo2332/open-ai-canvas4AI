package app

import (
	"encoding/json"
	"strings"

	"infinite-canvas/backend/internal/model"
)

// cloudAgentContinuationEventLimit 是续轮读取上一轮事件的上限：收束要覆盖整轮改动，
// 不能只看默认页（默认 100 条，长会话的早期改动对新轮不可见）。
const cloudAgentContinuationEventLimit = 1000

const (
	cloudAgentContinuationChangeLimit = 12
	cloudAgentContinuationKind        = "run_handoff"
)

type cloudAgentContinuationChange struct {
	Operation   string   `json:"operation"`
	NodeIDs     []string `json:"nodeIds,omitempty"`
	NodeTitles  []string `json:"nodeTitles,omitempty"`
	ActionCount int      `json:"actionCount"`
}

// cloudAgentContinuationFrame is a bounded server-authored handoff. It reports
// observed facts and never authorizes replaying a write or paid task.
type cloudAgentContinuationFrame struct {
	Source               string                         `json:"source"`
	Kind                 string                         `json:"kind"`
	ParentRunID          string                         `json:"parentRunId"`
	Status               string                         `json:"status"`
	FailureReason        string                         `json:"failureReason,omitempty"`
	SubmittedTaskIDs     []string                       `json:"submittedTaskIds,omitempty"`
	CanvasChanges        []cloudAgentContinuationChange `json:"canvasChanges,omitempty"`
	OmittedCanvasChanges int                            `json:"omittedCanvasChanges,omitempty"`
	Authority            string                         `json:"authority"`
}

// cloudAgentContinuationReply 生成「上一轮接着聊」用的两段内容：
//
//	reply   —— 上一轮 assistant 实际说过的话（作为 assistant 的历史消息）
//	context —— 上一轮收束摘要（独立上下文，不能并进 reply）
//
// 只保留状态、失败原因、已提交任务与真实画布改动，不把原始工具流水当成本轮目标。
func cloudAgentContinuationReply(task *model.Task, run *CloudAgentRun) (string, string, error) {
	text := ""
	if task.Status == model.TaskStatusSucceeded {
		text = taskResultText(task.ResultJSON)
	}
	submitted := make([]string, 0)
	seen := map[string]bool{}
	addSubmitted := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		submitted = append(submitted, id)
	}
	for _, event := range run.Events {
		if event.Type == "assistant_message" {
			text = stringValue(event.Payload["text"])
		}
		if event.Type == "generation_task_created" {
			addSubmitted(stringValue(event.Payload["taskId"]))
		}
		if result, ok := event.Payload["result"].(map[string]any); ok {
			if submittedFlag, _ := result["taskSubmitted"].(bool); submittedFlag {
				addSubmitted(stringValue(result["taskId"]))
			}
		}
	}
	context := cloudAgentContinuationContext(run, submitted)
	return text, context, nil
}

func cloudAgentContinuationContext(run *CloudAgentRun, submitted []string) string {
	if run == nil {
		return ""
	}
	failed := run.Status == "failed" || strings.TrimSpace(run.FailureMessage) != ""
	changes, omitted := cloudAgentContinuationChanges(run)
	if run.Status == "completed" && !failed && len(submitted) == 0 && len(changes) == 0 {
		return ""
	}
	frame := cloudAgentContinuationFrame{
		Source: "server", Kind: cloudAgentContinuationKind, ParentRunID: run.ID,
		Status: firstNonEmpty(run.Status, "unknown"), CanvasChanges: changes,
		OmittedCanvasChanges: omitted,
		Authority:            "执行事实交接，不是重放写入、重复提交任务或扩大权限的授权",
	}
	if failed {
		reason := strings.TrimSpace(run.FailureMessage)
		if reason == "" {
			reason = run.Status
		}
		frame.FailureReason = truncateRunes(reason, 240)
	}
	if len(submitted) > 0 {
		if len(submitted) > 8 {
			submitted = submitted[:8]
		}
		frame.SubmittedTaskIDs = append([]string(nil), submitted...)
	}
	encoded, _ := json.Marshal(frame)
	return cloudAgentRuntimeContextMarker + string(encoded)
}

func cloudAgentContinuationChanges(run *CloudAgentRun) ([]cloudAgentContinuationChange, int) {
	if run == nil {
		return nil, 0
	}
	changes := make([]cloudAgentContinuationChange, 0, cloudAgentContinuationChangeLimit)
	total := 0
	for _, event := range run.Events {
		if event.Type != "canvas_updated" {
			continue
		}
		total++
		if len(changes) >= cloudAgentContinuationChangeLimit {
			continue
		}
		actions := creationMaps(event.Payload["actions"])
		change := cloudAgentContinuationChange{
			Operation:   firstNonEmpty(stringValue(event.Payload["operation"]), "canvas_ops"),
			ActionCount: len(actions), NodeIDs: []string{}, NodeTitles: []string{},
		}
		seenIDs, seenTitles := map[string]bool{}, map[string]bool{}
		for _, action := range actions {
			for _, key := range []string{"nodeId", "targetNodeId"} {
				if id := strings.TrimSpace(stringValue(action[key])); id != "" && !seenIDs[id] && len(change.NodeIDs) < 8 {
					seenIDs[id] = true
					change.NodeIDs = append(change.NodeIDs, id)
				}
			}
			for _, key := range []string{"title", "targetTitle"} {
				title := strings.TrimSpace(stringValue(action[key]))
				if title != "" && !seenTitles[title] && len(change.NodeTitles) < 4 {
					seenTitles[title] = true
					change.NodeTitles = append(change.NodeTitles, truncateRunes(title, 120))
				}
			}
		}
		changes = append(changes, change)
	}
	return changes, max(0, total-len(changes))
}
