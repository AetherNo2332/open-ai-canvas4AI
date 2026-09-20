package app

import (
	"strings"

	"infinite-canvas/backend/internal/model"
)

// 一次模型调用可用的输入预算（token 口径）。
//
// 口径来源：上游 v1.5.7 的窗口算式（context budget）——工具 schema、协议包装与兜底轮
// 都不体现在 canonical 正文里，必须按窗口比例留一份 overhead，否则大窗口模型也用不满容量。
// 我们保留自己的两项语义差异：
//   - 输出预留取"用户填的预留输出"与"能力声明的最大输出"中的较大者（两者都是给输出留的额度）；
//   - 压缩触发线用这份输入预算的 85%，但触发后执行的仍是我们的阶梯（卸载 → 语义压缩 → 降级），
//     而不是上游的"丢完整轮次、丢不动即失败"。
const (
	cloudAgentBudgetMinContextWindowTokens = 4_096
	cloudAgentBudgetMaxContextWindowTokens = 10_000_000
	cloudAgentBudgetMinOverheadTokens      = 4_096
	cloudAgentBudgetMaxOverheadTokens      = 32_768
	cloudAgentBudgetMinInputTokens         = 1_024
)

type cloudAgentContextBudget struct {
	ContextWindowTokens  int    `json:"contextWindowTokens"`
	ReservedOutputTokens int    `json:"reservedOutputTokens"`
	OverheadTokens       int    `json:"overheadTokens"`
	InputBudgetTokens    int    `json:"inputBudgetTokens"`
	CompactAtTokens      int    `json:"compactAtTokens"`
	Source               string `json:"source"`
}

// cloudAgentOverheadTokens 是工具 schema / 协议包装 / 兜底轮的比例预留（窗口的 4%，夹在 4K–32K）。
func cloudAgentOverheadTokens(contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	overhead := contextWindow / 25
	return min(max(overhead, cloudAgentBudgetMinOverheadTokens), cloudAgentBudgetMaxOverheadTokens)
}

// cloudAgentEffectiveOutputReserve 是"给输出留的额度"：用户填的预留输出与能力声明的最大输出取大。
// 两者语义都是"这一步不该拿去装输入的额度"，取大是保守做法。
func cloudAgentEffectiveOutputReserve(text *TextCapabilityConfig) int {
	if text == nil {
		return 0
	}
	return max(text.ReservedOutputTokens, text.MaxOutputTokens)
}

// cloudAgentContextBudgetFor 由窗口与输出预留算出输入预算与压缩触发线。
// 窗口为 0（未声明）或越界时返回 false：调用方应退回字节/条数兜底，而不是编一个窗口。
func cloudAgentContextBudgetFor(contextWindow, reservedOutput int, source string) (cloudAgentContextBudget, bool) {
	if contextWindow < cloudAgentBudgetMinContextWindowTokens || contextWindow > cloudAgentBudgetMaxContextWindowTokens {
		return cloudAgentContextBudget{}, false
	}
	if reservedOutput < 0 {
		reservedOutput = 0
	}
	// 输出预留不得吃掉整个窗口：留出至少一半给输入（与上游的兜底一致）。
	if reservedOutput >= contextWindow {
		reservedOutput = contextWindow / 2
	}
	overhead := cloudAgentOverheadTokens(contextWindow)
	inputBudget := contextWindow - reservedOutput - overhead
	if inputBudget < cloudAgentBudgetMinInputTokens {
		inputBudget = max(cloudAgentBudgetMinInputTokens, contextWindow/2)
	}
	return cloudAgentContextBudget{
		ContextWindowTokens:  contextWindow,
		ReservedOutputTokens: reservedOutput,
		OverheadTokens:       overhead,
		InputBudgetTokens:    inputBudget,
		CompactAtTokens:      inputBudget * cloudAgentCompactionPercent / 100,
		Source:               source,
	}, true
}

// cloudAgentContextBudgetForRequest 解析"下一次文本调用真正会用的预算"。
//
// 逻辑模型取**所有可用 text 路由的最小窗口与最小输出**：路由选择不允许在上下文装配之后
// 换到一个更小的窗口（否则装配按大窗口做、执行按小窗口跑，请求会被上游直接拒）。
func (s *Service) cloudAgentContextBudgetForRequest(task *model.Task, request CloudAgentRequest) (cloudAgentContextBudget, bool) {
	if s == nil || s.repo == nil {
		return cloudAgentContextBudget{}, false
	}
	if id := strings.TrimSpace(request.LogicalModelID); id != "" {
		if snapshot, err := s.routeCatalogSnapshot(); err == nil && snapshot != nil {
			if entry, ok := snapshot.Models[id]; ok {
				if budget, ok := cloudAgentRouteIntersectionBudget(entry.Routes); ok {
					return budget, true
				}
			}
		}
	}
	text := s.cloudAgentChannelTextCapability(task, request)
	if text == nil {
		return cloudAgentContextBudget{}, false
	}
	return cloudAgentContextBudgetFor(
		text.ContextWindowTokens,
		cloudAgentEffectiveOutputReserve(text),
		"channel-model",
	)
}

// cloudAgentChannelTextCapability 解析这次调用所属**渠道模型**的文本能力。
//
// 任务行的 channel_model_id 在画布 Agent 上一直是空的（实测 82 个 agent 任务全为空），
// 所以必须能从请求里的渠道 + 模型键兜底解析，否则模型窗口占比永远显示不出来、
// 压缩判据也只能退回字节口径。逻辑模型没有单一渠道模型，这里返回 nil（走路由交集那条路）。
func (s *Service) cloudAgentChannelTextCapability(task *model.Task, request CloudAgentRequest) *TextCapabilityConfig {
	if s == nil || s.repo == nil {
		return nil
	}
	var channelModel *model.ChannelModel
	switch {
	case task != nil && strings.TrimSpace(task.ChannelModelID) != "":
		channelModel, _ = s.repo.ChannelModel(task.ChannelModelID)
	case strings.TrimSpace(request.ChannelID) != "" && strings.TrimSpace(request.ChannelModelKey) != "":
		channelModel, _ = s.repo.ChannelModelByKeyIncludingDisabled(request.ChannelID, request.ChannelModelKey)
	}
	if channelModel == nil {
		return nil
	}
	config, err := normalizedChannelModelCapability(channelModel)
	if err != nil || config == nil || config.Text == nil {
		return nil
	}
	return config.Text
}

// cloudAgentRouteIntersectionBudget 取所有可用 text 路由的**最小**窗口与**最小**输出预留。
//
// 为什么取交集而不是"随便挑一条"：路由选择发生在上下文装配之后，装配按大窗口做、
// 执行落到小窗口的路由时，请求会被上游直接拒（或者被悄悄截断）。窗口未知（0）的路由不参与，
// 但如果一条可用的 text 路由都没有，就返回 false，让调用方退回字节兜底。
func cloudAgentRouteIntersectionBudget(routes []cachedLogicalRoute) (cloudAgentContextBudget, bool) {
	window, reserve := 0, 0
	for _, route := range routes {
		if normalizeCapability(route.CapabilitySpec.Capability) != "text" {
			continue
		}
		channelModel := route.ChannelModel
		config, err := normalizedChannelModelCapability(&channelModel)
		if err != nil || config == nil || config.Text == nil || config.Text.ContextWindowTokens <= 0 {
			continue
		}
		if window == 0 || config.Text.ContextWindowTokens < window {
			window = config.Text.ContextWindowTokens
		}
		if routeReserve := cloudAgentEffectiveOutputReserve(config.Text); reserve == 0 || routeReserve < reserve {
			reserve = routeReserve
		}
	}
	return cloudAgentContextBudgetFor(window, reserve, "logical-route-intersection")
}
