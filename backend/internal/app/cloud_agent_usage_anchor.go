package app

import (
	"encoding/json"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/repository"
)

// cloudAgentCompactionRatio 是触发语义压缩的上下文利用率：有效上游输入用量
// （无实测时采用本地估算）达到**输入预算**（窗口 − 输出预留 − overhead）的比例时，
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

// cloudAgentTokenAnchor 保存最近的上游实测。估算及签名只用于诊断，
// 不叠加到实测，也不能因两者差距较大而否定 provider 用量。
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
	// Model / ChannelID 用于拒绝不同 provider 的旧读数；Signature 保留请求诊断身份。
	Signature string `json:"signature,omitempty"`
	Model     string `json:"model,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
	// ContextWindowTokens 保存实测请求当时的窗口；当前压力分母取当前配置。
	ContextWindowTokens int `json:"contextWindowTokens,omitempty"`
}

// 续轮继承同一模型/渠道的最新用量，独立复制，避免新 run 修改父轮诊断。
func cloudAgentInheritedTokenAnchor(parent *cloudAgentRuntime, request CloudAgentRequest) *cloudAgentTokenAnchor {
	if parent == nil || parent.TokenAnchor == nil || !parent.TokenAnchor.Accepted || parent.TokenAnchor.InputTokens <= 0 {
		return nil
	}
	previous := parent.Request
	if previous.Model != request.Model || previous.ChannelID != request.ChannelID || previous.ChannelModelKey != request.ChannelModelKey || previous.LogicalModelID != request.LogicalModelID {
		return nil
	}
	anchor := *parent.TokenAnchor
	anchor.Step = 0
	return &anchor
}

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

// 换模型或渠道后不再沿用旧 provider 的读数；同一 provider 的实测持续有效。
func cloudAgentExpireTokenAnchor(runID string, state *cloudAgentRuntime) {
	if state == nil {
		return
	}
	if state.LastStepModel == "" && state.LastStepChannelID == "" {
		cloudAgentExpireTokenAnchorForSelection(runID, state)
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

// 逻辑模型/自动路由的选择器不是上游模型键。运行内选择器不变，续轮已在继承时
// 比对完整选择器；实际线路变化由 enqueue 的已解析任务输入重新核验。
func cloudAgentExpireTokenAnchorForSelection(runID string, state *cloudAgentRuntime) {
	if state == nil || state.TokenAnchor == nil {
		return
	}
	model, channelID := firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model), state.Request.ChannelID
	if state.Request.LogicalModelID != "" || channelID == "" {
		model, channelID = state.TokenAnchor.Model, state.TokenAnchor.ChannelID
	}
	cloudAgentExpireTokenAnchorForRequest(runID, state, 0, "", model, channelID)
}

// 系统提示、工具、窗口和步数变化不删除已知用量。窗口只更新压力的分母。
func cloudAgentExpireTokenAnchorForRequest(runID string, state *cloudAgentRuntime, _ int, _ string, model, channelID string) {
	if state == nil || state.TokenAnchor == nil || !state.TokenAnchor.Accepted {
		return
	}
	anchor := state.TokenAnchor
	switch {
	case anchor.Model != "" && model != "" && anchor.Model != model:
		anchor.RejectReason = "模型已变化，锚点作废"
		state.event(runID, "context_transition", map[string]any{"kind": "model_changed", "reason": "anchor_signature_changed", "text": anchor.RejectReason})
	case anchor.ChannelID != "" && channelID != "" && anchor.ChannelID != channelID:
		anchor.RejectReason = "供应线路已变化，锚点作废"
		state.event(runID, "context_transition", map[string]any{"kind": "route_changed", "reason": "anchor_signature_changed", "text": anchor.RejectReason})
	default:
		return
	}
	anchor.Accepted = false
}

// recordCloudAgentTokenAnchor 用上一步的上游实测用量给上下文压力定锚。
// 幂等：同一任务只采信一次；用量缺失时保留已有实测。
// userID 用于限定"这条调用确实是本用户跑出来的"（上游 #601 的口径），不能只按 taskId 取。
func (s *Service) recordCloudAgentTokenAnchor(userID string, state *cloudAgentRuntime) {
	if s == nil {
		return
	}
	s.recordCloudAgentTokenAnchorWithRepository(s.repo, userID, state)
}

// recordCloudAgentTokenAnchorWithRepository lets the Pi checkpoint path read usage
// through its current transaction, so the anchor and assistant checkpoint commit
// atomically without querying the database through a second connection.
func (s *Service) recordCloudAgentTokenAnchorWithRepository(repo *repository.Repository, userID string, state *cloudAgentRuntime) {
	if s == nil || state == nil {
		return
	}
	// 路由回退会改写实际任务输入，即使没有 usage 也必须核验完成时的 provider。
	if repo != nil && state.LastStepTaskID != "" && state.LastStepOperation == cloudAgentStepOperation {
		if task, err := repo.TaskForUser(userID, state.LastStepTaskID); err == nil && task.Status == model.TaskStatusSucceeded {
			var input struct {
				Config map[string]any `json:"config"`
			}
			if json.Unmarshal([]byte(task.InputJSON), &input) == nil {
				state.LastStepModel = firstNonEmpty(stringValue(input.Config["model"]), state.LastStepModel)
				state.LastStepChannelID = firstNonEmpty(stringValue(input.Config["channelId"]), state.LastStepChannelID)
			}
		}
	}
	// 用量必须属于当前 provider。取不到新用量时保留同一 provider 的上次实测。
	cloudAgentExpireTokenAnchor(state.RuntimeRunID, state)
	if repo == nil || state.LastStepTaskID == "" {
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
	log, ok, err := repo.APICallLogUsageForTask(userID, state.LastStepTaskID)
	if err != nil || !ok {
		return
	}
	// 成功回退可能改写任务线路；本次成功调用的身份和实测一起成为最新权威。
	state.LastStepModel = firstNonEmpty(log.Model, state.LastStepModel)
	state.LastStepChannelID = firstNonEmpty(log.ChannelID, state.LastStepChannelID)
	anchor := &cloudAgentTokenAnchor{
		TaskID: state.LastStepTaskID, Step: state.Step, InputTokens: log.InputTokens,
		CachedTokens: log.CachedTokens, OutputTokens: log.OutputTokens,
		EstimatedTokens: state.LastStepEstimate, SourceBytes: state.LastStepSourceBytes,
		Signature: state.LastStepSignature, Model: state.LastStepModel, ChannelID: state.LastStepChannelID,
		ContextWindowTokens: state.LastStepWindowTokens,
		Accepted:            true,
	}
	state.TokenAnchor = anchor
	// 完成步骤时立即广播实测，包括最后一步；不等下一次请求前的压力事件。
	// 使用发送时保存的预算，避免事务内通过另一条数据库连接重新查询渠道配置。
	pressure := cloudAgentContextPressure{EstimatedInputTokens: state.LastStepEstimate}
	if state.LastStepPressure != nil {
		pressure = *state.LastStepPressure
	}
	payload := cloudAgentContextPressurePayload(pressure, state)
	payload["phase"], payload["requestId"] = "after_request", anchor.TaskID
	payload["providerMeasurementScope"] = "completed_request"
	state.event(state.RuntimeRunID, "context_pressure", payload)
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
