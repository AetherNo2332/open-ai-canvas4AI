package app

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
)

// 行与输出的 metadata 可以被历史回滚，因此 Agent 的产物归属写入必须核对持久任务
// 绑定：只有未终结的已受理任务能证明该槽位仍被占用。
func TestAgentStoryboardOutputBindingChecksDurableOccupancy(t *testing.T) {
	s, db, _ := agentMediaFixture(t)
	doc := map[string]any{
		"id":                     "agent-canvas",
		"referenceFieldsVersion": float64(1),
		"nodes": []any{
			map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"referenceNodeIds": []any{}, "rows": []any{map[string]any{"id": "row", "assetBindings": []any{}}}}}},
			map[string]any{"id": "output", "type": "video", "metadata": map[string]any{}},
		},
		"connections": []any{},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.CanvasDocumentEpoch{CanvasID: "agent-canvas", OwnerID: "user", Epoch: "epoch-occupancy", UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.Task{ID: "active-task", UserID: "user", Type: "canvas_video", Status: model.TaskStatusRunning}).Error; err != nil {
		t.Fatal(err)
	}
	binding := model.CanvasTaskBinding{TaskID: "active-task", OwnerID: "user", CanvasID: "agent-canvas", Epoch: "epoch-occupancy", ActorID: "user", ClientID: "client", OperationID: "task-op", TargetNodeID: "board", Mode: "video", ConsumerJSON: `{"kind":"storyboard","rowId":"row","field":"videoNodeId","outputNodeId":"output"}`}
	if err = db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}

	args := cloudAgentStoryboardEditArgs{SnapshotHash: cloudAgentCanvasHash(doc), NodeID: "board", RowID: "row", Action: "bind_output", OutputMode: "video", OutputNodeID: "output"}
	if _, err = prepareCloudAgentStoryboardReferenceEdit(s.repo, "user", "agent-canvas", args); err == nil {
		t.Fatal("有活动任务的分镜行接受了新的产物归属")
	} else {
		var fieldErr *cloudAgentFieldArgumentError
		if !errors.As(err, &fieldErr) || fieldErr.Issue != "reference_busy" {
			t.Fatalf("活动占用应返回 reference_busy，实际：%v", err)
		}
	}
	stored, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PayloadJSON != string(raw) {
		t.Fatal("被拒绝的产物归属改写了画布")
	}
	remove := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "remove-active", map[string]any{"nodeId": "board", "snapshotHash": cloudAgentCanvasHash(doc), "action": "remove", "rowIds": []string{"row"}})
	if _, err = prepareCloudAgentStoryboardEdit(s.repo, "user", "agent-canvas", remove); err == nil {
		t.Fatal("持久活动占用被批量删除绕过")
	}

	// 任务终结后同一写入必须通过：占用校验只拦真正在跑的槽位。
	if err = db.Model(&model.Task{}).Where("id = ?", "active-task").Update("status", model.TaskStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = prepareCloudAgentStoryboardReferenceEdit(s.repo, "user", "agent-canvas", args); err != nil {
		t.Fatalf("任务结束后仍被拒绝：%v", err)
	}
}
