package tasks

import (
	"context"
	"testing"
	"yingce/backend/internal/model"
)

func TestListAnalysisMaterializesWithoutRendererAndDoesNotReplaceSourceText(t *testing.T) {
	s, db, repo, cs, p, req, stub := taskFixture(t)
	req.Task.Operation = "list_analysis"
	req.Consumer = Consumer{Kind: "batch_document"}
	req.Task.Input = map[string]any{"metadata": map[string]any{"listAnalysis": map[string]any{"inputNodeIds": []any{}, "targetRowCount": float64(1)}}}
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	original := str(metadata(findNode(before.Document, req.TargetNodeID))["content"])
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"{\"columns\":[\"卖点\"],\"rows\":[{\"卖点\":\"轻便\"}]}"}`})
	if err := repo.EnqueueCanvasMaterialization(h.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	result, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if findNode(result.Document, "batch-table-"+h.TaskID) == nil || str(metadata(findNode(result.Document, req.TargetNodeID))["content"]) != original || stub.calls != 1 {
		t.Fatal("analysis not independently materialized")
	}
}
