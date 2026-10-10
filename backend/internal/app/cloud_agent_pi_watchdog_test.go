package app

import (
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

// 看门狗：worker 崩溃或配置错误时，运行会被反复领取却永远不终结，
// 前端表现为"Agent 输出完了却一直运行中"。这里证明长时间无进展的运行会被终结。
func TestSweepStalledPiAgentRunsTerminatesAbandonedRun(t *testing.T) {
	s, db, run := piAgentTestFixture(t)

	// 租约远早于"停滞阈值"（6 × 45s）之前过期。
	stale := time.Now().UTC().Add(-(piStalledLeasePeriods + 1) * piAgentLeaseDuration)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"lease_expires_at": stale, "status": "running"}).Error; err != nil {
		t.Fatal(err)
	}

	swept, err := s.SweepStalledPiAgentRuns()
	if err != nil {
		t.Fatalf("看门狗失败: %v", err)
	}
	if swept != 1 {
		t.Fatalf("应终结 1 个停滞运行，实际 %d", swept)
	}
	failed, state := reloadPiRun(t, s, run.ID)
	if failed.Status != "failed" {
		t.Fatalf("状态 = %q，期望 failed", failed.Status)
	}
	if !strings.Contains(failed.FailureMessage, "长时间未推进") {
		t.Fatalf("失败原因不明确: %q", failed.FailureMessage)
	}
	reason := ""
	for _, event := range state.Events {
		if event.Type == "run_failed" {
			reason, _ = event.Payload["reason"].(string)
		}
	}
	if reason != "pi_worker_stalled" {
		t.Fatalf("run_failed 原因 = %q，期望 pi_worker_stalled", reason)
	}

	// 幂等：再次清扫不应重复处理。
	again, err := s.SweepStalledPiAgentRuns()
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("已终结的运行被重复清扫: %d", again)
	}
}

// 活跃 worker 的运行不能被误杀：租约刚续过（未过期）时必须跳过。
func TestSweepStalledPiAgentRunsLeavesActiveRunAlone(t *testing.T) {
	s, db, run := piAgentTestFixture(t)

	fresh := time.Now().UTC().Add(piAgentLeaseDuration)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"lease_expires_at": fresh, "status": "running"}).Error; err != nil {
		t.Fatal(err)
	}
	swept, err := s.SweepStalledPiAgentRuns()
	if err != nil {
		t.Fatal(err)
	}
	if swept != 0 {
		t.Fatalf("活跃运行被误杀: %d", swept)
	}
	alive, _ := reloadPiRun(t, s, run.ID)
	if alive.Status != "running" {
		t.Fatalf("活跃运行状态被改动: %q", alive.Status)
	}
}
