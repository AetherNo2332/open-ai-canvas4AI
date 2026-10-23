package app

import (
	"strings"
	"testing"
)

func TestCloudAgentPendingPlanAllowsFinishWithoutNudge(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	agentWithPendingPlan(t, s, db, root)
	run, state := agentFinishRunForTest(t, s, db, root.ID, "finish-with-pending-plan", "本轮分析完成，生成工作留待后续。")
	if run.Status != "completed" {
		t.Fatalf("待办状态不应阻止收尾：%s", run.Status)
	}
	if len(state.Plan) != 1 || state.Plan[0].Status != "doing" {
		t.Fatalf("服务端不应代模型清空或完成待办：%+v", state.Plan)
	}
	if len(agentFinalReplies(state)) != 1 || len(agentEventPayloads(state, "completion_blocked")) != 0 || state.CompletionNudges != 0 {
		t.Fatalf("应发布一次最终答复且不催办：events=%+v nudges=%d", state.Events, state.CompletionNudges)
	}
	for _, message := range state.Canonical.Messages {
		if strings.Contains(stringField(message, "content"), `"kind":"pending_plan"`) {
			t.Fatalf("不得注入待办催办：%+v", message)
		}
	}
	completed := agentEventPayloads(state, "tool_completed")
	if len(completed) != 1 {
		t.Fatalf("缺少唯一收尾回执：%+v", completed)
	}
	receipt, _ := completed[0]["result"].(map[string]any)
	if receipt["completionBlocked"] != false || receipt["committed"] != true || receipt["terminalStatus"] != "completed" {
		t.Fatalf("应持久化成功收尾回执：%+v", receipt)
	}
}
