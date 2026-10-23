package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/references"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func validateCloudAgentStoryboardInput(arguments string, a cloudAgentMediaArgs) error {
	if a.StoryboardNodeID == "" && a.StoryboardRowID == "" {
		return nil
	}
	if a.StoryboardNodeID == "" || a.StoryboardRowID == "" {
		return cloudAgentFieldError("storyboardNodeId", "storyboard_input_conflict", "分镜输入需要同时指定表和行")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &fields); err != nil {
		return err
	}
	for _, field := range []string{"prompt", "sourceNodeId", "referenceNodeIds", "referenceTransientIds", "durationSeconds"} {
		if _, ok := fields[field]; ok {
			return cloudAgentFieldError(field, "storyboard_input_conflict", "分镜输入由行字段确定，请移除重复的 "+field)
		}
	}
	return nil
}

func resolveCloudAgentStoryboardGeneration(doc map[string]any, a cloudAgentMediaArgs) (cloudAgentMediaArgs, tasks.Consumer, error) {
	if a.StoryboardNodeID == "" && a.StoryboardRowID == "" {
		return a, tasks.Consumer{}, nil
	}
	if !references.Enabled(doc) {
		return a, tasks.Consumer{}, cloudAgentFieldError("storyboardNodeId", "reference_migration_required", "请先在画布中规范化历史引用")
	}
	if a.StoryboardNodeID == "" || a.StoryboardRowID == "" || (a.Mode != "image" && a.Mode != "video") {
		return a, tasks.Consumer{}, BadAuthRequest("分镜生成需要表、行及 image/video 模式")
	}
	_, board, _, err := storyboardNodeFromDocument(doc, a.StoryboardNodeID)
	if err != nil {
		return a, tasks.Consumer{}, err
	}
	var row map[string]any
	for _, r := range creationMaps(board["rows"]) {
		if stringValue(r["id"]) == a.StoryboardRowID {
			row = r
			break
		}
	}
	if row == nil {
		return a, tasks.Consumer{}, BadAuthRequest("分镜行不存在")
	}
	if bound := stringValue(row[a.Mode+"NodeId"]); a.NodeID != "" && bound != "" && bound != a.NodeID {
		return a, tasks.Consumer{}, cloudAgentFieldError(a.Mode+"NodeId", "output_binding_conflict", "分镜已有输出归属，请续用空闲草稿或先明确解除原输出绑定")
	}
	resolved, err := references.Resolve(doc, references.Scope{Kind: "storyboard-row", NodeID: a.StoryboardNodeID, RowID: a.StoryboardRowID, Mode: a.Mode}, capability.BuiltinRegistry())
	if err != nil {
		return a, tasks.Consumer{}, cloudAgentReferenceError(err)
	}
	a.ResolvedBindings = resolved.Bindings
	a.ReferenceNodeIDs = []string{}
	if a.Mode == "video" {
		seconds, _ := row["durationSeconds"].(float64)
		a.Duration = int(seconds)
	}
	a.Prompt, _ = cloudAgentStoryboardEffectivePrompt(row, a.Mode)
	nodes := map[string]map[string]any{}
	for _, node := range creationMaps(doc["nodes"]) {
		nodes[stringValue(node["id"])] = node
	}
	for _, binding := range resolved.Bindings {
		if binding.Role == "first-frame" {
			status := stringValue(cloudAgentNodeMetadata(nodes[binding.NodeID])["status"])
			if status == "loading" || status == "error" {
				return a, tasks.Consumer{}, BadAuthRequest("分镜首帧尚未就绪")
			}
		}
		if binding.Role == "source-text" {
			content := strings.TrimSpace(stringValue(cloudAgentNodeMetadata(nodes[binding.NodeID])["content"]))
			if content != "" {
				a.Prompt += "\n\n" + content
			}
		} else {
			a.ReferenceNodeIDs = append(a.ReferenceNodeIDs, binding.NodeID)
		}
	}
	if len(a.ReferenceNodeIDs) > 16 {
		return a, tasks.Consumer{}, BadAuthRequest("参考节点最多 16 个；请在分镜中调整素材")
	}
	return a, tasks.Consumer{Kind: "storyboard", RowID: a.StoryboardRowID, Field: a.Mode + "NodeId", OutputNodeID: a.NodeID}, nil
}

func cloudAgentStoryboardEffectivePrompt(row map[string]any, mode string) (string, string) {
	field := "imageGenerationPrompt"
	if mode == "video" {
		field = "videoMotionPrompt"
	}
	if prompt := strings.TrimSpace(stringValue(row[field])); prompt != "" {
		return prompt, field
	}
	if prompt := strings.TrimSpace(stringValue(row["plotDescription"])); prompt != "" {
		return prompt, "plotDescription"
	}
	return "", "none"
}

func cloudAgentMediaCallArguments(a cloudAgentMediaArgs) ([]byte, error) {
	raw, err := json.Marshal(a)
	if err != nil || a.StoryboardNodeID == "" {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"prompt", "sourceNodeId", "referenceNodeIds", "referenceTransientIds", "durationSeconds"} {
		delete(fields, key)
	}
	return json.Marshal(fields)
}

func (s *Service) resolveCloudAgentStoryboardArgs(run *model.CloudAgentExecution, state *cloudAgentRuntime, callID, arguments string, a cloudAgentMediaArgs) (cloudAgentMediaArgs, error) {
	if err := validateCloudAgentStoryboardInput(arguments, a); err != nil {
		return a, err
	}
	if a.StoryboardNodeID == "" {
		return a, nil
	}
	cs, _ := s.CanvasCommandServices()
	principal := identity.Principal{ID: run.UserID, OwnerID: run.UserID, Role: "owner", ClientID: "agent:" + run.ID, Scopes: identity.OwnerScopes()}
	snapshot, err := cs.ReadSnapshot(context.Background(), principal, state.Request.CanvasID)
	if err != nil {
		return a, err
	}
	a, _, err = resolveCloudAgentStoryboardGeneration(snapshot.Document, a)
	if err == nil {
		a, err = cloudAgentStoryboardOutputTarget(snapshot.Document, a, run.UserID, fmt.Sprintf("%s:%d:%s:%s", run.ID, state.Step, state.LastStepTaskID, callID))
	}
	return a, err
}

func cloudAgentStoryboardOutputTarget(doc map[string]any, a cloudAgentMediaArgs, userID, seed string) (cloudAgentMediaArgs, error) {
	if a.StoryboardNodeID == "" {
		return a, nil
	}
	_, _, rows, err := storyboardNodeFromDocument(doc, a.StoryboardNodeID)
	if err != nil {
		return a, err
	}
	index := cloudAgentNodeIndex(rows, a.StoryboardRowID)
	if index < 0 {
		return a, BadAuthRequest("分镜行不存在")
	}
	row := rows[index]
	if a.NodeID == "" {
		a.NodeID = stringValue(row[a.Mode+"NodeId"])
		if a.NodeID == "" {
			a.NodeID = cloudAgentID(userID, "storyboard-output:"+a.StoryboardNodeID+":"+a.StoryboardRowID+":"+a.Mode+":"+seed)
		}
	}
	if a.Title == "" {
		a.Title = fmt.Sprintf("镜头 %v · %s", row["shotNumber"], map[string]string{"image": "关键帧", "video": "视频"}[a.Mode])
	}
	return a, nil
}

// Called within the existing Agent admission transaction, after billing/task
// persistence. The structured materializer owns all subsequent canvas writes.
func registerCloudAgentStoryboardTask(repo *repository.Repository, run *model.CloudAgentExecution, canvasID string, plan *cloudAgentMediaPlan, task *model.Task) error {
	if plan.Args.StoryboardNodeID == "" {
		return nil
	}
	epoch, err := repo.CommandEpoch(run.UserID, canvasID)
	if err != nil {
		return err
	}
	consumer, _ := json.Marshal(tasks.Consumer{Kind: "storyboard", RowID: plan.Args.StoryboardRowID, Field: plan.Args.Mode + "NodeId", OutputNodeID: plan.Args.NodeID})
	binding := model.CanvasTaskBinding{TaskID: task.ID, OwnerID: run.UserID, CanvasID: canvasID, Epoch: epoch.Epoch, ActorID: run.UserID, ClientID: "agent:" + run.ID, OperationID: plan.CallID, TargetNodeID: plan.Args.StoryboardNodeID, Mode: plan.Args.Mode, ConsumerJSON: string(consumer), CreatedAt: time.Now()}
	if err = repo.InsertCanvasTaskBinding(&binding); err != nil {
		return err
	}
	var input map[string]any
	if err = json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return err
	}
	for _, field := range []string{"referenceImages", "referenceVideos", "referenceAudios"} {
		for _, ref := range creationMaps(input[field]) {
			if id := strings.TrimPrefix(stringValue(ref["storageKey"]), "resource:"); id != "" {
				if err = repo.SaveCanvasTaskResource(task.ID, run.UserID, id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func cloudAgentStoryboardConsumer(repo *repository.Repository, taskID string) (*model.CanvasTaskBinding, tasks.Consumer, error) {
	binding, err := repo.CanvasTaskBinding(taskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, tasks.Consumer{}, nil
	}
	if err != nil {
		return nil, tasks.Consumer{}, err
	}
	var consumer tasks.Consumer
	if err = json.Unmarshal([]byte(binding.ConsumerJSON), &consumer); err != nil {
		return nil, consumer, err
	}
	if consumer.Kind != "storyboard" {
		return nil, consumer, nil
	}
	return binding, consumer, nil
}
func (s *Service) materializeCloudAgentStoryboardTask(task *model.Task) error {
	if !cloudAgentHasStoryboardTarget(task) {
		return nil
	}
	binding, _, err := cloudAgentStoryboardConsumer(s.repo, task.ID)
	if err != nil || binding == nil {
		return err
	}
	if err = s.repo.EnqueueCanvasMaterialization(task.ID); err != nil {
		return err
	}
	_, service := s.CanvasCommandServices()
	return service.Materialize(context.Background(), task.ID)
}

func cloudAgentHasStoryboardTarget(task *model.Task) bool {
	var input struct {
		Metadata struct {
			StoryboardNodeID string `json:"storyboardNodeId"`
		} `json:"metadata"`
	}
	return json.Unmarshal([]byte(task.InputJSON), &input) == nil && input.Metadata.StoryboardNodeID != ""
}

func bindCloudAgentStoryboardOutput(repo *repository.Repository, userID string, doc map[string]any, a cloudAgentMediaArgs, task *model.Task) error {
	if a.StoryboardNodeID == "" {
		return nil
	}
	epoch, err := repo.CommandEpoch(userID, stringValue(doc["id"]))
	if err == nil {
		active, e := repo.ActiveCanvasTaskBindings(userID, stringValue(doc["id"]), epoch.Epoch)
		if e != nil {
			return e
		}
		for _, b := range active {
			var c tasks.Consumer
			_ = json.Unmarshal([]byte(b.ConsumerJSON), &c)
			if b.TargetNodeID == a.StoryboardNodeID && c.RowID == a.StoryboardRowID {
				return creationConflict("该分镜行已有活动任务")
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err = references.BindStoryboardOutput(doc, a.StoryboardNodeID, a.StoryboardRowID, a.Mode, a.NodeID); err != nil {
		return cloudAgentReferenceError(err)
	}
	if task != nil {
		_, board, _, _ := storyboardNodeFromDocument(doc, a.StoryboardNodeID)
		for _, row := range creationMaps(board["rows"]) {
			if stringValue(row["id"]) == a.StoryboardRowID {
				row["taskId"], row["taskStatus"], row["status"] = task.ID, string(task.Status), "loading"
			}
		}
	}
	return nil
}

func prepareCanvasStoryboardInput(_ context.Context, repo *repository.Repository, p identity.Principal, doc commands.Document, req tasks.SubmitRequest) (tasks.TaskRequest, error) {
	if !references.Enabled(doc) {
		return req.Task, cloudAgentFieldError("storyboardNodeId", "reference_migration_required", "请先在画布中规范化历史引用")
	}
	a, _, err := resolveCloudAgentStoryboardGeneration(doc, cloudAgentMediaArgs{Mode: req.Task.Mode, StoryboardNodeID: req.TargetNodeID, StoryboardRowID: req.Consumer.RowID, NodeID: req.Consumer.OutputNodeID})
	if err != nil {
		return req.Task, err
	}
	canvas, err := repo.CanvasProjectForUser(p.OwnerID, req.Command.CanvasID)
	if err != nil {
		return req.Task, err
	}
	nodes := map[string]map[string]any{}
	for _, n := range creationMaps(doc["nodes"]) {
		nodes[stringValue(n["id"])] = n
	}
	refs := map[string]any{}
	for _, id := range a.ReferenceNodeIDs {
		ref, field, err := cloudAgentMediaReference(repo, p.OwnerID, canvas.ProjectID, nodes[id])
		if err != nil {
			return req.Task, err
		}
		list, _ := refs[field].([]any)
		refs[field] = append(list, ref)
	}
	if err = validateCloudAgentMediaReferences(a.Mode, refs); err != nil {
		return req.Task, err
	}
	prompt, err := cloudAgentMediaReferencePrompt(a.Prompt, refs)
	if err != nil {
		return req.Task, err
	}
	config := map[string]any{}
	if original, ok := req.Task.Input["config"].(map[string]any); ok {
		for k, v := range original {
			config[k] = v
		}
	}
	if a.Mode == "video" {
		config["videoSeconds"] = strconv.Itoa(a.Duration)
	}
	metadata := map[string]any{"storyboardNodeId": a.StoryboardNodeID, "storyboardRowId": a.StoryboardRowID}
	for _, b := range a.ResolvedBindings {
		if b.Role == "first-frame" {
			metadata["videoStartFrameNodeId"] = b.NodeID
		}
		if b.Role == "last-frame" {
			metadata["videoEndFrameNodeId"] = b.NodeID
		}
	}
	refs["mode"], refs["prompt"], refs["config"], refs["metadata"] = a.Mode, prompt, config, metadata
	resolved := req.Task
	resolved.Input = refs
	resolved.Prompt = prompt
	resolved.Operation = cloudAgentMediaOperation(a.Mode, refs)
	return resolved, nil
}
