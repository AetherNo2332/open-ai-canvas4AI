package app

import (
	"errors"
	"strings"
	"sync"
)

// strict function calling 的启用与探测（handoff 工作项 B 第 2 步的第二半）。
//
// 上游支持 strict 时，由 provider 按 schema 约束采样，能从源头减少缺字段/类型错的调用；
// 但"支持 strict"必须由能力合同显式声明，不能靠猜——把不支持 strict 的上游当支持，
// 会让每一步工具调用都失败。因此：
//   - 能力合同声明 `text.strictTools=true` 才发 strict；
//   - 上游若仍然拒绝 strict，自动回退一次（不带 strict 重发），并把"这条线路不吃 strict"
//     记在进程内，后续请求不再尝试；
//   - 无论哪条路径，服务端的**本地 schema 预检**都照常执行（见 cloud_agent_tool_schema.go），
//     它是兜底而不是替代品。

// cloudAgentStrictToolRejections 记住"这条线路拒绝过 strict"：命中后不再尝试，
// 避免每一步都先失败一次。进程内缓存，重启后重新探测（代价只是每进程一次）。
var cloudAgentStrictToolRejections sync.Map

func cloudAgentStrictToolKey(channelID, modelKey string) string {
	return strings.TrimSpace(channelID) + "|" + strings.TrimSpace(modelKey)
}

func cloudAgentStrictToolsRejected(channelID, modelKey string) bool {
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(modelKey) == "" {
		return false
	}
	_, rejected := cloudAgentStrictToolRejections.Load(cloudAgentStrictToolKey(channelID, modelKey))
	return rejected
}

func cloudAgentRememberStrictToolsRejected(channelID, modelKey string) {
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(modelKey) == "" {
		return
	}
	cloudAgentStrictToolRejections.Store(cloudAgentStrictToolKey(channelID, modelKey), true)
}

// cloudAgentStepStrictTools 解析本步是否该带 strict：能力合同声明了、且这条线路没拒绝过。
func (s *Service) cloudAgentStepStrictTools(state *cloudAgentRuntime) bool {
	if s == nil || s.repo == nil || state == nil {
		return false
	}
	if cloudAgentStrictToolsRejected(state.Request.ChannelID, state.Request.ChannelModelKey) {
		return false
	}
	channelModel, err := s.repo.ChannelModelByKey(state.Request.ChannelID, state.Request.ChannelModelKey)
	if err != nil || channelModel == nil || normalizeCapability(channelModel.Capability) != "text" {
		return false
	}
	config, err := normalizedChannelModelCapability(channelModel)
	if err != nil || config == nil || config.Text == nil || config.Text.StrictTools == nil {
		return false
	}
	return *config.Text.StrictTools
}

// isAgentStrictToolsCompatibilityError 识别"上游不认 strict"这一类报错。
// 只在明确提到 strict 时才回退：泛化的 unknown parameter 可能指向别的问题，误判会掩盖真因。
func isAgentStrictToolsCompatibilityError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	var payloadErr providerPayloadError
	if errors.As(err, &payloadErr) {
		message += " " + strings.ToLower(payloadErr.raw)
	}
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) {
		message += " " + strings.ToLower(httpErr.Body)
	}
	if !strings.Contains(message, "strict") {
		return false
	}
	for _, marker := range []string{"unknown", "unrecognized", "unexpected", "not supported", "unsupported", "invalid", "extra"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// applyAgentStrictTools 给每个工具函数定义加 strict 标记。
// 只作用于"函数工具"形状；声明式协议的请求体由适配器负责，不走这里。
func applyAgentStrictTools(body map[string]interface{}, enabled bool) {
	if !enabled || body == nil {
		return
	}
	tools, ok := body["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		return
	}
	for _, item := range tools {
		tool, _ := item.(map[string]interface{})
		function, _ := tool["function"].(map[string]interface{})
		if function == nil {
			continue
		}
		function["strict"] = true
	}
}

// stripAgentStrictTools 去掉 strict 标记（回退用）。
func stripAgentStrictTools(body map[string]interface{}) bool {
	if body == nil {
		return false
	}
	tools, ok := body["tools"].([]interface{})
	if !ok {
		return false
	}
	changed := false
	for _, item := range tools {
		tool, _ := item.(map[string]interface{})
		function, _ := tool["function"].(map[string]interface{})
		if function == nil {
			continue
		}
		if _, exists := function["strict"]; exists {
			delete(function, "strict")
			changed = true
		}
	}
	return changed
}
