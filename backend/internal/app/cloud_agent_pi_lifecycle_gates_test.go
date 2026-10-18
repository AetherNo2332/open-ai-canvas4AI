package app

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// setPiRunStatus 把运行直接置为给定状态，用于构造"worker 仍在途、但运行已终结"的竞态。
func setPiRunStatus(t *testing.T, s *Service, db *gorm.DB, runID, status string) {
	t.Helper()
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", runID).
		Update("status", status).Error; err != nil {
		t.Fatal(err)
	}
}

// TestPiFailModelStepPreservesTerminalStates 覆盖"运行已经终结，但 worker 的 /fail
// 仍在途"的竞态窗口。
//
// 修复前 PiFailModelStep 只特判 failed，之后无条件写 failed —— cancelled / rejected /
// completed 会被改写回 failed，把一次用户主动取消或已完成的运行变成失败。
//
// 必须返回 nil 而不是错误：Node 只有在收到错误时才会把它当协议错误；给一个已经正确
// 终结的运行回 4xx，会被 bridge 的致命分类整轮退出，把"已终结"变成 worker 报错。
// 兄弟入口 PiFailRun 一直是这个语义（cloudAgentRunTerminal → return nil）。
func TestPiFailModelStepPreservesTerminalStates(t *testing.T) {
	for _, status := range []string{"cancelled", "rejected", "completed"} {
		t.Run(status, func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)

			taskID := "pi-task-" + status
			if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID,
				Type: "canvas_text", Status: model.TaskStatusFailed, Error: "上游超时"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				state, err := cloudAgentDecode(current)
				if err != nil {
					return err
				}
				state.ActiveTaskID = taskID
				state.TaskIDs = append(state.TaskIDs, taskID)
				return cloudAgentSave(current, &state)
			}); err != nil {
				t.Fatal(err)
			}
			setPiRunStatus(t, s, db, run.ID, status)

			current, _ := reloadPiRun(t, s, run.ID)
			if err := s.PiFailModelStep("user", run.ID, current.LeaseOwner, taskID); err != nil {
				t.Fatalf("已终结运行(%s)的失败上报必须幂等成功，实际 %v", status, err)
			}
			after, _ := reloadPiRun(t, s, run.ID)
			if after.Status != status {
				t.Fatalf("终态被改写：%q -> %q", status, after.Status)
			}
		})
	}
}

// TestPiFailModelStepRejectsSucceededTaskEvenOnTerminalRun 保证幂等守卫不会顺手吞掉
// 真正的协议违规："任务已成功"永远不能被上报为失败，与运行是否终态无关。
func TestPiFailModelStepRejectsSucceededTaskEvenOnTerminalRun(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	taskID := "pi-task-succeeded"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID,
		Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"ok"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.ActiveTaskID = taskID
		state.TaskIDs = append(state.TaskIDs, taskID)
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	setPiRunStatus(t, s, db, run.ID, "cancelled")

	current, _ := reloadPiRun(t, s, run.ID)
	if err := s.PiFailModelStep("user", run.ID, current.LeaseOwner, taskID); err == nil {
		t.Fatal("已成功的任务被上报为失败时必须拒绝，不能因运行已终结而放行")
	}
}

// TestSweepStalledPiAgentRunsSetsCleanupPending：看门狗终结的运行必须走完整清理。
//
// finishCloudAgentCleanup 的入口条件是 `CleanupPending && 终态`。看门狗此前只写 failed，
// 于是被判停的运行既不取消在跑的子任务，也不回写已完成的媒体结果 —— 那部分工作永远丢失。
func TestSweepStalledPiAgentRunsSetsCleanupPending(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	stale := time.Now().UTC().Add(-(piStalledLeasePeriods + 1) * piAgentLeaseDuration)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"lease_expires_at": stale, "status": "running"}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := s.SweepStalledPiAgentRuns(); err != nil {
		t.Fatalf("看门狗失败: %v", err)
	}
	swept, _ := reloadPiRun(t, s, run.ID)
	if swept.Status != "failed" {
		t.Fatalf("状态 = %q，期望 failed", swept.Status)
	}
	if !swept.CleanupPending {
		t.Fatal("看门狗判停的运行必须置 CleanupPending，否则子任务与媒体回写无人收尾")
	}
}

// TestSweepStalledIgnoresNeverClaimedRun：从未被领取的运行不能被当成"worker 失联"。
//
// StalledPiAgentRuns 原先的条件是 `lease_expires_at IS NULL OR lease_expires_at < cutoff`，
// 于是**根本没有 worker 在跑**时，每个新运行都会在 6×45s≈4.5 分钟后被判 failed，且原因是
// "worker 长时间未推进"——既不准确，也让"排队"语义消失。
func TestSweepStalledIgnoresNeverClaimedRun(t *testing.T) {
	s, db, run := piAgentTestFixture(t)

	// 夹具的运行没有租约（从未领取）。把它做旧，模拟"排了很久的队"。
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"lease_expires_at": nil, "created_at": time.Now().Add(-2 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}

	swept, err := s.SweepStalledPiAgentRuns()
	if err != nil {
		t.Fatal(err)
	}
	if swept != 0 {
		t.Fatalf("从未领取的运行不应被 stalled 看门狗处理，实际 %d", swept)
	}
	alive, _ := reloadPiRun(t, s, run.ID)
	if alive.Status != "running" {
		t.Fatalf("未领取运行被误改状态: %q", alive.Status)
	}
}

// TestSweepUnclaimedPiAgentRunsFailsWithDistinctReason：长时间无人领取的运行必须变成
// **明确可见**的终态，而不是静默排队。
//
// 前端把 queued 与 running 渲染成同一种"运行中"，所以"永远排队"就等于重现"永远显示运行中"。
// 因此超时后要给出与 worker 失联**不同**的失败原因，让运维能区分"没有 worker"与"worker 死了"。
func TestSweepUnclaimedPiAgentRunsFailsWithDistinctReason(t *testing.T) {
	s, db, run := piAgentTestFixture(t)

	old := time.Now().Add(-(piUnclaimedRunTimeout + time.Minute))
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"lease_expires_at": nil, "created_at": old}).Error; err != nil {
		t.Fatal(err)
	}

	swept, err := s.SweepUnclaimedPiAgentRuns()
	if err != nil {
		t.Fatalf("未领取清扫失败: %v", err)
	}
	if swept != 1 {
		t.Fatalf("应终结 1 个长期无人领取的运行，实际 %d", swept)
	}
	failed, state := reloadPiRun(t, s, run.ID)
	if failed.Status != "failed" {
		t.Fatalf("状态 = %q，期望 failed", failed.Status)
	}
	if !failed.CleanupPending {
		t.Fatal("未领取清扫同样需要置 CleanupPending")
	}
	reason := ""
	for _, event := range state.Events {
		if event.Type == "run_failed" {
			reason, _ = event.Payload["reason"].(string)
		}
	}
	if reason != "pi_worker_unavailable" {
		t.Fatalf("run_failed 原因 = %q，期望 pi_worker_unavailable（与 pi_worker_stalled 区分）", reason)
	}
}

// TestSweepUnclaimedIgnoresRecentRun：刚创建、还没等到 worker 的运行必须留在队列里。
// 阈值要能容忍 worker 重启与镜像重建。
func TestSweepUnclaimedIgnoresRecentRun(t *testing.T) {
	s, db, run := piAgentTestFixture(t)

	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"lease_expires_at": nil, "created_at": time.Now().Add(-time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}

	swept, err := s.SweepUnclaimedPiAgentRuns()
	if err != nil {
		t.Fatal(err)
	}
	if swept != 0 {
		t.Fatalf("刚创建的无租约运行不应被清扫，实际 %d", swept)
	}
	alive, _ := reloadPiRun(t, s, run.ID)
	if alive.Status != "running" {
		t.Fatalf("新运行被误杀: %q", alive.Status)
	}
}

// TestClaimPiAgentStillClaimsNeverLeasedRun 是 S1-C 的回归护栏。
//
// 修"未领取被误杀"时唯一的诱惑是把 claim 谓词里的 `lease_expires_at IS NULL` 一起删掉。
// 那会让**首次领取永远不可能发生**。这里锁死：从未租出的运行必须仍然可被领取。
func TestClaimPiAgentStillClaimsNeverLeasedRun(t *testing.T) {
	s, _, run := piAgentTestFixture(t)

	claimed, err := s.ClaimPiAgent("worker-fresh")
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("从未租出的运行必须仍可被领取")
	}
	if claimed.RunID != run.ID {
		t.Fatalf("领取到的运行 = %q，期望 %q", claimed.RunID, run.ID)
	}
	leased, _ := reloadPiRun(t, s, run.ID)
	if leased.LeaseOwner != "worker-fresh" || leased.LeaseExpiresAt == nil {
		t.Fatalf("领取后必须同时写入 owner 与到期时间: owner=%q expires=%v", leased.LeaseOwner, leased.LeaseExpiresAt)
	}
}
