package app

import (
	"encoding/json"
	"fmt"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/repository"
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

// 行与输出的 metadata 可以被历史回滚，所以 Agent 的产物归属写入也要核对持久任务
// 绑定：只有已受理且未终结的任务才能证明该槽位仍被占用。原生命令走
// commands.Service 的同类校验，这里覆盖 app 侧的语义入口。
type cloudAgentActiveConsumer struct {
	RowID        string `json:"rowId"`
	OutputNodeID string `json:"outputNodeId"`
}

func validateCloudAgentStoryboardOccupancy(repo *repository.Repository, userID, canvasID, boardID, rowID, outputID string) error {
	epoch, err := repo.CommandEpoch(userID, canvasID)
	if err != nil {
		if repo.IsMissing(err) {
			return nil
		}
		return err
	}
	bindings, err := repo.ActiveCanvasTaskBindings(userID, canvasID, epoch.Epoch)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		var consumer cloudAgentActiveConsumer
		if err = json.Unmarshal([]byte(binding.ConsumerJSON), &consumer); err != nil {
			return err
		}
		if binding.TargetNodeID == boardID && consumer.RowID == rowID {
			return cloudAgentFieldError("rowId", "reference_busy", "该分镜行仍有未完成任务，请先处理已有任务")
		}
		if outputID != "" && (binding.TargetNodeID == outputID || consumer.OutputNodeID == outputID) {
			return cloudAgentFieldError("outputNodeId", "reference_busy", "该产物仍有未完成任务，请先处理已有任务")
		}
	}
	return nil
}
func prepareCloudAgentStoryboardReferenceEdit(repo *repository.Repository, userID, canvasID string, args cloudAgentStoryboardEditArgs) (*cloudAgentStoryboardMutationPlan, error) {
	if len(args.RowIDs) > 0 || args.Assets != nil {
		return nil, BadAuthRequest("共享引用与产物归属动作不接受 rowIds 或 assets")
	}
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
	if args.References != nil {
		if !cloudAgentStoryboardSharedAction(args.Action) || args.ReferenceNodeIDs != nil {
			return nil, BadAuthRequest("references 仅用于 set_references，且不能与 referenceNodeIds 同时提交")
		}
		args.ReferenceNodeIDs, err = cloudAgentResolveBusinessReferences(doc, args.References)
		if err != nil {
			return nil, err
		}
	}
	// 共享引用声明（set_references）是引用合同专属语义：历史画布必须先完成一次规范化，
	// 否则服务端不会按字段派生托管连接。产物归属（bind_output／unbind_output）写的是
	// 历史画布同样有效的行归属字段，不受这道门禁影响。
	if cloudAgentStoryboardSharedAction(args.Action) {
		if err := validateCloudAgentReferenceMigration(doc); err != nil {
			return nil, err
		}
	}
	_, board, _, err := storyboardNodeFromDocument(doc, args.NodeID)
	if err != nil {
		return nil, err
	}
	summary := ""
	fields := []string{}
	if cloudAgentStoryboardSharedAction(args.Action) {
		if args.RowID != "" || args.ReferenceNodeIDs == nil || len(args.ReferenceNodeIDs) > 100 || args.OutputMode != "" || args.OutputNodeID != "" {
			return nil, BadAuthRequest("共享引用需要 referenceNodeIds，不接受行或输出参数")
		}
		next := append([]string{}, cloudAgentStrings(board["referenceNodeIds"])...)
		if args.Action == "set_references" {
			next = args.ReferenceNodeIDs
		} else {
			if len(args.ReferenceNodeIDs) == 0 {
				return nil, BadAuthRequest("增量共享引用需要非空 references")
			}
			if _, err = cloudAgentResolveBusinessReferences(doc, args.ReferenceNodeIDs); err != nil {
				return nil, err
			}
			for _, id := range args.ReferenceNodeIDs {
				if args.Action == "add_references" {
					if !cloudAgentContainsString(next, id) {
						next = append(next, id)
					}
				} else {
					retained := []string{}
					for _, old := range next {
						if old != id {
							retained = append(retained, old)
						}
					}
					next = retained
				}
			}
		}
		if len(next) > 100 {
			return nil, BadAuthRequest("共享引用最多100项")
		}
		board["referenceNodeIds"] = next
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
		// 绑定与解除都先核对持久占用：解除一个正在生成中的槽位同样会打乱回填。
		if err = validateCloudAgentStoryboardOccupancy(repo, userID, canvasID, args.NodeID, args.RowID, output); err != nil {
			return nil, err
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
