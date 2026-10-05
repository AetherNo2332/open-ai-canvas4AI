package app

import (
	"testing"
)

// G4：Agent 的准入 ID 是确定性任务 ID。worker 在"任务已落库、运行检查点未落库"
// 之间崩溃后一定用同一 ID 重投；此时必须幂等返回既有任务，而不是准入失败。
func TestCloudAgentAdmissionIdempotentResubmission(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")
	req, _, err := s.prepareCloudAgentMedia(run, &state, agentMediaCall(args))
	if err != nil {
		t.Fatal(err)
	}
	req.admission = &taskAdmission{ID: "generation-task", MaxCharge: 1000, AgentRunID: run.ID, GenerationID: "generation", ApprovalID: "approval"}
	req.creationPrepare = &creationTaskPreparation{}

	first, err := s.CreateTask(run.UserID, req)
	if err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	// 模拟第一次提交已经把任务真正落库（同一 admission ID）。
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := createTaskWithStorageQuotaRepository(s.repo, first, req.creationPrepare.Order, policy); err != nil {
		t.Fatalf("首次落库失败: %v", err)
	}

	retry, err := s.CreateTask(run.UserID, req)
	if err != nil {
		t.Fatalf("重投应当幂等成功，实际报错: %v", err)
	}
	if retry.ID != first.ID {
		t.Fatalf("重投返回了不同任务: %q vs %q", retry.ID, first.ID)
	}

	// 幂等命中必须同时校验运行归属：换一个运行 ID 就不算命中，
	// 调用方拿到的只是"已准备但未落库"的任务，绝不可能拿到既有任务。
	foreign := req
	foreign.admission = &taskAdmission{ID: "generation-task", MaxCharge: 1000, AgentRunID: "other-run", GenerationID: "generation"}
	foreign.creationPrepare = &creationTaskPreparation{}
	if s.cloudAgentTaskForAdmissionID(run.UserID, foreign) != nil {
		t.Fatal("跨运行 ID 的提交命中了别人登记的任务")
	}
	foreignTask, err := s.CreateTask(run.UserID, foreign)
	if err != nil {
		t.Fatalf("跨运行提交不应报错（重复 ID 由落库阶段拦截）: %v", err)
	}
	if foreignTask.AgentRunID == run.ID {
		t.Fatal("跨运行提交拿到了别人运行的任务")
	}

	// admission 不完整时绝不能命中：否则任何调用方都能用空运行 ID 复用既有任务。
	blank := req
	blank.admission = &taskAdmission{ID: "generation-task", MaxCharge: 1000}
	if s.cloudAgentTaskForAdmissionID(run.UserID, blank) != nil {
		t.Fatal("缺少 AgentRunID 的提交命中了既有任务")
	}

	// 没有 admission 的普通任务不能被这条幂等路径影响。
	plain := CreateTaskRequest{ProjectID: run.CanvasID, Type: "canvas_text", Operation: "noop", Prompt: "p", Input: map[string]any{"mode": "text"}}
	if _, err := s.CreateTask(run.UserID, plain); err == nil {
		t.Fatal("缺少模型选择的普通任务不应创建成功")
	}
}
