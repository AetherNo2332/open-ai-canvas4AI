package app

import (
	"strings"
	"testing"
)

func TestEstimateCloudAgentTokensTreatsCJKConservatively(t *testing.T) {
	if got := estimateCloudAgentTokens([]byte("abcd中文")); got != 3 {
		t.Fatalf("estimate = %d, want 3", got)
	}
}

func TestEstimateCloudAgentTokensRoundsASCIIUp(t *testing.T) {
	if got := estimateCloudAgentTokens([]byte("hello")); got != 2 {
		t.Fatalf("estimate = %d, want 2", got)
	}
}

func TestCloudAgentContextPressureSeparatesNextRequestEstimateFromPreviousProviderMeasurement(t *testing.T) {
	pressure := cloudAgentContextPressure{EstimatedInputTokens: 20000, UsableInputTokens: 100000}
	state := &cloudAgentRuntime{TokenAnchor: &cloudAgentTokenAnchor{
		Step: 2, InputTokens: 10500, EstimatedTokens: 19500, Accepted: true,
	}}
	payload := cloudAgentContextPressurePayload(pressure, state)
	if payload["readingScope"] != "next_request" || payload["estimateMethod"] != "local_v1" {
		t.Fatalf("next request scope missing: %+v", payload)
	}
	if payload["providerMeasurementScope"] != "previous_request" {
		t.Fatalf("provider scope = %v", payload["providerMeasurementScope"])
	}
	if payload["estimatedInputTokens"] != 20000 || payload["projectedTokens"] != 11000 {
		t.Fatalf("estimate/projected readings were mixed: %+v", payload)
	}
}

// 设计 §4 的快照字段：读数必须带版本、相位、测量来源、窗口与锚点状态，
// 消费方不该靠字段名猜"这个数属于谁"。
func TestCloudAgentContextPressurePayloadCarriesSnapshotV2Fields(t *testing.T) {
	pressure := cloudAgentContextPressure{
		EstimatedInputTokens: 20000, UsableInputTokens: 100000, ContextWindowTokens: 128000,
		ReservedOutputTokens: 16000, OverheadTokens: 4000, BudgetSource: "channel-model", ModelLimitConfigured: true,
	}
	state := &cloudAgentRuntime{
		Step:        6,
		TokenAnchor: &cloudAgentTokenAnchor{TaskID: "task-anchor", Step: 4, InputTokens: 10500, CachedTokens: 2000, OutputTokens: 300, EstimatedTokens: 19500, Accepted: true},
	}
	payload := cloudAgentContextPressurePayload(pressure, state)
	if payload["schemaVersion"] != 2 || payload["phase"] != "before_request" {
		t.Fatalf("snapshot version/phase 缺失：%+v", payload)
	}
	if payload["measurementSource"] != "provider" || payload["normalizedInputTokens"] != int64(10500) || payload["projectedNextInputTokens"] != 11000 {
		t.Fatalf("测量来源与两种量纲没分开：%+v", payload)
	}
	window, ok := payload["window"].(map[string]any)
	if !ok || window["contextWindowTokens"] != 128000 || window["usableInputTokens"] != 100000 || window["source"] != "channel-model" {
		t.Fatalf("window 快照不对：%+v", payload["window"])
	}
	anchor, ok := payload["anchor"].(map[string]any)
	if !ok || anchor["id"] != "task-anchor" || anchor["valid"] != true || anchor["ageSteps"] != 2 {
		t.Fatalf("anchor 快照不对：%+v", payload["anchor"])
	}
	usage, ok := payload["providerUsage"].(map[string]any)
	if !ok || usage["cacheReadTokens"] != int64(2000) || usage["uncachedInputTokens"] != int64(8500) {
		t.Fatalf("providerUsage 不对：%+v", payload["providerUsage"])
	}
}

// 窗口从未确认变为已确认：要落且只落一条 context_transition（界面据此标"模型窗口已识别"）。
func TestCloudAgentNoteContextWindowResolvedEmitsOnce(t *testing.T) {
	state := &cloudAgentRuntime{}
	pressure := cloudAgentContextPressure{ModelLimitConfigured: true, ContextWindowTokens: 1000000, UsableInputTokens: 967232, BudgetSource: "channel-model"}
	cloudAgentNoteContextWindowResolved("run-1", state, pressure)
	cloudAgentNoteContextWindowResolved("run-1", state, pressure)
	transitions := 0
	for _, event := range state.Events {
		if event.Type == "context_transition" {
			transitions++
			if event.Payload["kind"] != "window_resolved" {
				t.Fatalf("过渡类型不对：%+v", event.Payload)
			}
		}
	}
	if transitions != 1 {
		t.Fatalf("context_transition 条数 = %d，期望 1", transitions)
	}
	if !state.ContextWindowKnown {
		t.Fatal("窗口识别标记没落下")
	}
}

// 锚点治理（设计 §6）：超过 N 步未刷新就作废；模型/渠道变化也作废。
func TestCloudAgentTokenAnchorExpires(t *testing.T) {
	fresh := &cloudAgentRuntime{Step: 10, Request: CloudAgentRequest{Model: "m", ChannelID: "c"}, TokenAnchor: &cloudAgentTokenAnchor{Step: 9, Accepted: true, Signature: "sig"}}
	fresh.TokenAnchor.Signature = cloudAgentStepSignature(fresh)
	cloudAgentExpireTokenAnchor("run-1", fresh)
	if !fresh.TokenAnchor.Accepted || fresh.TokenAnchor.RejectReason != "" {
		t.Fatalf("新鲜锚点不该被作废：%+v", fresh.TokenAnchor)
	}

	stale := &cloudAgentRuntime{Step: 20, Request: CloudAgentRequest{Model: "m", ChannelID: "c"}, TokenAnchor: &cloudAgentTokenAnchor{Step: 10, Accepted: true}}
	stale.TokenAnchor.Signature = cloudAgentStepSignature(stale)
	cloudAgentExpireTokenAnchor("run-1", stale)
	if stale.TokenAnchor.Accepted || !strings.Contains(stale.TokenAnchor.RejectReason, "未刷新") {
		t.Fatalf("超期锚点必须作废：%+v", stale.TokenAnchor)
	}

	changed := &cloudAgentRuntime{Step: 2, Request: CloudAgentRequest{Model: "m2", ChannelID: "c"}, TokenAnchor: &cloudAgentTokenAnchor{Step: 1, Accepted: true, Model: "m", ChannelID: "c"}}
	before := &cloudAgentRuntime{Step: 1, Request: CloudAgentRequest{Model: "m", ChannelID: "c"}}
	changed.TokenAnchor.Signature = cloudAgentStepSignature(before)
	cloudAgentExpireTokenAnchor("run-1", changed)
	if changed.TokenAnchor.Accepted || !strings.Contains(changed.TokenAnchor.RejectReason, "模型已变化") {
		t.Fatalf("换模型后锚点必须作废：%+v", changed.TokenAnchor)
	}
	kinds := []string{}
	for _, event := range changed.Events {
		if event.Type == "context_transition" {
			kinds = append(kinds, stringValue(event.Payload["kind"]))
		}
	}
	if len(kinds) != 1 || kinds[0] != "model_changed" {
		t.Fatalf("换模型应落 model_changed 过渡：%v", kinds)
	}
}
