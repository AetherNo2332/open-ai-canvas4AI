package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// cloudAgentBudgetTestService 建一个"只有一个渠道模型"的最小服务，用于验证预算解析。
// window 为 0 表示渠道没声明上下文窗口（此时必须退回字节兜底，不能编一个窗口）。
func cloudAgentBudgetTestService(t *testing.T, window, reserved, maxOutput int) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "budget.db")+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(database.Models()...); err != nil {
		t.Fatal(err)
	}
	for _, item := range []any{
		&model.ModelChannel{ID: "channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "预算测试"},
		&model.ChannelModel{
			ID: "cm", ChannelID: "channel", ModelKey: "text-test", Capability: "text",
			Protocol:             model.ChannelInterfaceChatCompletion,
			CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, cloudAgentBudgetTestCapability(window, reserved, maxOutput, "text-test")),
			BillingMode:          "fixed_request", UnitPriceMicrocredits: 100, PriceConfigured: true, Enabled: true,
		},
	} {
		if err = db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &Service{repo: repository.New(db)}, db
}

func cloudAgentBudgetTestCapability(window, reserved, maxOutput int, modelName string) *ModelCapabilityConfig {
	config := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), modelName)
	config.Text.ContextWindowTokens = window
	config.Text.ReservedOutputTokens = reserved
	config.Text.MaxOutputTokens = maxOutput
	return config
}

func TestCloudAgentContextBudgetMath(t *testing.T) {
	budget, ok := cloudAgentContextBudgetFor(128000, 16384, "channel-model")
	if !ok {
		t.Fatal("declared window rejected")
	}
	if budget.OverheadTokens != 5120 || budget.InputBudgetTokens != 106496 {
		t.Fatalf("input budget = %+v", budget)
	}
	if wantCompactAt := budget.InputBudgetTokens * cloudAgentCompactionPercent / 100; budget.CompactAtTokens != wantCompactAt {
		t.Fatalf("compact line %d is not 85%% of the input budget %d", budget.CompactAtTokens, budget.InputBudgetTokens)
	}
	if budget.CompactAtTokens != 90521 {
		t.Fatalf("compact line = %d, want 90521", budget.CompactAtTokens)
	}
	if budget.Source != "channel-model" {
		t.Fatalf("budget source = %q", budget.Source)
	}
	// 小窗口的 overhead 被抬到下限：窗口 8192 时按 4% 只有 327，必须补到 4096。
	small, ok := cloudAgentContextBudgetFor(8192, 1024, "channel-model")
	if !ok || small.OverheadTokens != cloudAgentBudgetMinOverheadTokens || small.InputBudgetTokens != 8192-1024-4096 {
		t.Fatalf("small window budget = %+v", small)
	}
}

func TestCloudAgentContextBudgetClampsReserveThatEatsTheWindow(t *testing.T) {
	// 预留输出被配成大于窗口时不能算出负预算：夹到半窗，仍留一半给输入。
	budget, ok := cloudAgentContextBudgetFor(65536, 90000, "channel-model")
	if !ok {
		t.Fatal("clamped budget rejected")
	}
	if budget.ReservedOutputTokens != 32768 || budget.InputBudgetTokens <= 0 || budget.CompactAtTokens >= budget.InputBudgetTokens {
		t.Fatalf("reserve clamp = %+v", budget)
	}
}

func TestCloudAgentContextBudgetRejectsUndeclaredWindow(t *testing.T) {
	for _, window := range []int{0, -1, cloudAgentBudgetMinContextWindowTokens - 1, cloudAgentBudgetMaxContextWindowTokens + 1} {
		if _, ok := cloudAgentContextBudgetFor(window, 1024, "channel-model"); ok {
			t.Fatalf("window %d accepted; must fall back to the byte basis", window)
		}
	}
}

func TestCloudAgentEffectiveOutputReserveTakesTheLargerDeclaration(t *testing.T) {
	tests := []struct {
		text   *TextCapabilityConfig
		expect int
	}{
		{nil, 0},
		{&TextCapabilityConfig{}, 0},
		{&TextCapabilityConfig{ReservedOutputTokens: 16384}, 16384},
		{&TextCapabilityConfig{MaxOutputTokens: 8192}, 8192},
		{&TextCapabilityConfig{ReservedOutputTokens: 4096, MaxOutputTokens: 32000}, 32000},
		{&TextCapabilityConfig{ReservedOutputTokens: 32000, MaxOutputTokens: 4096}, 32000},
	}
	for _, tc := range tests {
		if got := cloudAgentEffectiveOutputReserve(tc.text); got != tc.expect {
			t.Fatalf("reserve for %+v = %d, want %d", tc.text, got, tc.expect)
		}
	}
}

func TestCloudAgentRouteIntersectionTakesTheTightestTextRoute(t *testing.T) {
	routes := []cachedLogicalRoute{
		cloudAgentBudgetTestRoute(t, "text", 200000, 16384, 0, "wide"),
		cloudAgentBudgetTestRoute(t, "text", 128000, 8192, 4096, "narrow"),
		cloudAgentBudgetTestRoute(t, "image", 32000, 0, 0, "painter"),
	}
	budget, ok := cloudAgentRouteIntersectionBudget(routes)
	if !ok {
		t.Fatal("route intersection rejected")
	}
	// 窗口取最小 128000；输出预留取最小 8192（能力声明的 4096 小于它，不参与）。
	if budget.ContextWindowTokens != 128000 || budget.ReservedOutputTokens != 8192 {
		t.Fatalf("route intersection = %+v", budget)
	}
	if budget.Source != "logical-route-intersection" || budget.OverheadTokens != 5120 || budget.InputBudgetTokens != 128000-8192-5120 {
		t.Fatalf("route intersection budget = %+v", budget)
	}
}

func TestCloudAgentRouteIntersectionIgnoresRoutesWithoutDeclaredWindow(t *testing.T) {
	routes := []cachedLogicalRoute{
		cloudAgentBudgetTestRoute(t, "text", 0, 0, 0, "unknown-window"),
		cloudAgentBudgetTestRoute(t, "image", 200000, 0, 0, "painter"),
	}
	if _, ok := cloudAgentRouteIntersectionBudget(routes); ok {
		t.Fatal("routes without a declared text window must not produce a token budget")
	}
	// 有窗口的路由与没窗口的路由混在一起时，只按有窗口的那条算交集。
	mixed := append(routes, cloudAgentBudgetTestRoute(t, "text", 65536, 4096, 0, "declared"))
	budget, ok := cloudAgentRouteIntersectionBudget(mixed)
	if !ok || budget.ContextWindowTokens != 65536 || budget.ReservedOutputTokens != 4096 {
		t.Fatalf("mixed route intersection = %+v ok=%v", budget, ok)
	}
}

func cloudAgentBudgetTestRoute(t *testing.T, capability string, window, reserved, maxOutput int, modelKey string) cachedLogicalRoute {
	t.Helper()
	return cachedLogicalRoute{
		CapabilitySpec: CapabilitySpec{Capability: capability, Version: 1},
		ChannelModel: model.ChannelModel{
			ID: "cm-" + modelKey, ModelKey: modelKey, Capability: capability,
			Protocol:             model.ChannelInterfaceChatCompletion,
			CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, cloudAgentBudgetTestCapability(window, reserved, maxOutput, modelKey)),
			Enabled:              true,
		},
	}
}

func cloudAgentBudgetTestState(request CloudAgentRequest, messages []map[string]any) *cloudAgentRuntime {
	return &cloudAgentRuntime{Request: request, Canonical: canonicalAgentRequest{Messages: messages}}
}

func TestCloudAgentCompactionReadingUsesTheInputBudget(t *testing.T) {
	s, _ := cloudAgentBudgetTestService(t, 128000, 16384, 0)
	state := cloudAgentBudgetTestState(
		CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"},
		[]map[string]any{{"role": "user", "content": "继续设计第三幕"}},
	)
	reading, ok := cloudAgentCompactionReading(s, state, state.Canonical)
	if !ok {
		t.Fatal("channel model with a declared window produced no token reading")
	}
	if reading.UsableInputTokens != 106496 || reading.CompactAtTokens != 90521 || reading.OverheadTokens != 5120 || reading.BudgetSource != "channel-model" {
		t.Fatalf("reading = %+v", reading)
	}
	if reading.TokenSource != "estimate" || reading.ProjectedTokens <= 0 || reading.Ratio <= 0 {
		t.Fatalf("reading without a provider anchor = %+v", reading)
	}
}

func TestCloudAgentCompactionFiresAtTheInputBudgetLine(t *testing.T) {
	s, _ := cloudAgentBudgetTestService(t, 128000, 16384, 0)
	canonical := canonicalAgentRequest{SystemPrompt: "system", Messages: []map[string]any{{"role": "user", "content": "继续"}}}
	state := &cloudAgentRuntime{Request: CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"}, Canonical: canonical}
	raw, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	// 上游实测锚点：把"本地估算增量"消成 0，投影就等于锚点读数，触发线可精确断言。
	estimate := estimateCloudAgentTokens(raw)
	reading, ok := cloudAgentCompactionReading(s, state, canonical)
	if !ok {
		t.Fatal("no token reading")
	}
	state.TokenAnchor = &cloudAgentTokenAnchor{Accepted: true, InputTokens: int64(reading.CompactAtTokens), EstimatedTokens: estimate}
	needed, _, _, anchored, hasReading := cloudAgentCompactionDecision(s, state, canonical)
	if !hasReading || anchored.TokenSource != "provider" || anchored.ProjectedTokens != reading.CompactAtTokens {
		t.Fatalf("anchored reading = %+v", anchored)
	}
	// CompactAtTokens 是压缩线的整数下取整展示值；真正判据是 ratio >= 85%，
	// 所以恰好落在下取整值时还不压，多 1 个 token 才压。
	if needed {
		t.Fatalf("projected %d tokens is below the 85%% line and must not compact", anchored.ProjectedTokens)
	}
	state.TokenAnchor.InputTokens = int64(reading.CompactAtTokens) + 1
	needed, _, turns, over, hasReading := cloudAgentCompactionDecision(s, state, canonical)
	if !needed || !hasReading || over.Ratio < cloudAgentCompactionRatio || turns != 1 {
		t.Fatalf("compaction not requested at the line: needed=%v reading=%+v turns=%d", needed, over, turns)
	}
	payload := cloudAgentCompactionEventPayload(over, hasReading, 0, turns)
	if payload["basis"] != "tokens" || payload["compactAtTokens"] != reading.CompactAtTokens || payload["overheadTokens"] != 5120 || payload["budgetSource"] != "channel-model" {
		t.Fatalf("compaction payload = %+v", payload)
	}
}

func TestCloudAgentCompactionFallsBackToBytesWithoutAWindow(t *testing.T) {
	s, _ := cloudAgentBudgetTestService(t, 0, 0, 0)
	state := cloudAgentBudgetTestState(
		CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"},
		[]map[string]any{{"role": "user", "content": "继续"}},
	)
	if _, ok := cloudAgentCompactionReading(s, state, state.Canonical); ok {
		t.Fatal("undeclared window must not produce a token reading")
	}
	needed, _, _, reading, hasReading := cloudAgentCompactionDecision(s, state, state.Canonical)
	if needed || hasReading || reading.ProjectedTokens != 0 {
		t.Fatalf("small conversation compacted without a token basis: needed=%v reading=%+v", needed, reading)
	}
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": strings.Repeat("x", agentcontext.ThresholdBytes+1024)}}
	needed, _, _, _, hasReading = cloudAgentCompactionDecision(s, state, state.Canonical)
	if !needed || hasReading {
		t.Fatalf("byte fallback did not fire: needed=%v hasReading=%v", needed, hasReading)
	}
	payload := cloudAgentCompactionEventPayload(cloudAgentPressureReading{}, false, agentcontext.ThresholdBytes+2048, 1)
	if payload["basis"] != "bytes" {
		t.Fatalf("payload without a reading = %+v", payload)
	}
}

func TestCloudAgentContextPressurePayloadCarriesTheBudgetFields(t *testing.T) {
	pressure := cloudAgentContextPressure{EstimatedInputTokens: 1000, UsableInputTokens: 106496, OverheadTokens: 5120, InputBudgetTokens: 106496, BudgetSource: "channel-model"}
	payload := cloudAgentContextPressurePayload(pressure, nil)
	if payload["overheadTokens"] != 5120 || payload["inputBudgetTokens"] != 106496 || payload["budgetSource"] != "channel-model" {
		t.Fatalf("pressure payload = %+v", payload)
	}
	// 没有预算时不要放出 0 值字段：界面按缺字段显示"未配置模型上限"。
	empty := cloudAgentContextPressurePayload(cloudAgentContextPressure{EstimatedInputTokens: 10}, nil)
	for _, key := range []string{"overheadTokens", "inputBudgetTokens", "budgetSource"} {
		if _, ok := empty[key]; ok {
			t.Fatalf("payload leaked %q without a budget: %+v", key, empty)
		}
	}
}

func TestCloudAgentChannelTextCapabilityFallsBackToRequestKeys(t *testing.T) {
	s, _ := cloudAgentBudgetTestService(t, 64000, 4096, 0)
	// 任务行的 channel_model_id 在画布 Agent 上一直是空的，请求里的渠道 + 模型键必须能兜底解析。
	text := s.cloudAgentChannelTextCapability(nil, CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"})
	if text == nil || text.ContextWindowTokens != 64000 || text.ReservedOutputTokens != 4096 {
		t.Fatalf("channel capability = %+v", text)
	}
	if text := s.cloudAgentChannelTextCapability(nil, CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "missing-model"}); text != nil {
		t.Fatalf("unknown model key resolved to %+v", text)
	}
}
