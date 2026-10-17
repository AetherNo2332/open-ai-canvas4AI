package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

// 图片→导演流程的取消契约:image_to_director 运行的 block scene 在批准后即写入
// 画布,若在连续性检查通过前取消运行,半成品场景必须随之清理(与失败路径的
// rollbackDirectorWorkflowDraft 同语义)。本测试覆盖取消路径此前缺失的清理。

const directorStagingDocBefore = `{"nodes":[{"id":"n1","type":"text","title":"大纲"}],"previsScenes":[]}`

const directorStagingDocAfter = `{"nodes":[{"id":"n1","type":"text","title":"大纲"}],"previsScenes":[{"id":"scn-1","title":"从图复现的场景","templateId":"blocking","shots":[],"objects":[],"cameras":[],"lights":[],"updatedAt":"2026-10-13T08:00:00Z"}]}`

// newStagedDirectorRun 建一条真实的 image_to_director 运行:block scene 已批准
// 并写入画布(含 mutation 记录),状态停在 designed 阶段(未通过连续性检查)。
func newStagedDirectorRun(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: directorStagingDocBefore}).Error; err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	const key = "director-staged-cancel"
	req.IdempotencyKey = key
	created, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.repo.CloudAgent("user", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var beforeDoc, afterDoc map[string]any
	if err := json.Unmarshal([]byte(directorStagingDocBefore), &beforeDoc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(directorStagingDocAfter), &afterDoc); err != nil {
		t.Fatal(err)
	}
	if err := recordCloudAgentCanvasMutation(s.repo, cloudAgentMutationInput{
		RunID: run.ID, UserID: "user", CanvasID: "agent-canvas", StepID: "call-block", Operation: "previs_scene_create",
		BeforeSnapshotHash: cloudAgentCanvasHash(beforeDoc), AfterSnapshotHash: cloudAgentCanvasHash(afterDoc), BeforeJSON: directorStagingDocBefore,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", directorStagingDocAfter).Error; err != nil {
		t.Fatal(err)
	}
	// 状态推进到 designed:block scene 已应用、连续性检查未通过。
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.Workflow = "image_to_director"
	state.DirectorWorkflowPhase = "designed"
	state.DirectorWorkflowMutationCallID = "call-block"
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{"state_json": string(encoded), "revision": run.Revision + 1}).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, fresh
}

func canvasPayload(t *testing.T, s *Service) string {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	return canvas.PayloadJSON
}

func mutationStatus(t *testing.T, s *Service, runID string) string {
	t.Helper()
	mutation, err := s.repo.LatestCloudAgentCanvasMutation("user", runID)
	if err != nil || mutation == nil {
		t.Fatalf("mutation 记录缺失: %v", err)
	}
	return mutation.Status
}

// TestCancelCloudAgentRollsBackStagedDirectorScene:取消前场景停在 designed
// (连续性检查未通过),取消后画布必须回到场景写入之前,不留半成品场景。
func TestCancelCloudAgentRollsBackStagedDirectorScene(t *testing.T) {
	s, _, run := newStagedDirectorRun(t)

	if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
		t.Fatalf("取消必须成功: %v", err)
	}
	cancelled, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("取消后状态 = %q,期望 cancelled", cancelled.Status)
	}
	payload := canvasPayload(t, s)
	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatal(err)
	}
	scenes := creationMaps(doc["previsScenes"])
	if len(scenes) != 0 {
		t.Fatalf("取消后半成品场景必须清理,实际仍残留 %d 个场景: %s", len(scenes), payload)
	}
	if len(creationMaps(doc["nodes"])) != 1 {
		t.Fatalf("取消后画布原始节点必须保留: %s", payload)
	}
	if got := mutationStatus(t, s, run.ID); got != "undone" {
		t.Fatalf("场景草稿 mutation 应标记 undone,实际 %q", got)
	}
}

// TestCancelCloudAgentKeepsDriftedDirectorCanvas:用户在场景创建后编辑了画布
// (画布快照已漂移),取消不得静默覆盖用户编辑——保留漂移后的现状。
func TestCancelCloudAgentKeepsDriftedDirectorCanvas(t *testing.T) {
	s, db, run := newStagedDirectorRun(t)
	drifted := `{"nodes":[{"id":"n1","type":"text","title":"大纲"},{"id":"n2","type":"text","title":"用户手工编辑"}],"previsScenes":[{"id":"scn-1","title":"从图复现的场景","templateId":"blocking","shots":[],"objects":[],"cameras":[],"lights":[],"updatedAt":"2026-10-13T08:00:00Z"}]}`
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", drifted).Error; err != nil {
		t.Fatal(err)
	}

	if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
		t.Fatalf("取消必须成功: %v", err)
	}
	payload := canvasPayload(t, s)
	if !strings.Contains(payload, `"n2"`) {
		t.Fatalf("用户手工编辑不得被取消回滚覆盖: %s", payload)
	}
	if !strings.Contains(payload, `"scn-1"`) {
		t.Fatalf("漂移场景应保留由用户处理: %s", payload)
	}
}

// TestCancelCloudAgentIgnoresOrdinaryRuns:普通(非 image_to_director)运行取消
// 不回滚画布。
func TestCancelCloudAgentIgnoresOrdinaryRuns(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[{"id":"n1","type":"text","title":"大纲"}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.IdempotencyKey = "director-staged-cancel-plain"
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
		t.Fatalf("取消必须成功: %v", err)
	}
	payload := canvasPayload(t, s)
	if !strings.Contains(payload, `"n1"`) {
		t.Fatalf("普通运行取消不得改动画布: %s", payload)
	}
}