package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"yingce/backend/internal/platform"
	"yingce/backend/internal/repository"
)

// 同参缓存只能拦住“原样重复”的读取。模型也可能不断修改 offset、nodeIds 或
// 读取参数来绕过缓存，因此本轮还要限制所有只读工具的累计调用次数。
// 该上限高于正常画布分页读取所需次数，但足以在异常循环继续消耗模型额度前止损。
const cloudAgentMaxReadToolCallsPerRun = platform.DefaultRuntimeAgentReadCallsPerRun

func cloudAgentReadToolCacheable(name string) bool {
	switch name {
	case "canvas_get_state", "storyboard", "model_list":
		return true
	default:
		return false
	}
}

func cloudAgentReadCall(call cloudAgentCall) bool {
	return cloudAgentReadToolReadOnly(call.Function.Name) || (call.Function.Name == "storyboard" && !cloudAgentWriteCall(call))
}

func cloudAgentReadToolReadOnly(name string) bool {
	switch name {
	case "web_search", "canvas_get_state", "canvas_read_content", "canvas_list_assets", "canvas_search_nodes", "canvas_read_batch_table", "canvas_list_node_types", "model_list", "task_get":
		return true
	default:
		return false
	}
}

func cloudAgentReadCacheKey(call cloudAgentCall) string {
	arguments := strings.TrimSpace(call.Function.Arguments)
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		if object, ok := value.(map[string]any); ok {
			switch call.Function.Name {
			case "canvas_get_state", "storyboard", "canvas_read_batch_table":
				for _, field := range []string{"offset", "connectionOffset", "storyboardOffset"} {
					if _, exists := object[field]; !exists {
						object[field] = float64(0)
					}
				}
			}
			value = object
		}
		if normalized, err := json.Marshal(value); err == nil {
			arguments = string(normalized)
		}
	}
	return call.Function.Name + ":" + arguments
}

func cloudAgentReadCacheKeyForState(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall) string {
	key := cloudAgentReadCacheKey(call)
	if state == nil {
		return key
	}
	switch call.Function.Name {
	case "canvas_get_state", "storyboard", "canvas_read_content", "canvas_search_nodes", "model_list":
		if repo != nil {
			if canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID); err == nil && canvas != nil {
				return fmt.Sprintf("%s:canvas-revision:%d", key, canvas.Revision)
			}
		}
	}
	return key
}

func cloudAgentReadToolCached(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall, services ...*Service) (any, error) {
	if !cloudAgentReadCall(call) {
		return cloudAgentReadTool(repo, userID, state, call, services...)
	}
	if state == nil {
		return nil, errors.New("Agent 只读工具缺少运行时状态")
	}
	if !cloudAgentReadToolCacheable(call.Function.Name) {
		if state.ReadToolCalls >= state.callLimits().ReadCallsPerRun {
			return nil, &cloudAgentReadLoopError{ToolName: call.Function.Name, Count: state.ReadToolCalls + 1, Budget: true}
		}
		state.ReadToolCalls++
		return cloudAgentReadTool(repo, userID, state, call, services...)
	}
	key := cloudAgentReadCacheKeyForState(repo, userID, state, call)
	if state.ReadToolCalls >= state.callLimits().ReadCallsPerRun {
		return nil, &cloudAgentReadLoopError{ToolName: call.Function.Name, Count: state.ReadToolCalls + 1, Budget: true}
	}
	if state.ToolReadResults != nil {
		if cached, ok := state.ToolReadResults[key]; ok {
			if result, handled, replayErr := cloudAgentReplayCachedRead(state, key, cached); handled {
				return result, replayErr
			}
		}
	}
	state.ReadToolCalls++
	state.readCacheExecution = true
	result, err := cloudAgentReadTool(repo, userID, state, call, services...)
	state.readCacheExecution = false
	if cacheErr := cloudAgentCacheReadOutcome(state, key, result, err); cacheErr != nil {
		return result, cacheErr
	}
	return result, err
}

// cloudAgentReplayCachedRead 重放缓存的只读结果或错误。handled=false 表示缓存条目
// 已失效（仅缓存过一次的错误会被清除），需要真实执行。
func cloudAgentReplayCachedRead(state *cloudAgentRuntime, key string, cached cloudAgentCachedToolResult) (any, bool, error) {
	if cached.Error != "" {
		if cached.ArgumentError {
			return nil, true, cloudAgentReplayCachedError(cached)
		}
		delete(state.ToolReadResults, key)
		return nil, false, nil
	}
	if state.ToolReadReplays == nil {
		state.ToolReadReplays = map[string]int{}
	}
	cached.ReplayCount = state.ToolReadReplays[key] + 1
	state.ToolReadReplays[key] = cached.ReplayCount
	state.ToolReadResults[key] = cached
	if len(cached.Result) == 0 {
		return nil, true, errors.New("缓存的 Agent 只读结果无效")
	}
	if !cloudAgentReadResultInContext(state, cached.Result) {
		var restored any
		if err := json.Unmarshal(cached.Result, &restored); err != nil {
			return nil, true, errors.New("缓存的 Agent 只读结果无效")
		}
		return restored, true, nil
	}
	return map[string]any{"cacheReplay": true, "replayCount": cached.ReplayCount, "message": "结果与上次读取相同，可直接使用；若用户刚修改过画布或纠正了你，请重新读取需要的节点"}, true, nil
}

// cloudAgentReplayCachedError 依据缓存的错误分类重建参数错误：字段级问题保留
// field/issue，rowId 类问题附带无效与重复清单。
func cloudAgentReplayCachedError(cached cloudAgentCachedToolResult) error {
	var err error = &cloudAgentArgumentError{BadAuthRequest(cached.Error)}
	if cached.Field != "" {
		err = &cloudAgentFieldArgumentError{error: err, Field: cached.Field, Issue: cached.Issue}
	}
	if len(cached.InvalidRowIDs)+len(cached.DuplicateRowIDs) > 0 {
		err = &cloudAgentStoryboardRowIDError{error: err, InvalidRowIDs: append([]string{}, cached.InvalidRowIDs...), DuplicateRowIDs: append([]string{}, cached.DuplicateRowIDs...)}
	}
	return err
}

// cloudAgentCacheReadOutcome 把执行结果写回缓存：参数类错误按分类缓存以便重放，
// 成功结果编码后缓存。编码失败原样向上返回。
func cloudAgentCacheReadOutcome(state *cloudAgentRuntime, key string, result any, err error) error {
	if err != nil {
		var argumentErr *cloudAgentArgumentError
		if errors.As(err, &argumentErr) {
			if state.ToolReadResults == nil {
				state.ToolReadResults = map[string]cloudAgentCachedToolResult{}
			}
			cached := cloudAgentCachedToolResult{Error: cloudAgentSafeToolError(err), ArgumentError: true}
			var fieldErr *cloudAgentFieldArgumentError
			if errors.As(err, &fieldErr) {
				cached.Field, cached.Issue = fieldErr.Field, fieldErr.Issue
			}
			var rowErr *cloudAgentStoryboardRowIDError
			if errors.As(err, &rowErr) {
				cached.InvalidRowIDs = append([]string(nil), rowErr.InvalidRowIDs...)
				cached.DuplicateRowIDs = append([]string(nil), rowErr.DuplicateRowIDs...)
			}
			state.ToolReadResults[key] = cached
		}
		return nil
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return marshalErr
	}
	if state.ToolReadResults == nil {
		state.ToolReadResults = map[string]cloudAgentCachedToolResult{}
	}
	state.ToolReadResults[key] = cloudAgentCachedToolResult{Result: encoded}
	return nil
}
