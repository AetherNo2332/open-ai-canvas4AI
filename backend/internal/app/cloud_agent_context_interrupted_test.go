package app

import (
	"context"
	"testing"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
)

// interruptedCompactionFixture 造一个"本轮暂停在压缩上、随后被取消"的现场：
// 压缩任务还在跑，状态里 ContextCompaction 是 running + resume，运行已经进终态且待收尾。
func interruptedCompactionFixture(t *testing.T) (*Service, *model.CloudAgentExecution, *cloudAgentRuntime) {
	t.Helper()
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Canonical.Messages = []map[string]any{
		{"role": "user", "content": "把分镜里第 1 到第 3 行补上光影"},
		{"role": "assistant", "content": "已读完分镜，准备写入"},
	}
	state.TextHistory = []providerTextMessage{{Role: "user", Content: "上一轮的要求"}, {Role: "assistant", Content: "上一轮的回复"}}
	state.ContextCompaction = &cloudAgentContextCompaction{Status: "running", Resume: true, TurnCount: 4, SourceBytes: 180000}
	state.ActiveTaskID = "task-compaction"
	state.TaskIDs = append(state.TaskIDs, "task-compaction")
	if err := db.Create(&model.Task{
		ID: "task-compaction", UserID: "user", Type: "canvas_text",
		Status: model.TaskStatusRunning, Operation: cloudAgentContextCompactionOperation,
	}).Error; err != nil {
		t.Fatal(err)
	}
	saved := saveAgentStateForTest(t, s, run, &state, func(current *model.CloudAgentExecution) {
		current.Status = "cancelled"
		current.CleanupPending = true
		current.ActiveTaskID = "task-compaction"
	})
	return s, saved, &state
}

func contextCompactedEvents(t *testing.T, s *Service, run *model.CloudAgentExecution) []CloudAgentEvent {
	t.Helper()
	current, err := s.repo.CloudAgent(run.UserID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(current)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]CloudAgentEvent, 0, 2)
	for _, event := range state.Events {
		if event.Type == "context_compacted" {
			events = append(events, event)
		}
	}
	return events
}

// 取消轮次时如果正停在压缩上，收尾必须落一份保底检查点并清掉压缩态：
// 终态轮次不会再被调度器推进，否则压缩态永远挂着、检查点永久丢失。
func TestCloudAgentCancelledRunFinalizesInterruptedCompaction(t *testing.T) {
	s, run, _ := interruptedCompactionFixture(t)
	if err := s.finishCloudAgentCleanup(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	final, err := s.repo.CloudAgent(run.UserID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "cancelled" {
		t.Fatalf("终态被改写: %q", final.Status)
	}
	if final.CleanupPending {
		t.Fatal("收尾没有完成")
	}
	state, err := cloudAgentDecode(final)
	if err != nil {
		t.Fatal(err)
	}
	if state.ContextCompaction != nil {
		t.Fatalf("压缩态没有收干净: %+v", state.ContextCompaction)
	}
	if state.ContextCheckpoint == nil {
		t.Fatal("保底检查点没有落盘")
	}
	if state.ContextCheckpoint.CompactedTurnCount != 4 {
		t.Fatalf("检查点轮次数 = %d", state.ContextCheckpoint.CompactedTurnCount)
	}
	events := contextCompactedEvents(t, s, final)
	if len(events) != 1 {
		t.Fatalf("context_compacted 事件数 = %d", len(events))
	}
	if events[0].Payload["mode"] != "fallback" || events[0].Payload["resume"] != true {
		t.Fatalf("压缩事件载荷 = %+v", events[0].Payload)
	}
	if events[0].Payload["reason"] == nil {
		t.Fatalf("中断收尾必须说明原因: %+v", events[0].Payload)
	}
	// 收尾是幂等的：CleanupPending 已清掉后再跑一次不会重复落事件。
	if err := s.finishCloudAgentCleanup(context.Background(), final); err != nil {
		t.Fatal(err)
	}
	if got := len(contextCompactedEvents(t, s, final)); got != 1 {
		t.Fatalf("收尾不幂等，事件数 = %d", got)
	}
}

// 失败轮同样是终态：中断收尾只能落检查点，不能把 failed 写成 completed。
func TestCloudAgentFailedRunStaysFailedWhenCompactionFinalized(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": "继续"}, {"role": "assistant", "content": "好的"}}
	state.ContextCompaction = &cloudAgentContextCompaction{Status: "requested", Resume: true, TurnCount: 2}
	state.ActiveTaskID = ""
	run = saveAgentStateForTest(t, s, run, &state)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	run, err = s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.finalizeCloudAgentInterruptedCompaction(run, &state, "本轮在压缩期间结束，已使用服务端保底检查点"); err != nil {
		t.Fatal(err)
	}
	final, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "failed" {
		t.Fatalf("失败轮被复活成 %q", final.Status)
	}
	if len(contextCompactedEvents(t, s, final)) != 1 {
		t.Fatal("失败轮没有落保底检查点")
	}
}

// 收尾压缩（非中途暂停）仍要把本轮判成 completed：keepTerminal 不能顺手改掉正常路径。
func TestCloudAgentFinishCompactionStillCompletesTheRun(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": "继续"}, {"role": "assistant", "content": "好的"}}
	state.ContextCompaction = &cloudAgentContextCompaction{Status: "requested", TurnCount: 2}
	state.ActiveTaskID = ""
	run = saveAgentStateForTest(t, s, run, &state)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Update("status", "running").Error; err != nil {
		t.Fatal(err)
	}
	run, err = s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := agentcontext.Checkpoint{Version: agentcontext.Version, HistorySummary: "收尾压缩"}
	if err := s.persistCloudAgentContextCheckpoint(run, &state, checkpoint, "model", ""); err != nil {
		t.Fatal(err)
	}
	final, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "completed" {
		t.Fatalf("收尾压缩后的状态 = %q，应为 completed", final.Status)
	}
	events := contextCompactedEvents(t, s, final)
	if len(events) != 1 || events[0].Payload["resume"] != false || events[0].Payload["mode"] != "model" {
		t.Fatalf("收尾压缩事件 = %+v", events)
	}
}

// 没有停在压缩上的轮次：收尾不该凭空写一条压缩事件。
func TestCloudAgentCleanupWithoutPendingCompactionWritesNoCheckpointEvent(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).
		Updates(map[string]any{"status": "cancelled", "cleanup_pending": true}).Error; err != nil {
		t.Fatal(err)
	}
	run, err = s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.finishCloudAgentCleanup(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if got := len(contextCompactedEvents(t, s, run)); got != 0 {
		t.Fatalf("无压缩态的收尾写了 %d 条压缩事件", got)
	}
}
