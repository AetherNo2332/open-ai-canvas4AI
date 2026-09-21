package app

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 一次模型调用可用的输入预算（token 口径）。
//
// 口径来源与上游一致（窗口算式）：工具 schema、协议包装与兜底轮都不体现在 canonical
// 正文里，必须按窗口比例留一份 overhead，否则大窗口模型也用不满容量。
// 与上游的差别是我们 P2 保留的三项语义：
//   - 输出预留取"用户填的预留输出"与"能力声明的最大输出"中的较大者（两者都是给输出留的额度）；
//   - 逻辑模型取**所有可用 text 路由**的最小窗口与最小输出（路由交集），而不是任选一条；
//   - 窗口未知（未声明 / 越界）时 Configured=false：界面不能声称"配置了模型上限"，
//     压缩判据也要退回字节/条数兜底，而不是拿 128k 冒充真实窗口。
const (
	defaultCloudAgentContextWindowTokens = 128_000
	defaultCloudAgentMaxOutputTokens     = 16_384
	minCloudAgentContextWindowTokens     = 4_096
	maxCloudAgentContextWindowTokens     = 10_000_000
	// cloudAgentBudgetMinOverheadTokens / MaxOverheadTokens 是 overhead 的夹取区间。
	cloudAgentBudgetMinOverheadTokens = 4_096
	cloudAgentBudgetMaxOverheadTokens = 32_768
	cloudAgentBudgetMinInputTokens    = 1_024
	// cloudAgentCompactionPercent 是压缩触发线（输入预算的百分比）。
	cloudAgentCompactionPercent = 85
)

type cloudAgentContextBudget struct {
	ContextWindowTokens int
	// MaxOutputTokens 是这一步给输出留的额度（上限而非实际用量）。
	MaxOutputTokens   int
	OverheadTokens    int
	InputBudgetTokens int
	CompactAtTokens   int
	Source            string
	// Configured 为 false 表示没有解析到任何模型声明的窗口，这份预算是兜底默认值。
	Configured bool
}

func defaultCloudAgentContextBudget() cloudAgentContextBudget {
	budget := cloudAgentContextBudgetFor(defaultCloudAgentContextWindowTokens, defaultCloudAgentMaxOutputTokens, "default")
	budget.Configured = false
	return budget
}

// cloudAgentOverheadTokens 是工具 schema / 协议包装与兜底轮的比例预留（窗口的 4%，夹在 4K–32K）。
func cloudAgentOverheadTokens(contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	return min(max(contextWindow/25, cloudAgentBudgetMinOverheadTokens), cloudAgentBudgetMaxOverheadTokens)
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
//
// 入参越界（未声明 0、或超出 4K–10M）时退回兜底默认值并把 Configured 置为 false：
// 上游的单返回值签名要保住（压缩兜底也依赖一个可用的预算），
// 但"这是默认值而不是真实能力"必须能被界面与判据区分出来。
func cloudAgentContextBudgetFor(contextWindow, maxOutput int, source string) cloudAgentContextBudget {
	configured := true
	if contextWindow < minCloudAgentContextWindowTokens || contextWindow > maxCloudAgentContextWindowTokens {
		contextWindow, configured = defaultCloudAgentContextWindowTokens, false
	}
	if maxOutput < 0 {
		maxOutput = 0
	}
	// 输出预留不得吃掉整个窗口：留出至少一半给输入。
	if maxOutput >= contextWindow {
		maxOutput = contextWindow / 2
	}
	overhead := cloudAgentOverheadTokens(contextWindow)
	inputBudget := contextWindow - maxOutput - overhead
	if inputBudget < cloudAgentBudgetMinInputTokens {
		inputBudget = max(cloudAgentBudgetMinInputTokens, contextWindow/2)
	}
	return cloudAgentContextBudget{
		ContextWindowTokens: contextWindow,
		MaxOutputTokens:     maxOutput,
		OverheadTokens:      overhead,
		InputBudgetTokens:   inputBudget,
		CompactAtTokens:     max(cloudAgentBudgetMinInputTokens, inputBudget*cloudAgentCompactionPercent/100),
		Source:              source,
		Configured:          configured,
	}
}

// cloudAgentContextBudgetForRequest 解析"下一次文本调用真正会用的预算"。
// 解析不到真实能力时返回兜底默认预算（Configured=false），调用方据此退回字节兜底。
func (s *Service) cloudAgentContextBudgetForRequest(req CloudAgentRequest) cloudAgentContextBudget {
	if budget, ok := s.cloudAgentResolvedContextBudget(req); ok {
		return budget
	}
	return defaultCloudAgentContextBudget()
}

// cloudAgentResolvedContextBudget 是"能不能给出真实窗口"的判据入口。
//
//   - 逻辑模型：取所有可用 text 路由的**最小**窗口与**最小**输出预留。为什么取交集而不是
//     "随便挑一条"：路由选择发生在上下文装配之后，装配按大窗口做、执行落到小窗口的路由时，
//     请求会被上游直接拒（或者被悄悄截断）。窗口未知（0）的路由不参与；
//     一条可用 text 路由都没有时返回 false，让调用方退回字节/条数兜底。
//   - 渠道模型：任务行的 channel_model_id 在画布 Agent 上一直是空的（实测 82 个 agent 任务全为空），
//     所以必须能从请求里的渠道 + 模型键兜底解析，否则模型窗口占比永远显示不出来、
//     压缩判据也只能退回字节口径。
func (s *Service) cloudAgentResolvedContextBudget(req CloudAgentRequest) (cloudAgentContextBudget, bool) {
	if s == nil || s.repo == nil {
		return cloudAgentContextBudget{}, false
	}
	if id := strings.TrimSpace(req.LogicalModelID); id != "" {
		if snapshot, err := s.routeCatalogSnapshot(); err == nil && snapshot != nil {
			if entry, ok := snapshot.Models[id]; ok {
				if budget, ok := cloudAgentRouteIntersectionBudget(entry.Routes); ok {
					return budget, true
				}
			}
		}
		return cloudAgentContextBudget{}, false
	}
	text := s.cloudAgentChannelTextCapability(req)
	if text == nil {
		return cloudAgentContextBudget{}, false
	}
	budget := cloudAgentContextBudgetFor(text.ContextWindowTokens, cloudAgentEffectiveOutputReserve(text), "channel-model")
	if !budget.Configured {
		return cloudAgentContextBudget{}, false
	}
	return budget, true
}

// cloudAgentChannelTextCapability 解析这次调用所属**渠道模型**的文本能力。
// 逻辑模型没有单一渠道模型，这里返回 nil（走路由交集那条路）。
func (s *Service) cloudAgentChannelTextCapability(request CloudAgentRequest) *TextCapabilityConfig {
	if s == nil || s.repo == nil {
		return nil
	}
	if strings.TrimSpace(request.ChannelID) == "" || strings.TrimSpace(request.ChannelModelKey) == "" {
		return nil
	}
	channelModel, err := s.repo.ChannelModelByKeyIncludingDisabled(request.ChannelID, request.ChannelModelKey)
	if err != nil || channelModel == nil {
		return nil
	}
	config, err := normalizedChannelModelCapability(channelModel)
	if err != nil || config == nil || config.Text == nil {
		return nil
	}
	return config.Text
}

// cloudAgentRouteIntersectionBudget 取所有可用 text 路由的最小窗口与最小输出预留。
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
	if window < minCloudAgentContextWindowTokens {
		return cloudAgentContextBudget{}, false
	}
	return cloudAgentContextBudgetFor(window, reserve, "logical-route-intersection"), true
}

// cloudAgentEstimatedTokens is deliberately conservative for non-ASCII text.
// It is a planning estimate, not a provider tokenizer; underestimation would
// make a request fail only after reaching an upstream provider.
func cloudAgentEstimatedTokens(value []byte) int {
	if len(value) == 0 {
		return 1
	}
	ascii, nonASCII := 0, 0
	for len(value) > 0 {
		r, size := utf8.DecodeRune(value)
		if r == utf8.RuneError && size == 1 {
			nonASCII++
			value = value[1:]
			continue
		}
		if r < 0x80 {
			ascii++
		} else {
			nonASCII++
		}
		value = value[size:]
	}
	return max(1, int(math.Ceil(float64(ascii)/4+float64(nonASCII)*1.5)))
}

func cloudAgentRequestEstimatedTokens(request *canonicalAgentRequest) (int, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	return cloudAgentEstimatedTokens(raw), nil
}

func cloudAgentContextBudgetMessage(budget cloudAgentContextBudget) string {
	return "用户指令与当前执行事实超过模型输入预算（" + formatTokenCount(budget.InputBudgetTokens) + " Token，能力来源：" + strings.TrimSpace(budget.Source) + "），请缩小本轮范围"
}

func formatTokenCount(value int) string {
	if value < 1_000 {
		return strconv.Itoa(value)
	}
	return strconv.Itoa(value/1_000) + "K"
}
