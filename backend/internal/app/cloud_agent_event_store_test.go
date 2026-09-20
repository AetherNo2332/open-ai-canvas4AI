package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 消息与事件都不再进检查点：保存时与检查点同事务落库，
// 解码时从表重建（消息全量、事件一窗），运行详情按页读取。
func TestCloudAgentEventsPersistWithCheckpointAndPage(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	// 事件数特意超过默认页大小，才能同时验证"事件窗口"与"默认页从表里补齐"。
	state.Events = nil
	state.EventSeqBase = 0
	total := cloudAgentRunEventPageLimit + 25
	for index := 0; index < total; index++ {
		state.event(run.ID, "tool_completed", map[string]any{"toolName": "canvas_get_state", "text": strings.Repeat("读", 40)})
	}
	if len(state.Events) != total {
		t.Fatalf("前置事件数不对：%d", len(state.Events))
	}
	// 走真实保存路径：条目挂到 run 上，由 MutateCloudAgent 的事务与检查点一起写库。
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.CloudAgentRunEventCount("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if int(stored) != total {
		t.Fatalf("入库条数 = %d，期望 %d", stored, total)
	}
	// 检查点里不再有消息与事件。
	persisted, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.CheckpointVersion != cloudAgentCheckpointVersion {
		t.Fatalf("检查点版本 = %d，期望 %d", persisted.CheckpointVersion, cloudAgentCheckpointVersion)
	}
	checkpoint := map[string]any{}
	if err := json.Unmarshal([]byte(persisted.StateJSON), &checkpoint); err != nil {
		t.Fatalf("检查点不是合法 JSON: %v", err)
	}
	if checkpoint["events"] != nil || checkpoint["textHistory"] != nil {
		t.Fatalf("检查点里仍带着事件或历史：%s", truncateRunes(persisted.StateJSON, 200))
	}
	if canonical, ok := checkpoint["canonical"].(map[string]any); !ok || canonical["messages"] != nil {
		t.Fatalf("检查点里仍带着会话消息：%s", truncateRunes(persisted.StateJSON, 200))
	}
	// 重建：消息来自 transcript，事件只载入一窗，水位落在窗口之前。
	rebuilt, err := cloudAgentDecode(persisted)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if len(rebuilt.Canonical.Messages) == 0 {
		t.Fatal("重建后 canonical 消息为空")
	}
	if len(rebuilt.Events) != repository.CloudAgentJournalWindow {
		t.Fatalf("事件窗口 = %d 条，期望 %d", len(rebuilt.Events), repository.CloudAgentJournalWindow)
	}
	if rebuilt.EventSeqBase != total-repository.CloudAgentJournalWindow {
		t.Fatalf("事件水位 = %d，期望 %d", rebuilt.EventSeqBase, total-repository.CloudAgentJournalWindow)
	}
	if err := validateCloudAgentRuntime(persisted, &rebuilt); err != nil {
		t.Fatalf("重建后校验失败: %v", err)
	}

	// 幂等：没有新事件时再保存一次不应重复入库、也不应改动窗口。
	if err := s.repo.MutateCloudAgent("user", root.ID, persisted.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &rebuilt)
	}); err != nil {
		t.Fatal(err)
	}
	storedAgain, _ := s.repo.CloudAgentRunEventCount("user", root.ID)
	if storedAgain != stored {
		t.Fatalf("重复保存不幂等：stored %d → %d", stored, storedAgain)
	}

	// 默认视图：返回最近一页（事件窗口 + 表里更早的部分），序号连续。
	events := s.cloudAgentRunEventsForView("user", persisted, &rebuilt, 0, 0)
	if len(events) != cloudAgentRunEventPageLimit {
		t.Fatalf("默认页大小 = %d，期望 %d", len(events), cloudAgentRunEventPageLimit)
	}
	if events[len(events)-1].Seq != total || events[0].Seq != total-cloudAgentRunEventPageLimit+1 {
		t.Fatalf("默认页序号不连续：首 %d 尾 %d", events[0].Seq, events[len(events)-1].Seq)
	}
	if count := s.cloudAgentRunEventCount("user", persisted, &rebuilt); count != total {
		t.Fatalf("累计条数 = %d，期望 %d", count, total)
	}

	// 增量：sinceSeq 之后只返回更新的部分。
	delta := s.cloudAgentRunEventsForView("user", persisted, &rebuilt, total-3, 0)
	if len(delta) != 3 || delta[0].Seq != total-2 {
		t.Fatalf("增量视图不对：%+v", delta)
	}
	// 跨窗口边界：从水位之前 2 条开始，应当由"表 + 窗口"共同补齐且序号连续。
	delta = s.cloudAgentRunEventsForView("user", persisted, &rebuilt, rebuilt.EventSeqBase-2, 0)
	if len(delta) != 2+len(rebuilt.Events) {
		t.Fatalf("跨边界增量条数 = %d，期望 %d", len(delta), 2+len(rebuilt.Events))
	}
	for index := 1; index < len(delta); index++ {
		if delta[index].Seq != delta[index-1].Seq+1 {
			t.Fatalf("增量序号不连续：%d → %d", delta[index-1].Seq, delta[index].Seq)
		}
	}
}

// 旧检查点（消息与事件都在 state_json 里）读进来的运行照旧可读，
// 并在下一次保存时升级成 v2（消息进表、检查点置空）。
func TestCloudAgentLegacyCheckpointUpgradesOnSave(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 把这一行真的改回旧形态：StateJSON 里带消息与事件、版本 0（模拟升级前写入的运行）。
	current, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := json.Marshal(&current)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).
		Updates(map[string]any{"checkpoint_version": 0, "message_count": 0, "event_count": 0, "state_json": string(legacyJSON)}).Error; err != nil {
		t.Fatal(err)
	}
	legacy, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(legacy)
	if err != nil {
		t.Fatalf("旧检查点应可读: %v", err)
	}
	if !state.legacyCheckpoint {
		t.Fatal("旧检查点未被标记为待升级")
	}
	if len(state.Events) == 0 || len(state.Canonical.Messages) == 0 {
		t.Fatalf("旧检查点内容缺失：events=%d messages=%d", len(state.Events), len(state.Canonical.Messages))
	}
	legacyEvents := len(state.Events)
	if err := s.repo.MutateCloudAgent("user", root.ID, legacy.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	upgraded, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.CheckpointVersion != cloudAgentCheckpointVersion {
		t.Fatalf("升级后版本 = %d", upgraded.CheckpointVersion)
	}
	upgradedCheckpoint := map[string]any{}
	if err := json.Unmarshal([]byte(upgraded.StateJSON), &upgradedCheckpoint); err != nil {
		t.Fatal(err)
	}
	if upgradedCheckpoint["events"] != nil {
		t.Fatal("升级后检查点里仍有事件")
	}
	if upgraded.MessageCount == 0 || upgraded.EventCount != legacyEvents {
		t.Fatalf("升级后计数不对：messageCount=%d eventCount=%d（期望 %d）", upgraded.MessageCount, upgraded.EventCount, legacyEvents)
	}
	rebuilt, err := cloudAgentDecode(upgraded)
	if err != nil {
		t.Fatalf("升级后重建失败: %v", err)
	}
	if len(rebuilt.Canonical.Messages) != upgraded.MessageCount-len(rebuilt.TextHistory) {
		t.Fatalf("重建消息条数与计数不一致：%d vs %d", len(rebuilt.Canonical.Messages), upgraded.MessageCount)
	}
	if len(rebuilt.Events) != legacyEvents || rebuilt.EventSeqBase != 0 {
		t.Fatalf("升级后事件窗口不对：%d 条 水位 %d", len(rebuilt.Events), rebuilt.EventSeqBase)
	}
}

// 表里没有记录（旧检查点、之后没保存过的运行）时，运行详情仍要能读到内存窗口里的事件。
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
	state.EventSeqBase = 0
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
		{RunID: root.ID, UserID: "user", Seq: 1001, EventID: "e1", Type: "tool_completed", Payload: "{}", CreatedAt: now, ExpiresAt: now.Add(-time.Hour)},
		{RunID: root.ID, UserID: "user", Seq: 1002, EventID: "e2", Type: "tool_completed", Payload: "{}", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	before, _ := s.repo.CloudAgentRunEventCount("user", root.ID)
	deleted, err := s.repo.PurgeExpiredCloudAgentRunEvents(now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("删除条数 = %d，期望 1", deleted)
	}
	remaining, _ := s.repo.CloudAgentRunEventCount("user", root.ID)
	if remaining != before-1 {
		t.Fatalf("剩余条数 = %d，期望 %d", remaining, before-1)
	}
}

// 画布删除只清归属、不删审计事件。
func TestCloudAgentRunEventCanvasScopeClearedOnCanvasDelete(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	row := model.CloudAgentRunEvent{RunID: root.ID, UserID: "user", CanvasID: "canvas-x", Seq: 5001, EventID: "e1", Type: "tool_completed", Payload: "{}", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.ClearCloudAgentRunEventCanvasScope("user", "canvas-x"); err != nil {
		t.Fatal(err)
	}
	var stored model.CloudAgentRunEvent
	if err := db.First(&stored, "run_id = ? AND seq = ?", root.ID, 5001).Error; err != nil {
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
