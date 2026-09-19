package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

// 事件出表：状态只留尾部缓存，全量进 cloud_agent_run_events；
// 重复 flush 幂等、序号不变，运行详情按页读取。
func TestCloudAgentEventsFlushTailAndPage(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟升级前的旧状态：事件全在 state_json 里，水位为 0。
	state.Events = nil
	state.EventSeqBase, state.EventFlushedSeq = 0, 0
	// 事件数特意超过默认页大小，才能同时验证"尾部裁剪"与"默认页从表里补齐"。
	total := cloudAgentRunEventPageLimit + 25
	for index := 0; index < total; index++ {
		state.event(run.ID, "tool_completed", map[string]any{"toolName": "canvas_get_state", "text": strings.Repeat("读", 40)})
	}
	if len(state.Events) != total {
		t.Fatalf("前置事件数不对：%d", len(state.Events))
	}

	if err := s.cloudAgentFlushRunEvents(run, &state); err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.CloudAgentRunEventCount("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if int(stored) != total {
		t.Fatalf("入库条数 = %d，期望 %d", stored, total)
	}
	if len(state.Events) != cloudAgentEventTailLimit {
		t.Fatalf("状态尾部应裁到 %d 条，实际 %d", cloudAgentEventTailLimit, len(state.Events))
	}
	if state.EventSeqBase != total-cloudAgentEventTailLimit {
		t.Fatalf("水位 = %d，期望 %d", state.EventSeqBase, total-cloudAgentEventTailLimit)
	}
	if err := validateCloudAgentRuntime(run, &state); err != nil {
		t.Fatalf("出表后校验失败: %v", err)
	}

	// 幂等：再次 flush 不应重复入库、也不应改动尾部。
	before := len(state.Events)
	if err := s.cloudAgentFlushRunEvents(run, &state); err != nil {
		t.Fatal(err)
	}
	storedAgain, _ := s.repo.CloudAgentRunEventCount("user", run.ID)
	if storedAgain != stored || len(state.Events) != before {
		t.Fatalf("flush 不幂等：stored %d → %d，tail %d → %d", stored, storedAgain, before, len(state.Events))
	}

	// 默认视图：返回最近一页（尾部 + 表里更早的部分），并给出累计条数。
	events := s.cloudAgentRunEventsForView("user", run, &state, 0, 0)
	if len(events) != cloudAgentRunEventPageLimit {
		t.Fatalf("默认页大小 = %d，期望 %d", len(events), cloudAgentRunEventPageLimit)
	}
	if events[len(events)-1].Seq != total || events[0].Seq != total-cloudAgentRunEventPageLimit+1 {
		t.Fatalf("默认页序号不连续：首 %d 尾 %d", events[0].Seq, events[len(events)-1].Seq)
	}
	if count := s.cloudAgentRunEventCount("user", run, &state); count != total {
		t.Fatalf("累计条数 = %d，期望 %d", count, total)
	}

	// 增量：sinceSeq 之后只返回更新的部分（表 + 尾部都要覆盖）。
	delta := s.cloudAgentRunEventsForView("user", run, &state, total-3, 0)
	if len(delta) != 3 || delta[0].Seq != total-2 {
		t.Fatalf("增量视图不对：%+v", delta)
	}
	// 增量跨过尾部边界：37 之后的 3 条来自表 + 尾部
	delta = s.cloudAgentRunEventsForView("user", run, &state, state.EventSeqBase-2, 0)
	if len(delta) != 2+len(state.Events) {
		t.Fatalf("跨边界增量条数 = %d，期望 %d", len(delta), 2+len(state.Events))
	}
	for index := 1; index < len(delta); index++ {
		if delta[index].Seq != delta[index-1].Seq+1 {
			t.Fatalf("增量序号不连续：%d → %d", delta[index-1].Seq, delta[index].Seq)
		}
	}
}

// 表里没有记录（升级前结束、之后没保存过的旧运行）时，运行详情仍要能读到 state_json 里的事件。
func TestCloudAgentRunEventsFallBackToLegacyState(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Events = nil
	for index := 0; index < 5; index++ {
		state.event(run.ID, "assistant_message", map[string]any{"text": "legacy"})
	}
	events := s.cloudAgentRunEventsForView("user", run, &state, 0, 0)
	if len(events) != 5 || events[0].Seq != 1 {
		t.Fatalf("旧运行回退失败：%+v", events)
	}
	delta := s.cloudAgentRunEventsForView("user", run, &state, 2, 0)
	if len(delta) != 3 || delta[0].Seq != 3 {
		t.Fatalf("旧运行增量回退失败：%+v", delta)
	}
}

// 保留期清理：只删过期事件，未到期的不动。
func TestCloudAgentRunEventRetentionPurge(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	now := time.Now().UTC()
	rows := []model.CloudAgentRunEvent{
		{RunID: root.ID, UserID: "user", Seq: 1, EventID: "e1", Type: "tool_completed", Payload: "{}", CreatedAt: now, ExpiresAt: now.Add(-time.Hour)},
		{RunID: root.ID, UserID: "user", Seq: 2, EventID: "e2", Type: "tool_completed", Payload: "{}", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := s.repo.PurgeExpiredCloudAgentRunEvents(now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("删除条数 = %d，期望 1", deleted)
	}
	remaining, _ := s.repo.CloudAgentRunEventCount("user", root.ID)
	if remaining != 1 {
		t.Fatalf("剩余条数 = %d，期望 1", remaining)
	}
}

// 画布删除只清归属、不删审计事件。
func TestCloudAgentRunEventCanvasScopeClearedOnCanvasDelete(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	row := model.CloudAgentRunEvent{RunID: root.ID, UserID: "user", CanvasID: "canvas-x", Seq: 1, EventID: "e1", Type: "tool_completed", Payload: "{}", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.ClearCloudAgentRunEventCanvasScope("user", "canvas-x"); err != nil {
		t.Fatal(err)
	}
	var stored model.CloudAgentRunEvent
	if err := db.First(&stored, "run_id = ? AND seq = ?", root.ID, 1).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CanvasID != "" {
		t.Fatalf("画布归属未清理：%q", stored.CanvasID)
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(stored.Payload), &payload); err != nil {
		t.Fatalf("事件内容被破坏：%v", err)
	}
}
