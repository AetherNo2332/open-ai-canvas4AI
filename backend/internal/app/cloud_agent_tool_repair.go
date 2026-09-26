package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"infinite-canvas/backend/internal/model"
)

const (
	cloudAgentToolAttemptLimit = 3
	// cloudAgentToolScopeAttempt 是"收紧可用工具"的档位：同一个错误指纹第二次出现时，
	// 下一步只开放修复所需的工具，避免模型继续在别的工具上犯错。
	cloudAgentToolScopeAttempt = 2
)

// cloudAgentToolRepair 是同一个**错误指纹**的自动纠错进度。
// 只按工具名计数会把"换了字段的另一种错误"也算进同一个额度；带上指纹之后，
// 模型改对了字段再犯新错时不会立刻被熔断，而原地重复同一个错误会更快被收紧。
type cloudAgentToolRepair struct {
	GroupID     string `json:"groupId"`
	Attempt     int    `json:"attempt"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// cloudAgentToolErrorFingerprint 是"同一个错误"的判据：工具 + 字段级问题 + 目标 + 参数外形。
// 用参数外形（键与类型）而不是整段参数：值变了但错法没变仍算同一个错误。
func cloudAgentToolErrorFingerprint(call cloudAgentCall, err error) string {
	tool := call.Function.Name
	field, issue := "", ""
	var fieldErr *cloudAgentFieldArgumentError
	if errors.As(err, &fieldErr) {
		field, issue = fieldErr.Field, fieldErr.Issue
	}
	return creationHash(strings.Join([]string{tool, issue, field, cloudAgentArgumentShape(call.Function.Arguments)}, "\n"))
}

// cloudAgentArgumentShape 把参数压成"键路径 + 类型"的形状串（值不参与）。
func cloudAgentArgumentShape(raw string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "invalid-json"
	}
	parts := make([]string, 0, len(args))
	for key, value := range args {
		parts = append(parts, key+":"+cloudAgentArgumentValueShape(value))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func cloudAgentArgumentValueShape(value any) string {
	switch typed := value.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		return "number"
	case []any:
		if len(typed) == 0 {
			return "array"
		}
		return "array<" + cloudAgentArgumentValueShape(typed[0]) + ">"
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return "object{" + strings.Join(keys, ",") + "}"
	case nil:
		return "null"
	default:
		return "other"
	}
}

// Count per tool and per fingerprint, not per model step: intervening reads must not
// reset a failed write's allowance. Only explicitly typed, pre-execution errors are
// repairable.
func cloudAgentTrackToolRepair(runID string, state *cloudAgentRuntime, call cloudAgentCall, result any, err error, payload map[string]any) bool {
	previous, exists := state.ToolRepairs[call.Function.Name]
	if err == nil {
		if exists {
			payload["retry"] = map[string]any{"groupId": previous.GroupID, "attempt": previous.Attempt, "maxAttempts": cloudAgentToolAttemptLimit, "status": "recovered"}
			delete(state.ToolRepairs, call.Function.Name)
			// 修好了就把工具集恢复原状：收紧只服务于"正在修的这一步"。
			cloudAgentClearToolScope(state)
		}
		return false
	}
	detail, _ := result.(map[string]any)
	var argumentErr *cloudAgentArgumentError
	if !errors.As(err, &argumentErr) || detail["taskSubmitted"] == true || detail["phase"] == "completion" {
		return false
	}
	if state.ToolRepairs == nil {
		state.ToolRepairs = map[string]cloudAgentToolRepair{}
	}
	fingerprint := cloudAgentToolErrorFingerprint(call, err)
	if !exists {
		previous.GroupID = runID + ":repair:" + call.ID
	} else if previous.Fingerprint != "" && previous.Fingerprint != fingerprint {
		// 换了错法：从第一次重新给机会（但仍沿用同一个纠错组，便于界面追踪）。
		previous.Attempt = 0
	}
	previous.Fingerprint = fingerprint
	previous.Attempt++
	state.ToolRepairs[call.Function.Name] = previous
	exhausted := previous.Attempt >= cloudAgentToolAttemptLimit
	status := "retrying"
	if exhausted {
		status = "exhausted"
	}
	retry := map[string]any{
		"groupId": previous.GroupID, "attempt": previous.Attempt,
		"maxAttempts": cloudAgentToolAttemptLimit, "status": status, "fingerprint": fingerprint,
	}
	if !exhausted && previous.Attempt >= cloudAgentToolScopeAttempt {
		// 第二次同一个错误：下一步只开放修复所需的工具，避免继续扩大错误面。
		scope := cloudAgentRepairToolScope(call)
		state.ToolScope = scope
		// 收紧是**附加**信息：status 仍是 retrying（前端与既有的自动纠错契约不变）。
		retry["toolScope"] = scope
		retry["ladder"] = "scoped"
	}
	payload["retry"], detail["retry"] = retry, retry
	return exhausted
}

// cloudAgentRepairToolScope 是"只修这一个错误"需要的最小工具集：
// 出错的那个工具本身，加上读画布 / 读模型目录 / 问用户这三个出口。
func cloudAgentRepairToolScope(call cloudAgentCall) []string {
	scope := []string{call.Function.Name}
	for _, name := range []string{"canvas_get_state", "model_list", "canvas_read_storyboard", "canvas_read_batch_table", "ask_user"} {
		if name != call.Function.Name {
			scope = append(scope, name)
		}
	}
	return scope
}

func cloudAgentClearToolScope(state *cloudAgentRuntime) {
	if state != nil {
		state.ToolScope = nil
	}
}

func cloudAgentRecordToolResult(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, result any, err error) {
	if !cloudAgentToolResult(run.ID, state, call, result, err) {
		return
	}
	run.Status = "failed"
	run.FailureMessage = truncateRunes(fmt.Sprintf("自动纠正已尝试 %d 次，仍未完成：%s", cloudAgentToolAttemptLimit, cloudAgentSafeToolError(err)), 1000)
	cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(run.FailureMessage, 120), state)
	repair := state.ToolRepairs[call.Function.Name]
	state.event(run.ID, "run_failed", map[string]any{
		"text": run.FailureMessage, "reason": "tool_retry_exhausted", "toolName": call.Function.Name,
		"callId": call.ID, "attempts": cloudAgentToolAttemptLimit, "fingerprint": repair.Fingerprint,
	})
}

// cloudAgentScopedTools 按收紧档过滤工具表；没有收紧（scope 为空）时返回 nil 表示不动。
func cloudAgentScopedTools(tools []map[string]any, scope []string) []map[string]any {
	if len(scope) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(scope))
	for _, name := range scope {
		allowed[name] = true
	}
	kept := make([]map[string]any, 0, len(scope))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if allowed[stringField(function, "name")] {
			kept = append(kept, tool)
		}
	}
	return kept
}

// cloudAgentToolInScope 判断某个工具名在本步是否被收紧档放行。
func cloudAgentToolInScope(state *cloudAgentRuntime, name string) bool {
	if state == nil || len(state.ToolScope) == 0 {
		return true
	}
	for _, allowed := range state.ToolScope {
		if allowed == name {
			return true
		}
		if cloudAgentIsToolCategory(name) && cloudAgentToolCategory(allowed) == name {
			return true
		}
	}
	return false
}
