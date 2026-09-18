package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// slimFixture 造一份"每一步都带增量、压力、审批"的长会话事件日志。
func slimFixture(steps int, withPendingStep bool) *cloudAgentRuntime {
	state := &cloudAgentRuntime{Decisions: map[string]string{}, Events: []CloudAgentEvent{}}
	emit := func(kind string, payload map[string]any) {
		state.Events = append(state.Events, CloudAgentEvent{
			EventID: "run:" + kind, RunID: "run", Seq: len(state.Events) + 1, Type: kind, Payload: payload,
		})
	}
	for step := 0; step < steps; step++ {
		for delta := 0; delta < 20; delta++ {
			emit("reasoning_delta", map[string]any{"messageId": "m", "text": strings.Repeat("想", 400)})
		}
		emit("reasoning_message", map[string]any{"messageId": "m", "text": strings.Repeat("想", 400)})
		emit("assistant_delta", map[string]any{"messageId": "m", "text": strings.Repeat("说", 80)})
		emit("assistant_message", map[string]any{"messageId": "m", "text": strings.Repeat("说", 200)})
		emit("context_pressure", map[string]any{
			"estimatedInputTokens": 7000, "breakdown": map[string]any{"buckets": []any{strings.Repeat("x", 900)}},
		})
		emit("canvas_updated", map[string]any{
			"canvasId": "canvas", "text": "已写入", "actions": []any{"update_node"},
			"canvasPatch": map[string]any{"canvasId": "canvas", "nodes": []any{strings.Repeat("y", 3000)}},
		})
		emit("approval_requested", map[string]any{
			"approvalId": "a", "toolName": "canvas_apply_ops", "arguments": strings.Repeat("z", 1500),
			"preview": map[string]any{"summary": "修改节点"},
		})
		emit("approval_decided", map[string]any{
			"approvalId": "a", "toolName": "canvas_apply_ops", "decision": "approve",
			"arguments": strings.Repeat("z", 1500), "preview": map[string]any{"summary": "修改节点"},
		})
		emit("tool_completed", map[string]any{"toolName": "canvas_apply_ops", "text": "工具执行成功"})
	}
	if withPendingStep {
		// 正在进行的这一步：增量还有文本，不能被清掉（界面正在流式显示）。
		emit("reasoning_delta", map[string]any{"messageId": "m2", "text": "正在进行中的思考"})
	}
	return state
}

// 事件瘦身必须显著降低体积，且只删重复/被取代的字段。
func TestCloudAgentSlimEventHistoryReducesWithoutLosingSemantics(t *testing.T) {
	state := slimFixture(6, true)
	before := cloudAgentEventHistoryBytes(state)
	if !cloudAgentSlimEventHistory(state, false) {
		t.Fatal("长事件日志应当被瘦身")
	}
	after := cloudAgentEventHistoryBytes(state)
	if after > before/2 {
		t.Fatalf("瘦身效果不足: %d → %d 字节（实测增量文本是主要增长源，至少要砍一半）", before, after)
	}
	// seq 与 EventID 绝不能动（SSE 断线重连的游标就是 seq）。
	for index, event := range state.Events {
		if event.Seq != index+1 || event.EventID == "" || event.RunID != "run" {
			t.Fatalf("事件序被改动: %+v", event)
		}
	}
	counts := map[string]int{}
	trimmedDeltas, keptDeltas := 0, 0
	breakdowns, patches, decidedArgs := 0, 0, 0
	for _, event := range state.Events {
		counts[event.Type]++
		switch event.Type {
		case "reasoning_delta":
			if text, _ := event.Payload["text"].(string); text == "" {
				trimmedDeltas++
			} else {
				keptDeltas++
			}
		case "context_pressure":
			if _, ok := event.Payload["breakdown"]; ok {
				breakdowns++
			}
		case "canvas_updated":
			if _, ok := event.Payload["canvasPatch"]; ok {
				patches++
			}
			if stringValue(event.Payload["text"]) == "" {
				t.Fatal("瘦身删掉了给用户看的事件文案")
			}
		case "approval_decided":
			if arguments, _ := event.Payload["arguments"].(string); arguments != "" {
				decidedArgs++
			}
		}
	}
	if counts["reasoning_message"] != 6 || counts["tool_completed"] != 6 {
		t.Fatalf("结构性事件被删了: %+v", counts)
	}
	if trimmedDeltas == 0 || keptDeltas != 1 {
		t.Fatalf("增量清理不符合预期: 清理 %d / 保留 %d（只应保留进行中那一条）", trimmedDeltas, keptDeltas)
	}
	if breakdowns != 1 {
		t.Fatalf("占用分布只应保留最新一条，实际 %d", breakdowns)
	}
	if patches != 3 {
		t.Fatalf("画布增量应保留最近 3 份，实际 %d", patches)
	}
	if decidedArgs > cloudAgentApprovalKeep {
		t.Fatalf("已决审批的原始参数只应保留最近 %d 条，实际 %d 条", cloudAgentApprovalKeep, decidedArgs)
	}
	// 幂等：再跑一次不应继续变。
	size := cloudAgentEventHistoryBytes(state)
	if cloudAgentSlimEventHistory(state, false) {
		t.Fatal("第二次瘦身不该再改动")
	}
	if cloudAgentEventHistoryBytes(state) != size {
		t.Fatal("幂等性被破坏")
	}
}

// 媒体审批的原始参数要留给客户端做设置比对，不能被清。
func TestCloudAgentSlimKeepsMediaApprovalArguments(t *testing.T) {
	state := &cloudAgentRuntime{Events: []CloudAgentEvent{
		{RunID: "run", Seq: 1, EventID: "e1", Type: "approval_decided", Payload: map[string]any{
			"approvalId": "a", "toolName": "generate_media", "decision": "approve",
			"arguments": `{"modelId":"m","durationSeconds":5}`,
		}},
	}}
	cloudAgentSlimEventHistory(state, false)
	if arguments, _ := state.Events[0].Payload["arguments"].(string); arguments == "" {
		t.Fatal("媒体审批参数被误删，客户端设置比对会失效")
	}
}

// 回归守卫：一轮 24 步的会话（每步都带增量/压力/审批）在瘦身后必须远低于 512KB 守卫。
func TestCloudAgentSlimKeepsLongRunUnderStateGuard(t *testing.T) {
	state := slimFixture(24, false)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 512<<10 {
		t.Fatalf("fixture 应当先超过 512KB 守卫才能证明瘦身有效，实际 %d 字节", len(raw))
	}
	cloudAgentSlimEventHistory(state, false)
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 300<<10 {
		t.Fatalf("瘦身后仍然太大: %d 字节", len(raw))
	}
	t.Logf("24 步会话事件日志: 瘦身后 %d 字节", len(raw))
}
