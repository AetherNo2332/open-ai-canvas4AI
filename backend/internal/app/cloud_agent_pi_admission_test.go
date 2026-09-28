package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

// 阶段 2.2（门槛）：Pi 准入必须"每个准入身份只有一个任务、只预授权一次"。
//
// 关键不在于 CreateTask 的幂等回退（enqueueCloudAgentTask 走 creationPrepare 分支，
// 在那里提前返回，回退根本不生效），而在于：**任务创建、预算预授权与运行检查点
// 在同一个 MutateCloudAgent 事务里提交**，且 PiModelStep 在 ActiveTaskID 非空时
// 直接返回既有任务视图。
//
// 这个用例证明两件事：
//  1. 重复调用 PiModelStep（模拟"响应丢失后重投"）不会产生第二个任务或第二笔预授权；
//  2. 同一运行始终拿到同一个 taskId。
func TestPiModelStepAdmissionIsExactlyOnce(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	// 用运行自身的 canonical（含 system 提示与已披露工具）作为本步请求。
	_, runState := reloadPiRun(t, s, run.ID)
	framework := PiModelStepRequest{Canonical: runState.Canonical}

	first, err := s.PiModelStep("user", run.ID, run.LeaseOwner, framework)
	if err != nil {
		t.Fatalf("首次准入失败: %v", err)
	}
	if first.TaskID == "" {
		t.Fatal("首次准入没有产生任务")
	}

	// 重投：worker 在"任务已提交、响应丢失"后再请求一次同一步骤。
	second, err := s.PiModelStep("user", run.ID, run.LeaseOwner, framework)
	if err != nil {
		t.Fatalf("重投失败: %v", err)
	}
	if second.TaskID != first.TaskID {
		t.Fatalf("重投应返回既有任务：%s vs %s", second.TaskID, first.TaskID)
	}

	// 只允许一个任务与一笔预授权。
	var taskCount int64
	if err := db.Model(&model.Task{}).Where("agent_run_id = ?", run.ID).Count(&taskCount).Error; err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 {
		t.Fatalf("该运行的任务数 = %d，期望 1", taskCount)
	}
	var orderCount int64
	if err := db.Model(&model.BillingOrder{}).Where("task_id = ?", first.TaskID).Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 1 {
		t.Fatalf("该任务的预授权数 = %d，期望 1", orderCount)
	}

	// 运行检查点里记录的也是同一个任务。
	_, state := reloadPiRun(t, s, run.ID)
	if state.ActiveTaskID != first.TaskID {
		t.Fatalf("检查点记录的活动任务 = %q，期望 %q", state.ActiveTaskID, first.TaskID)
	}
}
