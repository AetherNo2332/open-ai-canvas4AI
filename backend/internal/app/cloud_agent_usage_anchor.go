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
	// Signature / Model / ChannelID 记录"定锚时的口径"：换了模型、线路、系统提示或工具 schema
	// 之后，旧实测不再可比，必须作废（设计 §6 的锚点治理）。
	Signature string `json:"signature,omitempty"`
	Model     string `json:"model,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
	// ContextWindowTokens 是定锚时解析到的模型窗口（上游 #601 新增）：窗口是"离满窗还有多远"
	// 的分母，换了窗口（管理员改了渠道模型能力、或换了路由落点）之后同一个 token 数含义就变了。
	ContextWindowTokens int `json:"contextWindowTokens,omitempty"`
}

// cloudAgentAnchorMaxAgeSteps 是锚点的最长有效期：超过这么多步没有刷新就作废。
// 真机实测过"锚点冻结"——`anchorStep` 恒为 4、`pressureTokens` 恒 20,035，而估算从
// 42,581 涨到 66,105，压缩决策却还在用那个老读数。
const cloudAgentAnchorMaxAgeSteps = 3

// cloudAgentRequestSignature 是"某一份具体信封的口径指纹"：模型/渠道/逻辑模型 + 系统提示 +
// 工具 schema，另加这份信封自己的模型与线路（上游 #601 新增的两个入参）。
// 它刻意不含消息正文：消息每步都在变，含进去等于每步作废，锚点就永远用不上。
func cloudAgentRequestSignature(state *cloudAgentRuntime, canonical canonicalAgentRequest, channelID, model string) string {
	if state == nil {
		return ""
	}
	tools, _ := json.Marshal(canonical.Tools)
	return creationHash(strings.Join([]string{
		state.Request.Model, state.Request.ChannelID, state.Request.ChannelModelKey, state.Request.LogicalModelID,
		channelID, model, canonical.SystemPrompt, string(tools),
	}, "\u0000"))
}

// cloudAgentStepSignature 是"运行态里那份 canonical 的口径指纹"：模型/渠道/逻辑模型 +
// 系统提示 + 工具 schema。保留它是为了兼容还没有 LastStep* 的检查点与两侧既有用例。
func cloudAgentStepSignature(state *cloudAgentRuntime) string {
	if state == nil {
		return ""
	}
	return cloudAgentRequestSignature(state, state.Canonical, "", "")
}

// cloudAgentAnchorWindowTokens 把预算折算成锚点口径的窗口读数：没有解析到模型自己声明的
// 窗口时报 0（未知），调用方据此跳过窗口比较，而不是拿兜底默认窗口当判据。
func cloudAgentAnchorWindowTokens(budget cloudAgentContextBudget) int {
	if !budget.Configured {
		return 0
	}
	return budget.ContextWindowTokens
}

// cloudAgentExpireTokenAnchor 让"口径已变或太久没刷新"的锚点作废（我方的两参入口）。
// 只标记不删除：读数仍要能显示"这个实测是多少、为什么不再用它"。
// 比对用的是**实际发出去那份信封**的指纹与模型/线路/窗口（上游 #601 的 LastStep* 记账）；
// 检查点还没有这些字段时（升级后第一次续跑、或调用方只构造了运行态）退回按运行态算，
// 避免把"字段缺失"误判成"口径已变"。
func cloudAgentExpireTokenAnchor(runID string, state *cloudAgentRuntime) {
	if state == nil {
		return
	}
	signature := state.LastStepSignature
	if signature == "" {
		signature = cloudAgentStepSignature(state)
	}
	model, channelID := state.LastStepModel, state.LastStepChannelID
	if model == "" {
		model = state.Request.Model
	}
	if channelID == "" {
		channelID = state.Request.ChannelID
	}
	cloudAgentExpireTokenAnchorForRequest(runID, state, state.LastStepWindowTokens, signature, model, channelID)
}

// cloudAgentExpireTokenAnchorForRequest 让"口径已变、窗口已变或太久没刷新"的锚点作废。
// 只标记不删除：读数仍要能显示"这个实测是多少、为什么不再用它"。
func cloudAgentExpireTokenAnchorForRequest(runID string, state *cloudAgentRuntime, windowTokens int, signature, model, channelID string) {
	if state == nil || state.TokenAnchor == nil || !state.TokenAnchor.Accepted {
		return
	}
	anchor := state.TokenAnchor
	if anchor.Signature != "" && anchor.Signature != signature {
		switch {
		case anchor.Model != "" && anchor.Model != model:
			anchor.RejectReason = "模型已变化，锚点作废"
			state.event(runID, "context_transition", map[string]any{"kind": "model_changed", "reason": "anchor_signature_changed", "text": anchor.RejectReason})
		case anchor.ChannelID != "" && anchor.ChannelID != channelID:
			anchor.RejectReason = "供应线路已变化，锚点作废"
			state.event(runID, "context_transition", map[string]any{"kind": "route_changed", "reason": "anchor_signature_changed", "text": anchor.RejectReason})
		default:
			anchor.RejectReason = "系统提示或工具 schema 已变化，锚点作废"
		}
		anchor.Accepted = false
		return
	}
	// 窗口换了（管理员改了能力、或路由落点变了）：窗口是压力读数的分母，同一个 token 数
	// 含义已经不同，旧实测不再可比（上游 #601 的窗口治理）。
	if windowTokens > 0 && anchor.ContextWindowTokens > 0 && windowTokens != anchor.ContextWindowTokens {
		anchor.RejectReason = "模型窗口已变化，锚点作废"
		anchor.Accepted = false
		state.event(runID, "context_transition", map[string]any{
			"kind": "window_changed", "reason": "anchor_window_changed",
			"before": map[string]any{"contextWindowTokens": anchor.ContextWindowTokens},
			"after":  map[string]any{"contextWindowTokens": windowTokens},
			"text":   anchor.RejectReason,
		})
		return
	}
	if state.Step-anchor.Step > cloudAgentAnchorMaxAgeSteps {
		anchor.RejectReason = "锚点超过 " + strconv.Itoa(cloudAgentAnchorMaxAgeSteps) + " 步未刷新，已作废"
		anchor.Accepted = false
	}
}

// cloudAgentAnchorMinRatio / MaxRatio 是采信上游用量的合理区间。
// 实测与自估差出一个量级时通常意味着换了模型或计量口径（例如上游只报缓存命中、
// 或走了不同的协议分支），此时宁可继续用估算，也不要把压力曲线锚到错误基准上。
const (
	cloudAgentAnchorMinRatio = 0.5
	cloudAgentAnchorMaxRatio = 2.0
)

// recordCloudAgentTokenAnchor 用上一步的上游实测用量给上下文压力定锚。
// 幂等：同一任务只采信一次；没有实测或比值离谱时记录拒绝原因并保留估算。
// userID 用于限定"这条调用确实是本用户跑出来的"（上游 #601 的口径），不能只按 taskId 取。
func (s *Service) recordCloudAgentTokenAnchor(userID string, state *cloudAgentRuntime) {
	if s == nil || state == nil {
		return
	}
	// 即使这一步拿不到新的实测，也要先把"口径已变/太旧"的锚点作废，不能让压缩决策继续用它。
	cloudAgentExpireTokenAnchor(state.RuntimeRunID, state)
	if s.repo == nil || state.LastStepTaskID == "" || state.LastStepEstimate <= 0 || state.LastStepSignature == "" {
		return
	}
	// 只有"模型调用"这一步能配锚点：媒体任务发的是另一份请求（另一套信封），
	// 拿它当锚点会把压力算到错误的信封上。
	if state.LastStepOperation != cloudAgentStepOperation {
		return
	}
	if state.TokenAnchor != nil && state.TokenAnchor.TaskID == state.LastStepTaskID {
		return
	}
	// 只认本用户、成功、text 能力且真上报用量（usage_available）的那条调用：
	// 上游 #601 把这三条限定写进了 SQL（合并时保留上游那个带 userID 的版本，
	// 删掉我方早期同名的单参版本，Go 不支持按参数个数重载）。
	log, ok, err := s.repo.APICallLogUsageForTask(userID, state.LastStepTaskID)
	if err != nil || !ok {
		return
	}
	// A routed model may select another channel after the request was assembled.
	// Do not project that provider's tokenizer onto a different known route.
	if log.ChannelID != "" && state.LastStepChannelID != "" && log.ChannelID != state.LastStepChannelID {
		return
	}
	anchor := &cloudAgentTokenAnchor{
		TaskID: state.LastStepTaskID, Step: state.Step, InputTokens: log.InputTokens,
		CachedTokens: log.CachedTokens, OutputTokens: log.OutputTokens,
		EstimatedTokens: state.LastStepEstimate, SourceBytes: state.LastStepSourceBytes,
		Signature: state.LastStepSignature, Model: state.LastStepModel, ChannelID: state.LastStepChannelID,
		ContextWindowTokens: state.LastStepWindowTokens,
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

// cloudAgentStepLimits 解析当前生效的单步边界。策略读取失败时返回错误（fail-closed）：
// 退回出厂默认值可能比管理员配置更宽，等于悄悄放宽了执行边界。
func (s *Service) cloudAgentStepLimits() (cloudAgentStepLimits, error) {
	policy, err := s.runtimeConcurrencySetting()
	if err != nil {
		return cloudAgentStepLimits{}, err
	}
	limits := cloudAgentStepLimits{OutputTokens: policy.AgentStepMaxOutputTokens}
	if policy.AgentStepTimeoutSeconds > 0 {
		limits.Timeout = time.Duration(policy.AgentStepTimeoutSeconds) * time.Second
	} else {
		limits.Timeout = time.Duration(policy.TextTimeoutMinutes) * time.Minute
	}
	return limits, nil
}

// cloudAgentStepOutputBudget 把生效上限折算成本步请求要带的 maxOutputTokens：
// 放大档（空输出/超时升级重试）不能突破管理员配置的上限——它只换来"关掉思考"这一次机会；
// 原值为 0（不限制）时用兜底值，让重试仍然有界。
func cloudAgentStepOutputBudget(limits cloudAgentStepLimits, boosted bool) int {
	if !boosted {
		return limits.OutputTokens
	}
	if limits.OutputTokens <= 0 {
		return cloudAgentStepBoostFallbackTokens
	}
	return min(limits.OutputTokens, platform.MaxRuntimeAgentStepOutputTokens)
}
