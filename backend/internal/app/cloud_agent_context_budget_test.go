package app

import (
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

func TestCloudAgentContextBudgetKeepsModelWindowAndOutputSeparate(t *testing.T) {
	budget := cloudAgentContextBudgetFor(1_000_000, 64_000, "channel-model")
	if budget.ContextWindowTokens != 1_000_000 || budget.MaxOutputTokens != 64_000 {
		t.Fatalf("budget = %#v", budget)
	}
	if budget.InputBudgetTokens <= 900_000 || budget.CompactAtTokens >= budget.InputBudgetTokens {
		t.Fatalf("input budget did not preserve large model window: %#v", budget)
	}
	if budget.Source != "channel-model" {
		t.Fatalf("source = %q", budget.Source)
	}
}

func TestCloudAgentContextBudgetUsesChannelModelCapability(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	capability.Text.ContextWindowTokens = 1_000_000
	capability.Text.MaxOutputTokens = 64_000
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, capability)).Error; err != nil {
		t.Fatal(err)
	}

	budget := s.cloudAgentContextBudgetForRequest(CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"})
	if budget.Source != "channel-model" || budget.ContextWindowTokens != 1_000_000 || budget.MaxOutputTokens != cloudAgentStepMaxOutputTokens || budget.ReservedOutputTokens != cloudAgentStepMaxOutputTokens {
		t.Fatalf("channel capability budget = %#v", budget)
	}
	if budget.InputBudgetTokens <= 900_000 {
		t.Fatalf("channel capability did not expose the large window: %#v", budget)
	}
}

func TestCloudAgentContextBudgetUsesLogicalRouteSafeIntersection(t *testing.T) {
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	first := *capability.Text
	first.ContextWindowTokens = 1_000_000
	first.MaxOutputTokens = 64_000
	second := first
	second.ContextWindowTokens = 512_000
	second.MaxOutputTokens = 32_000

	s, _, _, _ := creationTestService(t)
	s.routeCatalogTTL = time.Hour
	s.routeCatalog = &routeCatalogSnapshot{LoadedAt: time.Now(), Models: map[string]cachedLogicalModel{
		"logical-text": {
			Routes: []cachedLogicalRoute{
				{CapabilitySpec: CapabilitySpec{Capability: "text"}, ChannelModel: model.ChannelModel{Capability: "text", CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, &ModelCapabilityConfig{Version: 1, Text: &first})}},
				{CapabilitySpec: CapabilitySpec{Capability: "text"}, ChannelModel: model.ChannelModel{Capability: "text", CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, &ModelCapabilityConfig{Version: 1, Text: &second})}},
			},
		},
	}}

	budget := s.cloudAgentContextBudgetForRequest(CloudAgentRequest{LogicalModelID: "logical-text"})
	if budget.Source != "logical-route-intersection" || budget.ContextWindowTokens != 512_000 || budget.MaxOutputTokens != cloudAgentStepMaxOutputTokens {
		t.Fatalf("logical route intersection = %#v", budget)
	}
}

func TestCloudAgentEstimatedTokensIsConservativeForChinese(t *testing.T) {
	english := cloudAgentEstimatedTokens([]byte(strings.Repeat("word ", 1000)))
	chinese := cloudAgentEstimatedTokens([]byte(strings.Repeat("中文", 1000)))
	if chinese <= english {
		t.Fatalf("Chinese estimate = %d, English estimate = %d", chinese, english)
	}
}

func TestFitCloudAgentModelContextUsesTokenBudget(t *testing.T) {
	request := canonicalAgentRequest{Messages: []map[string]any{{"role": "user", "content": strings.Repeat("中文", 5000)}}}
	if err := fitCloudAgentModelContext(&request, 1_000); err == nil {
		t.Fatal("expected token budget error")
	}
	if err := fitCloudAgentModelContext(&request, 20_000); err != nil {
		t.Fatalf("large token budget should pass: %v", err)
	}
}

// textRouteWithWindow 构造一条逻辑模型可用 text 路由，其渠道模型声明了窗口与输出预留。
func textRouteWithWindow(t *testing.T, window, reservedOutput int) cachedLogicalRoute {
	t.Helper()
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-model")
	if profile == nil || profile.Text == nil {
		t.Fatal("text capability profile expected")
	}
	profile.Text.ContextWindowTokens = window
	profile.Text.MaxOutputTokens = reservedOutput
	return cachedLogicalRoute{
		CapabilitySpec: CapabilitySpec{Version: 1, Capability: "text"},
		ChannelModel: model.ChannelModel{
			Capability:           "text",
			Protocol:             model.ChannelInterfaceChatCompletion,
			ModelKey:             "text-model",
			CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, profile),
		},
	}
}

// TestCloudAgentRouteIntersectionBudgetUsesPerRouteInputCapacity 覆盖
// TestCloudAgentContextBudgetUsesLogicalRouteSafeIntersection 漏掉的**交叉**组合：
// 上面那个用例里最小窗口与最小输出恰好落在同一条路由上，所以"分别取两个最小值"
// 与"逐条算可用输入再取最小值"给出相同结果，缺陷不会被发现。
//
// 512k/64k 与 1M/16k 才暴露问题：分别取最小值得到 512000-16000-20480 = 475520，
// 而 512k 那条路由实际只能吃 512000-64000-20480 = 427520。按 475520 装配的请求
// 最终落到 512k 路由上就会被上游拒绝——这正是"路由交集"要防的事。
func TestCloudAgentRouteIntersectionBudgetUsesPerRouteInputCapacity(t *testing.T) {
	routes := []cachedLogicalRoute{
		textRouteWithWindow(t, 512_000, 64_000),
		textRouteWithWindow(t, 1_000_000, 16_000),
	}
	budget, ok := cloudAgentRouteIntersectionBudget(routes)
	if !ok {
		t.Fatal("two declared text routes must produce a budget")
	}
	const smallestRouteCapacity = 512_000 - 64_000 - 20_480 // overhead(512k) = 512000/25
	if budget.InputBudgetTokens > smallestRouteCapacity {
		t.Fatalf("input budget %d exceeds the smallest route's real capacity %d",
			budget.InputBudgetTokens, smallestRouteCapacity)
	}
	if budget.InputBudgetTokens != smallestRouteCapacity {
		t.Fatalf("input budget = %d, want %d (the smallest route's capacity)",
			budget.InputBudgetTokens, smallestRouteCapacity)
	}
	// 预算必须自洽：窗口 - 输出预留 - overhead == 输入预算。
	if got := budget.ContextWindowTokens - budget.ReservedOutputTokens - budget.OverheadTokens; got != budget.InputBudgetTokens {
		t.Fatalf("budget is internally inconsistent: %d-%d-%d = %d, want %d",
			budget.ContextWindowTokens, budget.MaxOutputTokens, budget.OverheadTokens, got, budget.InputBudgetTokens)
	}
	if budget.ContextWindowTokens != 512_000 || budget.ReservedOutputTokens != 64_000 || budget.MaxOutputTokens != 16_000 {
		t.Fatalf("budget must come from the constraining route, got window=%d reserve=%d",
			budget.ContextWindowTokens, budget.MaxOutputTokens)
	}
	if !budget.Configured {
		t.Fatal("a budget derived from declared windows must be marked configured")
	}
}

// TestCloudAgentRouteIntersectionBudgetSkipsUnknownWindows 保持既有规则：
// 未声明窗口的路由不参与，也不能把预算放大。
func TestCloudAgentRouteIntersectionBudgetSkipsUnknownWindows(t *testing.T) {
	routes := []cachedLogicalRoute{
		textRouteWithWindow(t, 0, 0),
		textRouteWithWindow(t, 200_000, 20_000),
	}
	budget, ok := cloudAgentRouteIntersectionBudget(routes)
	if !ok {
		t.Fatal("the declared route must still produce a budget")
	}
	want := 200_000 - 20_000 - 8_000 // overhead(200k) = 200000/25
	if budget.InputBudgetTokens != want {
		t.Fatalf("input budget = %d, want %d from the only declared route", budget.InputBudgetTokens, want)
	}
}

// TestCloudAgentRouteIntersectionBudgetOrderIndependent：结果不得依赖路由缓存顺序。
func TestCloudAgentRouteIntersectionBudgetOrderIndependent(t *testing.T) {
	forward := []cachedLogicalRoute{
		textRouteWithWindow(t, 512_000, 64_000),
		textRouteWithWindow(t, 1_000_000, 16_000),
	}
	reverse := []cachedLogicalRoute{forward[1], forward[0]}
	first, okFirst := cloudAgentRouteIntersectionBudget(forward)
	second, okSecond := cloudAgentRouteIntersectionBudget(reverse)
	if !okFirst || !okSecond {
		t.Fatal("both orders must produce a budget")
	}
	if first != second {
		t.Fatalf("route order changed the budget: %+v vs %+v", first, second)
	}
}

// TestCloudAgentRouteIntersectionBudgetWithoutTextRouteFailsClosed：没有可用 text
// 路由就是"未知"，调用方必须退回字节/条数兜底，而不是拿默认窗口冒充真实能力。
func TestCloudAgentRouteIntersectionBudgetWithoutTextRouteFailsClosed(t *testing.T) {
	if _, ok := cloudAgentRouteIntersectionBudget(nil); ok {
		t.Fatal("no routes must not produce a budget")
	}
	imageOnly := cachedLogicalRoute{CapabilitySpec: CapabilitySpec{Version: 1, Capability: "image"}}
	if _, ok := cloudAgentRouteIntersectionBudget([]cachedLogicalRoute{imageOnly}); ok {
		t.Fatal("a non-text route must not produce a text budget")
	}
	undeclared := []cachedLogicalRoute{textRouteWithWindow(t, 0, 0)}
	if _, ok := cloudAgentRouteIntersectionBudget(undeclared); ok {
		t.Fatal("all-unknown windows must not produce a budget")
	}
}
