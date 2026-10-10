package app

import (
	"errors"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"time"

	"gorm.io/gorm"
)

type cloudAgentHistoryPlan struct {
	Canvas     *model.CanvasProject
	Mutation   *model.CloudAgentCanvasMutation
	Document   map[string]any
	BeforeJSON string
	BeforeHash string
	Preview    cloudAgentApprovalPreview
}

func prepareCloudAgentHistory(repo *repository.Repository, userID, canvasID, runID string, call cloudAgentCall) (*cloudAgentHistoryPlan, error) {
	var args struct {
		SnapshotHash string `json:"snapshotHash"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	hash := cloudAgentCanvasHash(doc)
	var mutation *model.CloudAgentCanvasMutation
	if call.Function.Name == "canvas_redo" {
		mutation, err = repo.CloudAgentCanvasMutationForRedo(userID, runID, canvasID)
	} else {
		mutation, err = repo.CloudAgentCanvasMutationForUndo(userID, runID, canvasID)
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("当前运行没有可撤销或重做的画布操作")
		}
		return nil, err
	}
	if mutation.CanvasID != canvasID || mutation.HasSubmittedTask {
		return nil, BadAuthRequest("已提交的生成任务不能通过画布历史撤销或重做")
	}
	target, expected, status, label := mutation.BeforeJSON, mutation.AfterSnapshotHash, "applied", "撤销"
	if call.Function.Name == "canvas_redo" {
		target, expected, status, label = mutation.AfterJSON, mutation.BeforeSnapshotHash, "undone", "重做"
	}
	if target == "" || mutation.Status != status {
		return nil, BadAuthRequest("当前操作没有可用的" + label + "快照")
	}
	if args.SnapshotHash != hash || hash != expected {
		return nil, cloudAgentSnapshotConflictError("画布发生后续变化，未执行" + label)
	}
	targetDoc, err := creationDocument(target)
	if err != nil {
		return nil, err
	}
	targetHash := mutation.BeforeSnapshotHash
	if call.Function.Name == "canvas_redo" {
		targetHash = mutation.AfterSnapshotHash
	}
	if cloudAgentCanvasHash(targetDoc) != targetHash {
		return nil, BadAuthRequest("画布历史快照无效")
	}
	return &cloudAgentHistoryPlan{Canvas: canvas, Mutation: mutation, Document: targetDoc, BeforeJSON: canvas.PayloadJSON, BeforeHash: hash, Preview: cloudAgentApprovalPreview{Kind: "canvas_history", Title: label + "画布操作", Description: label + "本次运行的最近一项可恢复画布变更，已提交任务和费用保持原状。", Items: []cloudAgentApprovalPreviewItem{{Operation: call.Function.Name, Summary: label + mutation.Operation}}}}, nil
}
func applyCloudAgentHistory(repo *repository.Repository, userID, canvasID, runID string, state *cloudAgentRuntime, call cloudAgentCall, policy RuntimePolicySetting) (any, error) {
	plan, err := prepareCloudAgentHistory(repo, userID, canvasID, runID, call)
	if err != nil {
		return nil, err
	}
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	if call.Function.Name == "canvas_redo" {
		err = repo.MarkCloudAgentCanvasMutationReapplied(userID, runID, plan.Mutation.ID)
	} else {
		err = repo.MarkCloudAgentCanvasMutationUndone(userID, runID, plan.Mutation.ID, time.Now().UTC())
	}
	if err != nil {
		return nil, err
	}
	if err := emitCloudAgentCanvasChange(repo, runID, state, cloudAgentMutationInput{UserID: userID, CanvasID: canvasID, BeforeJSON: plan.BeforeJSON, Operation: call.Function.Name, Preview: &plan.Preview}); err != nil {
		return nil, err
	}
	return map[string]any{"accepted": true, "canvasId": canvasID, "snapshotHash": cloudAgentCanvasHash(plan.Document), "operationId": plan.Mutation.ID, "summary": plan.Preview.Description}, nil
}
