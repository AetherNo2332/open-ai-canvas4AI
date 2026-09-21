package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/platform"
)

// cloudAgentCompactionRatio 是触发语义压缩的上下文利用率：下一步预计输入 token
// （优先上游实测锚点投影）达到**输入预算**（窗口 − 输出预留 − overhead）的比例时，
// 暂停步进循环、把历史压成检查点，然后继续。触发线取 85%（与上游同口径）。
const cloudAgentCompactionRatio = 0.85

// cloudAgentRequestHardLimitBytes 是请求正文的硬上限：预算算式之外的最后一道闸。
const cloudAgentRequestHardLimitBytes = 192 << 10

// cloudAgentStepOperation 是一次"模型调用"任务的操作名；只有它会与上游实测配锚点
// （压缩任务发的是另一份请求，拿它当锚点会把压力算到错误的信封上）。
const cloudAgentStepOperation = "cloud_agent_step"

// cloudAgentStepMaxOutputTokens 是单步模型调用的输出上限的出厂默认值，只在策略读取失败时兜底；
// 生效值来自运行时策略的 AgentStepMaxOutputTokens（管理端可改、可设 0 表示不限制）。
const cloudAgentStepMaxOutputTokens = platform.DefaultRuntimeAgentStepOutputTokens

// cloudAgentStepBoostFallbackTokens 是"不限制输出"（策略值为 0）时放大重试用的预算：
// 不限制的本意是"让模型写完"，重试却必须有个上界，否则一次卡住的调用会一直占着单步墙钟。
const cloudAgentStepBoostFallbackTokens = 32_768

// cloudAgentTokenAnchor 是"上游实测 + 本地估算"的一对读数。
// 上游用量来自模型自己的分词器（provider 上报），是计费与压力可以采信的权威值；
// 本地估算记录的是发出同一次请求时的读法，二者的差就是投影后续请求所需的换算基准。
type cloudAgentTokenAnchor struct {
	TaskID          string `json:"taskId"`
	Step            int    `json:"step"`
	InputTokens     int64  `json:"inputTokens"`
	CachedTokens    int64  `json:"cachedTokens"`
	OutputTokens    int64  `json:"outputTokens"`
	EstimatedTokens int    `json:"estimatedTokens"`
	SourceBytes     int    `json:"sourceBytes"`
	Accepted        bool   `json:"accepted"`
	RejectReason    string `json:"rejectReason,omitempty"`
	// Signature / Model / ChannelID 记录"定锚时的口径"：换了模型、路由、系统提示或工具 schema
	// 之后，旧实测不再可比，必须作废（设计 §6 的锚点治理）。
	Signature string `json:"signature,omitempty"`
	Model     string `json:"model,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
}

// cloudAgentAnchorMaxAgeSteps 是锚点的最长有效期：超过这么多步没有刷新就作废。
// 真机实测过"锚点冻结"——`anchorStep` 恒为 4、`pressureTokens` 恒 20,035，而估算从
// 42,581 涨到 66,105，压缩决策却还在用那个老读数。
const cloudAgentAnchorMaxAgeSteps = 3

// cloudAgentStepSignature 是"这一步的口径指纹"：模型/渠道/逻辑模型 + 系统提示 + 工具 schema。
func cloudAgentStepSignature(state *cloudAgentRuntime) string {
	if state == nil {
		return ""
	}
	tools, _ := json.Marshal(state.Canonical.Tools)
	return creationHash(strings.Join([]string{
		state.Request.Model, state.Request.ChannelID, state.Request.ChannelModelKey, state.Request.LogicalModelID,
		state.Canonical.SystemPrompt, string(tools),
	}, "\u0000"))
}

// cloudAgentExpireTokenAnchor 让"口径已变或太久没刷新"的锚点作废。
// 只标记不删除：读数仍要能显示"这个实测是多少、为什么不再用它"。
func cloudAgentExpireTokenAnchor(runID string, state *cloudAgentRuntime) {
	if state == nil || state.TokenAnchor == nil || !state.TokenAnchor.Accepted {
		return
	}
	anchor := state.TokenAnchor
	if signature := cloudAgentStepSignature(state); anchor.Signature != "" && anchor.Signature != signature {
		switch {
		case anchor.Model != "" && anchor.Model != state.Request.Model:
			anchor.RejectReason = "模型已变化，锚点作废"
			state.event(runID, "context_transition", map[string]any{"kind": "model_changed", "reason": "anchor_signature_changed", "text": anchor.RejectReason})
		case anchor.ChannelID != "" && anchor.ChannelID != state.Request.ChannelID:
			anchor.RejectReason = "供应线路已变化，锚点作废"
			state.event(runID, "context_transition", map[string]any{"kind": "route_changed", "reason": "anchor_signature_changed", "text": anchor.RejectReason})
		default:
			anchor.RejectReason = "系统提示或工具 schema 已变化，锚点作废"
		}
		anchor.Accepted = false
		return
	}
	if state.Step-anchor.Step > cloudAgentAnchorMaxAgeSteps {
		anchor.RejectReason = "锚点超过 " + strconv.Itoa(cloudAgentAnchorMaxAgeSteps) + " 步未刷新，已作废"
		anchor.Accepted = false
	}
}

// cloudAgentAnchorMinRatio / MaxRatio 是采信上游用量的合理区间。
// 实测与自估差出一个量级时通常意味着换了模型或计量口径（例如上游只报 cached、
// 或走了不同的协议分支），此时宁可继续用估算，也不要把压力曲线锚到错误基准上。
const (
	cloudAgentAnchorMinRatio = 0.5
	cloudAgentAnchorMaxRatio = 2.0
)

// recordCloudAgentTokenAnchor 用上一步的上游实测用量给上下文压力定锚。
// 幂等：同一任务只采信一次；没有实测或比值离谱时记录拒绝原因并保留估算。
func (s *Service) recordCloudAgentTokenAnchor(state *cloudAgentRuntime) {
	if s == nil || state == nil {
		return
	}
	// 即使这一步拿不到新的实测，也要先把"口径已变/太旧"的锚点作废，不能让压缩决策继续用它。
	cloudAgentExpireTokenAnchor(state.RuntimeRunID, state)
	if s.repo == nil || state.LastStepTaskID == "" || state.LastStepEstimate <= 0 {
		return
	}
	if state.LastStepOperation != cloudAgentStepOperation {
		return
	}
	if state.TokenAnchor != nil && state.TokenAnchor.TaskID == state.LastStepTaskID {
		return
	}
	log, ok, err := s.repo.APICallLogUsageForTask(state.LastStepTaskID)
	if err != nil || !ok {
		return
	}
	anchor := &cloudAgentTokenAnchor{
		TaskID: state.LastStepTaskID, Step: state.Step, InputTokens: log.InputTokens,
		CachedTokens: log.CachedTokens, OutputTokens: log.OutputTokens,
		EstimatedTokens: state.LastStepEstimate, SourceBytes: state.LastStepSourceBytes,
		Signature: cloudAgentStepSignature(state), Model: state.Request.Model, ChannelID: state.Request.ChannelID,
	}
	ratio := float64(anchor.InputTokens) / float64(anchor.EstimatedTokens)
	switch {
	case ratio < cloudAgentAnchorMinRatio:
		anchor.RejectReason = "上游实测远低于本地估算，可能换了模型或口径"
	case ratio > cloudAgentAnchorMaxRatio:
		anchor.RejectReason = "上游实测远高于本地估算，可能换了模型或口径"
	default:
		anchor.Accepted = true
	}
	state.TokenAnchor = anchor
}

// cloudAgentStepLimits 是一次模型调用实际生效的执行边界。
type cloudAgentStepLimits struct {
	// OutputTokens 是本次调用的输出上限；0 表示不限制（只有 Timeout 兜底）。
	OutputTokens int
	// Timeout 是单步墙钟：策略给了秒级值时用它，否则沿用文本任务超时。
	Timeout time.Duration
}

// cloudAgentStepLimits 解析当前生效的单步边界。策略读取失败时退回出厂默认值：
// 拿到一个确定的上界，好过让一次调用无限跑下去。
func (s *Service) cloudAgentStepLimits() cloudAgentStepLimits {
	policy, err := s.runtimeConcurrencySetting()
	if err != nil {
		policy = defaultRuntimePolicy().Task
	}
	limits := cloudAgentStepLimits{OutputTokens: policy.AgentStepMaxOutputTokens}
	if policy.AgentStepTimeoutSeconds > 0 {
		limits.Timeout = time.Duration(policy.AgentStepTimeoutSeconds) * time.Second
	} else {
		limits.Timeout = time.Duration(policy.TextTimeoutMinutes) * time.Minute
	}
	return limits
}

// cloudAgentStepOutputBudget 把生效上限折算成本步请求要带的 maxOutputTokens：
// 放大档（空输出升级重试）在原值上翻倍并以硬上限封顶；原值为 0（不限制）时用兜底值，
// 让重试仍然有界。
func cloudAgentStepOutputBudget(limits cloudAgentStepLimits, boosted bool) int {
	if !boosted {
		return limits.OutputTokens
	}
	if limits.OutputTokens <= 0 {
		return cloudAgentStepBoostFallbackTokens
	}
	return min(limits.OutputTokens*2, platform.MaxRuntimeAgentStepOutputTokens)
}
