package app

import (
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
