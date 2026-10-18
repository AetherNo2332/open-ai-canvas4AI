package app

import (
	"fmt"
	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/references"
	"infinite-canvas/backend/internal/repository"
)

func reconcileCloudAgentReferenceFields(doc map[string]any, beforeJSON string) error {
	if !references.Enabled(doc) {
		return nil
	}
	before, err := creationDocument(beforeJSON)
	if err != nil {
		return err
	}
	result, err := references.Reconcile(before, doc, capability.BuiltinRegistry(), func() string { return "ref-" + newID() })
	if err != nil {
		return cloudAgentReferenceError(err)
	}
	for key := range doc {
		delete(doc, key)
	}
	for key, value := range result.Document {
		doc[key] = value
	}
	return nil
}
func cloudAgentReferenceError(err error) error {
	if typed, ok := err.(*references.Error); ok {
		return cloudAgentFieldError(typed.Field, typed.Reason, typed.Message)
	}
	return err
}
func prepareCloudAgentStoryboardReferenceEdit(repo *repository.Repository, userID, canvasID string, args cloudAgentStoryboardEditArgs) (*cloudAgentStoryboardMutationPlan, error) {
	if len(args.Patch) != 0 {
		return nil, BadAuthRequest("引用或产物归属动作不接受行 patch")
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	beforeHash := cloudAgentCanvasHash(doc)
	if args.SnapshotHash != beforeHash {
		return nil, cloudAgentSnapshotConflictError("画布已变化，请重新读取后修改引用")
	}
	_, board, _, err := storyboardNodeFromDocument(doc, args.NodeID)
	if err != nil {
		return nil, err
	}
	summary := ""
	fields := []string{}
	if args.Action == "set_references" {
		if args.RowID != "" || args.ReferenceNodeIDs == nil || len(args.ReferenceNodeIDs) > 100 || args.OutputMode != "" || args.OutputNodeID != "" {
			return nil, BadAuthRequest("共享引用需要 referenceNodeIds，不接受行或输出参数")
		}
		board["referenceNodeIds"] = args.ReferenceNodeIDs
		if _, err = references.Resolve(doc, references.Scope{Kind: "storyboard-context", NodeID: args.NodeID}, capability.BuiltinRegistry()); err != nil {
			return nil, cloudAgentReferenceError(err)
		}
		summary = "修改分镜共享引用"
		fields = []string{"referenceNodeIds"}
	} else {
		if args.ReferenceNodeIDs != nil {
			return nil, BadAuthRequest("输出绑定不接受 referenceNodeIds")
		}
		output := args.OutputNodeID
		if args.Action == "unbind_output" {
			if output != "" {
				return nil, BadAuthRequest("解除输出不接受 outputNodeId")
			}
		} else if output == "" {
			return nil, BadAuthRequest("绑定输出需要 outputNodeId")
		}
		if err = references.BindStoryboardOutput(doc, args.NodeID, args.RowID, args.OutputMode, output); err != nil {
			return nil, cloudAgentReferenceError(err)
		}
		summary = fmt.Sprintf("修改镜头 %s 的 %s 输出归属", args.RowID, args.OutputMode)
		fields = []string{args.OutputMode + "NodeId"}
	}
	if err = reconcileCloudAgentReferenceFields(doc, canvas.PayloadJSON); err != nil {
		return nil, err
	}
	return &cloudAgentStoryboardMutationPlan{Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: beforeHash, Preview: cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "确认修改分镜引用", Description: summary, Items: []cloudAgentApprovalPreviewItem{{Operation: "edit_storyboard", NodeID: args.NodeID, NodeType: "script", NodeTypeLabel: "分镜脚本", Summary: summary, Fields: fields}}}}, nil
}
