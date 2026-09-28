package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// newRootTaskLessPiRun 造一条**真实的**"只有执行记录、没有根任务"的运行 ——
// 阶段 2 之后新 run 的形态（Go 直接建 run + 预授权 + 不可变快照，不再建根任务行）。
//
// 注意不能直接用 `piAgentTestFixture` 的运行：它的 ID 是写死的 `pi-run-1`，
// 而真实运行的 ID 必须由 `(userID, 幂等键)` 派生（`CreateCloudAgentRun` 里
// `id := cloudAgentID(userID, req.IdempotencyKey)`）。身份自校验会拒绝 ID 与幂等键
// 不匹配的运行 —— 那条守卫是对的，夹具不真实而已。所以这里按真实规则重建。
func newRootTaskLessPiRun(t *testing.T) (*Service, *model.CloudAgentExecution) {
	t.Helper()
	s, db, fixture := piAgentTestFixture(t)
	_, state := reloadPiRun(t, s, fixture.ID)

	const key = "control-plane-no-root-task"
	runID := cloudAgentID("user", key)
	state.Request.IdempotencyKey = key
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentExecution{
		ID: runID, UserID: "user", Status: "running", Engine: "pi", Revision: 1,
		CanvasID: state.Request.CanvasID, StateJSON: string(encoded),
	}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.TaskForUser("user", runID); err == nil {
		t.Fatal("前提不成立：新形态运行不应有根任务")
	}
	return s, run
}

// TestCancelCloudAgentWorksWithoutRootTask 覆盖控制面入口的根任务耦合。
//
// 修复前 `CancelCloudAgent` 以 `TaskForUser(userID, id)` + `operation == cloud_agent`
// 作为唯一授权入口。阶段 2 之后的新 run 没有根任务行，于是**完全无法取消** ——
// 用户只能看着它跑完，或者等看门狗超时判停。这是控制面能力缺失，不只是读不到。
func TestCancelCloudAgentWorksWithoutRootTask(t *testing.T) {
	s, run := newRootTaskLessPiRun(t)

	if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
		t.Fatalf("没有根任务的运行必须可取消，实际 %v", err)
	}
	cancelled, _ := reloadPiRun(t, s, run.ID)
	if cancelled.Status != "cancelled" {
		t.Fatalf("取消后状态 = %q，期望 cancelled", cancelled.Status)
	}
	// 取消会置 CleanupPending 并**同步**跑完 finishCloudAgentCleanup；
	// 该函数在成功收尾时会自己把它清回 false（cloud_agent_recovery.go:116）。
	// 所以"取消成功"的正确观测是 false —— 若为 true 说明清理没跑完（子任务未取消）。
	if cancelled.CleanupPending {
		t.Fatal("取消后清理未完成：CleanupPending 仍为 true，说明子任务/媒体收尾没有跑")
	}
}

// TestCancelCloudAgentStillWorksForLegacyRun：旧路径运行（有根任务）行为不变。
func TestCancelCloudAgentStillWorksForLegacyRun(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	// 重建旧版根任务 operation；Pi holding operation 不属于历史读取路径。
	if err := db.Model(&model.Task{}).Where("id = ?", created.ID).Updates(map[string]any{
		"operation": cloudAgentOperation,
		"status":    model.TaskStatusRunning,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.TaskForUser("user", created.ID); err != nil {
		t.Fatalf("前提不成立：旧路径运行应有根任务: %v", err)
	}
	if err := s.CancelCloudAgent(context.Background(), "user", created.ID); err != nil {
		t.Fatalf("旧路径运行仍必须可取消: %v", err)
	}
	view, err := s.CloudAgentRun("user", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "cancelled" {
		t.Fatalf("旧路径取消后状态 = %q，期望 cancelled", view.Status)
	}
}

// TestCancelCloudAgentStillRejectsForeignRun：放宽授权入口不得放宽归属隔离。
func TestCancelCloudAgentStillRejectsForeignRun(t *testing.T) {
	s, run := newRootTaskLessPiRun(t)
	if err := s.CancelCloudAgent(context.Background(), "other-user", run.ID); err == nil {
		t.Fatal("他人不得取消本用户的运行")
	}
	if err := s.CancelCloudAgent(context.Background(), "user", "ag-no-such-run"); err == nil {
		t.Fatal("不存在的运行必须 404")
	}
}

// TestInterjectCloudAgentAcceptsRunWithoutRootTask：插话的归属校验同样不能再要求根任务。
//
// 这里只断言"不再因为缺根任务而 404"。插话本身是否被接受取决于运行状态与 CAS，
// 那属于另一条逻辑，不在本用例的范围内。
func TestInterjectCloudAgentAcceptsRunWithoutRootTask(t *testing.T) {
	s, run := newRootTaskLessPiRun(t)

	_, err := s.InterjectCloudAgent("user", run.ID, "补充一句", "msg-root-task-less")
	if err != nil && strings.Contains(err.Error(), "不存在") {
		t.Fatalf("没有根任务的运行不应在归属校验处被判不存在: %v", err)
	}

	// 反向：他人仍然要 404。
	if _, err := s.InterjectCloudAgent("other-user", run.ID, "补充一句", "msg-foreign"); err == nil {
		t.Fatal("他人不得向本用户的运行插话")
	}
}

// TestRunRefResolutionUsedByControlPlaneEntries 锁住这条修复的形态：
// 两个控制面入口都必须能解析"没有根任务"的运行，且解析结果的画布归属来自执行记录。
func TestRunRefResolutionUsedByControlPlaneEntries(t *testing.T) {
	s, run := newRootTaskLessPiRun(t)

	ref, err := s.cloudAgentRunRefFor("user", run.ID)
	if err != nil {
		t.Fatalf("无根任务运行必须可解析: %v", err)
	}
	if ref.LegacyTask != nil {
		t.Fatal("无根任务运行时 LegacyTask 必须为 nil")
	}
	if ref.Identity.ID != run.ID || ref.Identity.CanvasID != run.CanvasID {
		t.Fatalf("身份解析错误: %+v", ref.Identity)
	}
	if ref.Identity.Status != run.Status {
		t.Fatalf("状态应取执行记录: %q vs %q", ref.Identity.Status, run.Status)
	}
	if _, err := json.Marshal(ref.State); err != nil {
		t.Fatalf("解析出的 state 必须可序列化: %v", err)
	}
}
