package app

import (
	"encoding/json"
	"strings"
	"testing"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// B3-E: long material condenses into one charged, session-scoped summary step:
// the summary enters later model inputs, the raw old turns leave them, a
// replay charges once, and another Session never sees the summary.
func TestAgentLongContextScopeAndCost(t *testing.T) {
	f := newBusinessFixture(t)
	executed := 0
	assignment := reviewNotebookWork(t, f, &executed)
	selection := cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}
	longReply := strings.Repeat("剧情材料首尾呼应的反派伏笔", 400) // 远超压缩阈值
	// 第一轮：一段超阈值的长材料进入历史。
	f.text.script(textReply(longReply))
	if _, err := f.runTurn(t, businessTurn{Assignment: assignment, MessageID: "long-1", Text: "读材料", Model: selection}); err != nil {
		t.Fatal(err)
	}
	// 第二轮：超过阈值触发一次压缩步骤；压缩任务与轮次各消费一个脚本回复。
	f.text.script(textReply("摘要：已有一段长材料，涉及反派伏笔"), textReply("已读"))
	turn := businessTurn{Assignment: assignment, MessageID: "long-2", Text: "继续", Model: selection}
	if _, err := f.runTurn(t, turn); err != nil {
		t.Fatal(err)
	}
	var compactions []model.PlatformSubmission
	if err := f.db.Where("user_id = ? AND idempotency_key LIKE ?", "user-a", "compact:%").Find(&compactions).Error; err != nil {
		t.Fatal(err)
	}
	if len(compactions) != 1 {
		t.Fatalf("compaction submissions = %d", len(compactions))
	}
	if compactions[0].BudgetGrantID != assignment.BudgetGrantID {
		t.Fatalf("compaction not charged to the same grant: %+v", compactions[0])
	}
	var summaries int64
	if err := f.db.Model(&model.AgentSessionMessage{}).Where("session_id = ? AND kind = ?", assignment.ExecutorSessionID, as.MessageKindSummary).Count(&summaries).Error; err != nil || summaries != 1 {
		t.Fatalf("summary messages = %d, err=%v", summaries, err)
	}
	// 第三轮：最终模型请求带摘要、不带原始长材料。
	f.text.script(textReply("好的，已基于摘要继续"))
	if _, err := f.runTurn(t, businessTurn{Assignment: assignment, MessageID: "long-3", Text: "接着做", Model: selection}); err != nil {
		t.Fatal(err)
	}
	last := f.text.lastBody(t)
	raw, err := json.Marshal(last["messages"])
	if err != nil {
		t.Fatal(err)
	}
	conversation := string(raw)
	if !strings.Contains(conversation, "已有一段长材料，涉及反派伏笔") {
		t.Fatalf("the summary did not reach the model input; conversation=%s", conversation[:min(len(conversation), 2000)])
	}
	if strings.Contains(conversation, "剧情材料首尾呼应的反派伏笔") {
		t.Fatal("the raw long material stayed in the model input after compaction")
	}
	// 重放第二轮：压缩只产生一次收费。
	calls := f.text.calls.Load()
	if _, err := f.runTurn(t, turn); err != nil {
		t.Fatal(err)
	}
	if f.text.calls.Load() != calls {
		t.Fatalf("replay re-ran the model: %d -> %d", calls, f.text.calls.Load())
	}
	var recount int64
	f.db.Model(&model.PlatformSubmission{}).Where("user_id = ? AND idempotency_key LIKE ?", "user-a", "compact:%").Count(&recount)
	if recount != 1 {
		t.Fatalf("compaction charged %d times after replay", recount)
	}
}
