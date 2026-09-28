package app

import (
	"encoding/json"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

// TestCloudAgentRunReadsRunWithoutRootTask 覆盖阶段 2 第 1 步：读取路径不再要求根任务。
//
// 迁移背景：旧 run 的身份就是那条 `operation=cloud_agent` 的根任务行，`cloudAgentTask`
// 以 runID 直查 Task 表。阶段 2 之后的新 run 由 Go 直接创建（run + 预授权 + 不可变快照），
// **不再有根任务** —— 若不先加兼容读路径就改创建流程，新 run 会全部变成 404。
//
// 这个用例证明：执行记录存在、根任务不存在时，运行仍可读。
func TestCloudAgentRunReadsRunWithoutRootTask(t *testing.T) {
	s, db, run := piAgentTestFixture(t)
	_, state := reloadPiRun(t, s, run.ID)

	const key = "no-root-task-key"
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

	// 前提：这条运行确实没有根任务。
	if _, err := s.repo.TaskForUser("user", runID); err == nil {
		t.Fatal("前提不成立：不应存在根任务")
	}

	view, err := s.CloudAgentRun("user", runID)
	if err != nil {
		t.Fatalf("没有根任务的运行必须可读（阶段 2 之后的新 run 形态）: %v", err)
	}
	if view.ID != runID {
		t.Fatalf("运行 ID = %q，期望 %q", view.ID, runID)
	}
	if view.CanvasID != state.Request.CanvasID {
		t.Fatalf("画布 ID = %q，期望 %q", view.CanvasID, state.Request.CanvasID)
	}
	if view.Status != "running" {
		t.Fatalf("状态 = %q，期望 running（应取执行记录，而不是任务状态映射）", view.Status)
	}
}

// TestCloudAgentRunStillReadsLegacyRunWithRootTask 保证这一步是**严格增量**的：
// 只要根任务还在，读取行为与迁移前一致（这条路径也是既有一切真实运行的路径）。
//
// 注意夹具差异：`piAgentTestFixture` 造的是"只有执行记录、没有根任务"的**未来形态**
//（它登记的是另一条 `pi-root-task`，不是 runID 那条根任务），所以那条运行在迁移前
// 本来就 404。真正要验证旧路径，必须走 `CreateCloudAgentRun` —— 它才会建根任务。
func TestCloudAgentRunStillReadsLegacyRunWithRootTask(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatalf("创建旧路径运行失败: %v", err)
	}
	// CreateCloudAgentRun 当前会创建 Pi holding 任务。把该已落库记录调整成旧 operation，
	// 构造历史根任务的读取形状；运行 ID、用户和嵌入请求仍沿用真实建 run 的确定性值。
	if err := db.Model(&model.Task{}).Where("id = ?", created.ID).Updates(map[string]any{
		"operation": cloudAgentOperation,
		"status":    model.TaskStatusRunning,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 前提：这条运行确实有根任务（runID == 根任务 ID）。
	root, err := s.repo.TaskForUser("user", created.ID)
	if err != nil {
		t.Fatalf("前提不成立：旧路径运行应有根任务: %v", err)
	}
	if root.Operation != cloudAgentOperation {
		t.Fatalf("根任务 operation = %q，期望 %q", root.Operation, cloudAgentOperation)
	}

	view, err := s.CloudAgentRun("user", created.ID)
	if err != nil {
		t.Fatalf("旧路径运行必须仍可读: %v", err)
	}
	if view.ID != created.ID || view.CanvasID != "agent-canvas" {
		t.Fatalf("旧路径视图身份变化: %+v", view)
	}
}

// TestCloudAgentRunRejectsUnknownRun：两条路径都不存在时必须仍然是 404，
// 不能因为新增了执行记录读取就放宽为"任意 ID 都可读"。
func TestCloudAgentRunRejectsUnknownRun(t *testing.T) {
	s, _, _ := piAgentTestFixture(t)
	if _, err := s.CloudAgentRun("user", "ag-does-not-exist"); err == nil {
		t.Fatal("不存在的运行必须 404")
	}
	// 归属隔离：别人的运行不能被读到。
	if _, err := s.CloudAgentRun("other-user", "ag-does-not-exist"); err == nil {
		t.Fatal("非归属用户不得读到运行")
	}
}

// TestCloudAgentRunRejectsMismatchedRunIdentity：执行记录存在但 ID 与
// (userID, 幂等键) 不一致时必须拒绝 —— 这是防越权/错挂的身份自校验，
// 与旧路径的 `task.ID == cloudAgentID(...)` 同源。
func TestCloudAgentRunRejectsMismatchedRunIdentity(t *testing.T) {
	s, db, run := piAgentTestFixture(t)
	_, state := reloadPiRun(t, s, run.ID)

	// 故意让 ID 与幂等键不匹配。
	state.Request.IdempotencyKey = "some-other-key"
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	const bogusID = "ag-identity-mismatch"
	if err := db.Create(&model.CloudAgentExecution{
		ID: bogusID, UserID: "user", Status: "running", Engine: "pi", Revision: 1,
		CanvasID: state.Request.CanvasID, StateJSON: string(encoded),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloudAgentRun("user", bogusID); err == nil {
		t.Fatal("ID 与幂等键不一致的运行必须被拒绝")
	}
	if err := db.Where("id = ?", bogusID).Delete(&model.CloudAgentExecution{}).Error; err != nil && err != gorm.ErrRecordNotFound {
		t.Fatal(err)
	}
}
