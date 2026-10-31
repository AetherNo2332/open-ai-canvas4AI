package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"yingce/backend/internal/assets"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type Service struct {
	repo          *repository.Repository
	commands      *commands.Service
	submitter     TaskSubmitter
	options       ServiceOptions
	materializeMu sync.Mutex
}

type ServiceOptions struct {
	PrepareStoryboardInput func(context.Context, *repository.Repository, identity.Principal, commands.Document, SubmitRequest) (TaskRequest, error)
	ValidateOutputs        func(context.Context, *repository.Repository, string, int, int64) error
	PrepareOutput          func(context.Context, model.Task, model.CanvasTaskBinding) (model.Task, error)
}

func NewService(repo *repository.Repository, cs *commands.Service, submitter TaskSubmitter, options ...ServiceOptions) *Service {
	s := &Service{repo: repo, commands: cs, submitter: submitter}
	if len(options) > 0 {
		s.options = options[0]
	}
	return s
}
func (s *Service) ReadSubmission(ctx context.Context, p identity.Principal, canvas, epoch, client, op string) (TaskHandle, error) {
	receipt, err := s.commands.GetReceipt(ctx, p, canvas, epoch, client, op)
	if err != nil {
		return TaskHandle{}, err
	}
	binding, err := s.repo.CanvasTaskBindingForOperation(p.OwnerID, canvas, epoch, p.ID, client, op)
	if err != nil {
		return TaskHandle{}, &commands.Error{Reason: "receipt_missing", Message: "任务提交回执不存在"}
	}
	base, _, err := mediaversions.BindingBase(binding)
	if err != nil {
		return TaskHandle{}, err
	}
	return TaskHandle{TaskID: binding.TaskID, VersionID: base.ResultVersionID, Receipt: receipt}, nil
}
func (s *Service) Submit(ctx context.Context, p identity.Principal, req SubmitRequest) (TaskHandle, error) {
	if !p.HasScope("generation.submit") || p.Role == "viewer" {
		return TaskHandle{}, &commands.Error{Reason: "permission_denied", Message: "没有任务提交权限"}
	}
	if len(req.Command.Operations) > 0 {
		return TaskHandle{}, commands.BadAuthRequest("任务提交不能附带任意画布操作")
	}
	if strings.TrimSpace(req.Task.Prompt) == "" || len(req.Task.Prompt) > 200000 {
		return TaskHandle{}, commands.BadAuthRequest("任务提示词无效")
	}
	if req.Task.Mode != "text" && req.Task.Mode != "image" && req.Task.Mode != "video" && req.Task.Mode != "audio" {
		return TaskHandle{}, commands.BadAuthRequest("生成模式无效")
	}
	if req.Intent != "" && req.Intent != "create" && req.Intent != "new_version" {
		return TaskHandle{}, commands.BadAuthRequest("生成意图无效")
	}
	var taskID string
	receipt, fresh, err := s.commands.ExecuteHost(ctx, p, req.Command, "task_submit", req, false, func(tx *repository.Repository, doc commands.Document) (commands.Document, map[string]bool, error) {
		node := findNode(doc, req.TargetNodeID)
		if node == nil {
			return nil, nil, commands.BadAuthRequest("目标节点不存在")
		}
		if err := validateConsumer(node, req); err != nil {
			return nil, nil, err
		}
		if req.Consumer.Kind == "storyboard" && (req.Task.Mode == "image" || req.Task.Mode == "video") && s.options.PrepareStoryboardInput != nil {
			resolved, err := s.options.PrepareStoryboardInput(ctx, tx, p, doc, req)
			if err != nil {
				return nil, nil, err
			}
			req.Task = resolved
		}
		meta := metadata(node)
		if req.Consumer.Kind == "batch" || req.Consumer.Kind == "storyboard" {
			meta = consumerRow(node, req.Consumer)
		}
		if err := validateActiveTarget(tx, p, req); err != nil {
			return nil, nil, err
		}
		if req.Consumer.OutputNodeID != "" {
			output := findNode(doc, req.Consumer.OutputNodeID)
			if output == nil || str(output["type"]) != req.Task.Mode {
				return nil, nil, commands.BadAuthRequest("输出节点不存在或类型不符")
			}
		}
		if mediaversions.Supported(req.Task.Mode) {
			target := node
			if req.Consumer.OutputNodeID != "" {
				target = findNode(doc, req.Consumer.OutputNodeID)
			}
			scope, e := mediaversions.ScopeForNode(tx, p.OwnerID, req.Command.CanvasID, str(target["id"]))
			if e != nil {
				return nil, nil, mediaVersionError(e)
			}
			if e = mediaversions.CheckAdmission(tx, scope, target, req.Intent); e != nil {
				return nil, nil, mediaVersionError(e)
			}
		}
		raw, err := json.Marshal(req.Task.Input)
		if err != nil {
			return nil, nil, err
		}
		input := map[string]any{}
		if string(raw) != "null" {
			if err = json.Unmarshal(raw, &input); err != nil {
				return nil, nil, err
			}
		}
		reads, err := s.commands.ValidateTaskReferences(ctx, tx, p, doc, input)
		if err != nil {
			return nil, nil, err
		}
		reads["node:"+req.TargetNodeID] = true
		emotionJSON, emotionReads, err := s.captureEmotion(ctx, tx, p, doc, node, req)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range emotionReads {
			reads[k] = v
		}
		var batchContextJSON string
		if req.Consumer.Kind == "batch_document" {
			m, _ := input["metadata"].(map[string]any)
			options, _ := m["listAnalysis"].(map[string]any)
			if options == nil {
				return nil, nil, commands.BadAuthRequest("列表分析缺少结果定义")
			}
			config, _ := input["config"].(map[string]any)
			batch, err := commands.PrepareBatchDocument(doc, req.TargetNodeID, options, str(config["model"]))
			if err != nil {
				return nil, nil, err
			}
			for _, id := range batch.InputNodeIDs {
				reads["node:"+id] = true
			}
			raw, err := json.Marshal(batch)
			if err != nil {
				return nil, nil, err
			}
			batchContextJSON = string(raw)
		}
		serverTask := req.Task
		serverTask.Input = input
		taskMetadata, _ := input["metadata"].(map[string]any)
		if taskMetadata == nil {
			taskMetadata = map[string]any{}
			input["metadata"] = taskMetadata
		}
		if req.Consumer.Kind == "storyboard_document" {
			// 拆镜提示词一律由服务端从画布组装；调用方只能给出修改意见、镜头数和单镜时长。
			split, _ := taskMetadata["storyboardSplit"].(map[string]any)
			scope, err := commands.PlanStoryboardSplitScope(doc, req.TargetNodeID, req.Consumer.RowIDs)
			if err != nil {
				return nil, nil, err
			}
			prompt, err := commands.BuildStoryboardSplitPrompt(doc, req.TargetNodeID, scope, commands.StoryboardSplitPromptInput{
				Request:             str(split["request"]),
				ShotCount:           intValue(split["shotCount"]),
				ShotDurationSeconds: intValue(split["shotDurationSeconds"]),
				SkillContext:        str(split["skillContext"]),
			})
			if err != nil {
				return nil, nil, err
			}
			serverTask.Prompt = prompt
			input["prompt"] = prompt
			delete(taskMetadata, "storyboardSplit")
		}
		taskMetadata["canvasId"], taskMetadata["nodeId"], taskMetadata["documentEpoch"] = req.Command.CanvasID, req.TargetNodeID, req.Command.DocumentEpoch
		prepared, err := s.submitter.Prepare(ctx, p, serverTask)
		if err != nil {
			return nil, nil, err
		}
		if prepared.Task.ID == "" || prepared.Task.UserID != p.OwnerID || prepared.Task.Status != model.TaskStatusQueued {
			return nil, nil, fmt.Errorf("invalid prepared task")
		}
		prepared.Task.ProjectID = req.Command.CanvasID
		if err = s.submitter.Commit(ctx, tx, prepared); err != nil {
			return nil, nil, err
		}
		consumer, _ := json.Marshal(req.Consumer)
		binding := model.CanvasTaskBinding{TaskID: prepared.Task.ID, OwnerID: p.OwnerID, CanvasID: req.Command.CanvasID, Epoch: req.Command.DocumentEpoch, ActorID: p.ID, ClientID: req.Command.ClientID, OperationID: req.Command.OperationID, TargetNodeID: req.TargetNodeID, Mode: req.Task.Mode, ConsumerJSON: string(consumer), OriginalContent: str(meta["content"]), CreatedAt: time.Now()}
		binding.ResultContextJSON = emotionJSON
		if req.Consumer.Kind == "batch_document" {
			binding.ResultContextJSON = batchContextJSON
		}
		if req.Consumer.Kind == "storyboard_document" {
			raw, err := json.Marshal(meta["storyboard"])
			if err != nil {
				return nil, nil, err
			}
			binding.OriginalContent = string(raw)
			// 重拆范围在提交时冻结：校验行存在、未锁定且连续；写回只按冻结的范围替换。
			scope, err := commands.PlanStoryboardSplitScope(doc, req.TargetNodeID, req.Consumer.RowIDs)
			if err != nil {
				return nil, nil, err
			}
			scopeJSON, err := json.Marshal(scope)
			if err != nil {
				return nil, nil, err
			}
			binding.ResultContextJSON = string(scopeJSON)
		}
		versioned := mediaversions.Supported(req.Task.Mode)
		var target map[string]any
		if versioned {
			target = node
			if req.Consumer.OutputNodeID != "" {
				target = findNode(doc, req.Consumer.OutputNodeID)
			}
			if target == nil {
				return nil, nil, commands.BadAuthRequest("媒体输出节点不存在")
			}
			if _, err := mediaversions.ScopeForNode(tx, p.OwnerID, req.Command.CanvasID, str(target["id"])); err != nil {
				return nil, nil, mediaVersionError(err)
			}
			base, err := mediaversions.Begin(target, prepared.Task, objectList(doc["nodes"]))
			if err != nil {
				return nil, nil, mediaVersionError(err)
			}
			binding.MediaVersionJSON = base.Encode()
		}
		if err = tx.InsertCanvasTaskBinding(&binding); err != nil {
			return nil, nil, err
		}
		resourceIDs := map[string]struct{}{}
		inputRaw, err := json.Marshal(input)
		if err != nil {
			return nil, nil, err
		}
		if err = assets.CollectOwnedDocumentReferences(string(inputRaw), resourceIDs); err != nil {
			return nil, nil, err
		}
		if err = assets.CollectOwnedDocumentReferences(emotionJSON, resourceIDs); err != nil {
			return nil, nil, err
		}
		for id := range resourceIDs {
			if err = tx.SaveCanvasTaskResource(binding.TaskID, binding.OwnerID, id); err != nil {
				return nil, nil, err
			}
		}
		if versioned {
			if req.Consumer.Kind == "storyboard" || req.Consumer.Kind == "batch" {
				setPending(node, req, prepared.Task)
			}
			reads["node:"+str(target["id"])] = true
		} else {
			setPending(node, req, prepared.Task)
		}
		if req.Consumer.OutputNodeID != "" {
			output := findNode(doc, req.Consumer.OutputNodeID)
			if output == nil || str(output["type"]) != req.Task.Mode {
				return nil, nil, commands.BadAuthRequest("输出节点不存在或类型不符")
			}
			if !versioned {
				setPending(output, SubmitRequest{Task: req.Task}, prepared.Task)
			}
			reads["node:"+req.Consumer.OutputNodeID] = true
		}
		taskID = prepared.Task.ID
		return doc, reads, nil
	})
	if err != nil {
		return TaskHandle{}, err
	}
	if taskID == "" {
		binding, err := s.repo.CanvasTaskBindingForOperation(p.OwnerID, req.Command.CanvasID, req.Command.DocumentEpoch, p.ID, req.Command.ClientID, req.Command.OperationID)
		if err != nil {
			return TaskHandle{}, err
		}
		taskID = binding.TaskID
	}
	if fresh {
		s.submitter.Wake()
	}
	return TaskHandle{TaskID: taskID, Receipt: receipt}, nil
}
func findNode(doc commands.Document, id string) map[string]any {
	for _, n := range objectList(doc["nodes"]) {
		if str(n["id"]) == id {
			return n
		}
	}
	return nil
}
func metadata(n map[string]any) map[string]any {
	m, _ := n["metadata"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		n["metadata"] = m
	}
	return m
}
func objectList(v any) []map[string]any {
	result := []map[string]any{}
	switch a := v.(type) {
	case []any:
		for _, v := range a {
			if n, ok := v.(map[string]any); ok {
				result = append(result, n)
			}
		}
	case []map[string]any:
		return a
	}
	return result
}
func str(v any) string { s, _ := v.(string); return s }

// intValue 读取 JSON 数字字段；缺失、非数字或越界时返回 0（表示由脚本自行决定）。
func intValue(v any) int {
	number, ok := v.(float64)
	if !ok || number < 0 || number > 1000 {
		return 0
	}
	return int(number)
}
func contains(a, b string) bool { return strings.Contains(a, b) }
