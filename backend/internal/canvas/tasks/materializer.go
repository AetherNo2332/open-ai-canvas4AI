package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strconv"
	"strings"
	"time"
)

// Outputs/asset registration is durable before canvas application. A crash
// between these stages retries ONLY the command, never the provider request.
func (s *Service) Materialize(ctx context.Context, taskID string) error {
	s.materializeMu.Lock()
	defer s.materializeMu.Unlock()
	job, err := s.repo.CanvasMaterialization(taskID)
	if err != nil {
		return err
	}
	if job.State == "done" {
		return nil
	}
	task, err := s.repo.Task(taskID)
	if err != nil {
		return err
	}
	if !terminal(task.Status) {
		return fmt.Errorf("task is not terminal")
	}
	binding, err := s.repo.CanvasTaskBinding(taskID)
	if err != nil {
		return err
	}
	if task.UserID != binding.OwnerID {
		return fmt.Errorf("task binding owner mismatch")
	}
	if task.Status == model.TaskStatusSucceeded && s.options.PrepareOutput != nil {
		transformed, e := s.options.PrepareOutput(ctx, *task, *binding)
		if e != nil {
			return e
		}
		task = &transformed
	}
	outputs, err := s.materializeOutputs(ctx, *task)
	if err != nil {
		return err
	}
	version, err := s.repo.CanvasMediaVersionForTask(task.UserID, task.ID)
	if err != nil {
		return err
	}
	p := identity.Principal{ID: binding.OwnerID, OwnerID: binding.OwnerID, ClientID: binding.ClientID, Role: "owner", Scopes: identity.OwnerScopes()}
	origin := identity.Principal{ID: binding.ActorID, OwnerID: binding.OwnerID, ClientID: binding.ClientID}
	req := commands.Command{Protocol: 1, CanvasID: binding.CanvasID, DocumentEpoch: binding.Epoch, ClientID: p.ClientID, OperationID: "task-result:" + taskID}
	if binding.ResolutionVersion > 0 {
		req.OperationID += fmt.Sprintf(":resolution:%d", binding.ResolutionVersion)
	}
	outcome := "applied"
	_, _, err = s.commands.ExecuteHost(ctx, p, req, "task_result", map[string]any{"taskId": taskID, "status": task.Status, "resultJson": task.ResultJSON, "error": task.Error}, true, func(tx *repository.Repository, doc commands.Document) (next commands.Document, reads map[string]bool, applyErr error) {
		defer func() {
			if applyErr == nil {
				applyErr = tx.FinishCanvasMaterialization(taskID, outcome, binding.ResolutionVersion)
			}
		}()
		if version != nil {
			var result commands.Document
			result, outcome, err = settleMediaVersion(tx, doc, *binding, *task, *version, outputs)
			if err == nil && outcome == "target_changed" {
				return nil, nil, errMediaVersionTargetChanged
			}
			return result, nil, err
		}
		if binding.Mode == "previs" {
			var input commands.PrevisRenderInput
			if err := json.Unmarshal([]byte(binding.ResultContextJSON), &input); err != nil {
				return nil, nil, err
			}
			scene := commands.PrevisScene(doc, input.SceneID)
			if scene == nil {
				outcome = "scene_deleted"
				return doc, nil, nil
			}
			if str(scene["renderTaskId"]) != taskID {
				outcome = "binding_changed"
				return doc, nil, nil
			}
			scene["renderTaskStatus"] = string(task.Status)
			if task.Status != model.TaskStatusSucceeded {
				scene["renderError"] = safeError(*task)
				return doc, nil, nil
			}
			var result commands.PrevisOutput
			if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
				return nil, nil, err
			}
			updated, reason, err := commands.ApplyPrevisResult(doc, input, taskID, result)
			outcome = reason
			if err == nil && reason == "applied" {
				for _, output := range outputs {
					nodeID := "previs-video-" + taskID
					if output.Kind == "image" {
						nodeID = "previs-preview-" + taskID
					}
					metadata(findNode(updated, nodeID))["assetId"] = output.AssetID
				}
			}
			return updated, nil, err
		}
		node := findNode(doc, binding.TargetNodeID)
		if node == nil {
			outcome = "target_deleted"
			return doc, nil, nil
		}
		var consumer Consumer
		if err := json.Unmarshal([]byte(binding.ConsumerJSON), &consumer); err != nil {
			return nil, nil, err
		}
		if consumer.Kind == "batch" || consumer.Kind == "storyboard" {
			row := consumerRow(node, consumer)
			if row == nil {
				outcome = "row_deleted"
				return doc, nil, nil
			}
			if str(row["taskId"]) != taskID {
				outcome = "binding_changed"
				return doc, nil, nil
			}
			// The row's declared output slot and the output's task identity are the
			// same durable claim for a failure as for a success: a terminal callback
			// may only write back while the row still owns this task's output.
			expected := consumer.OutputNodeID
			field := "outputNodeId"
			if consumer.Kind == "storyboard" {
				field = consumer.Field
			}
			if task.Status == model.TaskStatusSucceeded {
				if len(outputs) == 0 {
					return nil, nil, fmt.Errorf("task output missing")
				}
				if binding.Mode == "text" {
					data := outputData(outputs[0])
					row[consumer.Field] = str(data["content"])
				} else {
					result := findNode(doc, expected)
					if result == nil || str(row[field]) != expected || str(metadata(result)["taskId"]) != taskID {
						outcome = "output_binding_changed"
						return doc, nil, nil
					}
					if err := applyOutput(result, *task, outputs[0]); err != nil {
						return nil, nil, err
					}
				}
			} else {
				if expected != "" && binding.Mode != "text" && str(row[field]) != expected {
					outcome = "output_binding_changed"
					return doc, nil, nil
				}
				row["errorDetails"] = safeError(*task)
				if output := findNode(doc, expected); output != nil && str(metadata(output)["taskId"]) == taskID {
					m := metadata(output)
					m["taskStatus"], m["status"], m["errorDetails"] = string(task.Status), resultStatus(task.Status), safeError(*task)
				}
			}
			row["taskStatus"], row["status"] = string(task.Status), resultStatus(task.Status)
		} else {
			m := metadata(node)
			if str(m["taskId"]) != taskID {
				outcome = "binding_changed"
				return doc, nil, nil
			}
			if consumer.Kind == "batch_document" && task.Status == model.TaskStatusSucceeded {
				var context commands.BatchDocumentContext
				if err := json.Unmarshal([]byte(binding.ResultContextJSON), &context); err != nil {
					return nil, nil, err
				}
				updated, err := commands.ApplyGeneratedBatchDocument(doc, taskID, task.ResultJSON, context)
				if err != nil {
					return nil, nil, err
				}
				meta := metadata(findNode(updated, binding.TargetNodeID))
				meta["taskStatus"], meta["status"], meta["taskProgress"] = "succeeded", "success", 100
				delete(meta, "errorDetails")
				return updated, nil, nil
			}
			if consumer.Kind == "storyboard_document" {
				current, encodeErr := json.Marshal(m["storyboard"])
				if encodeErr != nil {
					return nil, nil, encodeErr
				}
				if string(current) != binding.OriginalContent {
					outcome = "storyboard_edited"
					m["taskStatus"], m["status"] = string(task.Status), "idle"
					return doc, nil, nil
				}
				if task.Status == model.TaskStatusSucceeded {
					updated, applyErr := commands.ApplyGeneratedStoryboard(doc, binding.TargetNodeID, taskID, task.ResultJSON)
					if applyErr != nil {
						return nil, nil, applyErr
					}
					m = metadata(findNode(updated, binding.TargetNodeID))
					m["taskStatus"], m["status"], m["taskProgress"] = string(task.Status), "success", 100
					delete(m, "errorDetails")
					return updated, nil, nil
				}
			}
			if binding.Mode == "text" && str(m["content"]) != binding.OriginalContent {
				outcome = "text_edited"
				m["taskStatus"], m["status"] = string(task.Status), "idle"
				return doc, nil, nil
			}
			if task.Status == model.TaskStatusSucceeded {
				if len(outputs) == 0 {
					return nil, nil, fmt.Errorf("task output missing")
				}
				if err := applyOutput(node, *task, outputs[0]); err != nil {
					return nil, nil, err
				}
				selectVersion(doc, node)
			} else {
				m["taskStatus"], m["status"], m["errorDetails"] = string(task.Status), resultStatus(task.Status), safeError(*task)
			}
		}
		return doc, nil, nil
	}, origin)
	if err != nil {
		e, commandError := err.(*commands.Error)
		if errors.Is(err, errMediaVersionTargetChanged) || (commandError && (e.Reason == "document_epoch_mismatch" || e.Reason == "entity_deleted")) {
			if commandError {
				outcome = e.Reason
			} else {
				outcome = "target_changed"
			}
			if version != nil {
				return s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
					if err := tx.LockCanvasWrite(binding.OwnerID, binding.CanvasID); err != nil {
						return err
					}
					if _, _, err := settleMediaVersion(tx, commands.Document{}, *binding, *task, *version, outputs); err != nil {
						return err
					}
					return tx.FinishCanvasMaterialization(taskID, outcome, binding.ResolutionVersion)
				})
			}
		} else {
			return err
		}
	}
	if err == nil {
		// Completed atomically with the command. A recovery may have requeued
		// this task after commit; never finish that newer attempt here.
		return nil
	}
	return s.repo.FinishCanvasMaterialization(taskID, outcome, binding.ResolutionVersion)
}
func (s *Service) DrainMaterializations(ctx context.Context) error {
	jobs, err := s.repo.PendingCanvasMaterializations(50)
	if err != nil {
		return err
	}
	var first error
	for _, j := range jobs {
		if err := s.Materialize(ctx, j.TaskID); err != nil {
			if ctx.Err() == nil {
				_ = s.repo.RecordCanvasMaterializationFailure(j.TaskID)
			}
			if first == nil {
				first = err
			}
		}
	}
	return first
}
func terminal(status model.TaskStatus) bool {
	return status == model.TaskStatusSucceeded || status == model.TaskStatusFailed || status == model.TaskStatusCancelled || status == model.TaskStatusSubmissionUncertain
}
func resultStatus(status model.TaskStatus) string {
	if status == model.TaskStatusSucceeded {
		return "success"
	}
	return "error"
}
func safeError(task model.Task) string {
	if task.Error == "submission_uncertainty_acknowledged" {
		return "主机已核对上游并结束本地等待；上游任务状态以服务商记录为准"
	}
	if task.Status == model.TaskStatusSubmissionUncertain {
		return "上游接单状态尚未确认，请核对原任务；已停止自动重提"
	}
	if task.Status == model.TaskStatusCancelled {
		return "任务已取消"
	}
	return "生成任务失败，请查看任务中心"
}

func (s *Service) materializeOutputs(ctx context.Context, task model.Task) ([]model.CanvasTaskOutput, error) {
	var out []model.CanvasTaskOutput
	err := s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		var err error
		out, err = tx.CanvasTaskOutputs(task.ID)
		if err != nil {
			return err
		}
		if len(out) > 0 || task.Status != model.TaskStatusSucceeded {
			return nil
		}
		binding, err := tx.CanvasTaskBinding(task.ID)
		if err != nil {
			return err
		}
		values, err := resultValues(task.ResultJSON, binding.Mode)
		if err != nil {
			return &commands.Error{Reason: "result_resource_unavailable", Message: "生成结果尚未保存为可用资源，请恢复原任务"}
		}
		for index, value := range values {
			outputMode := binding.Mode
			if binding.Mode == "previs" {
				outputMode = str(value["kind"])
			}
			data := map[string]any{}
			id := ""
			if outputMode == "text" {
				data["content"] = str(value["text"])
				if str(data["content"]) == "" {
					return fmt.Errorf("text output missing")
				}
			} else {
				for _, key := range []string{"resourceId", "storageKey", "dataUrl", "url"} {
					id = assets.ResourceID(str(value[key]))
					if key == "resourceId" && str(value[key]) != "" {
						id = str(value[key])
					}
					if id != "" {
						break
					}
				}
				if id == "" {
					return &commands.Error{Reason: "result_resource_unavailable", Message: "生成结果尚未保存为可用资源，请恢复原任务"}
				}
				r, err := tx.ResourceForUser(task.UserID, id)
				if err != nil && !tx.IsMissing(err) {
					return err
				}
				if err != nil || r.Status != model.ResourceStatusReady || !strings.HasPrefix(r.MimeType, outputMode+"/") {
					return &commands.Error{Reason: "result_resource_unavailable", Message: "生成资源未就绪或类型不匹配，请恢复原任务"}
				}
				data = map[string]any{"storageKey": "resource:" + id, "dataUrl": assets.FileURL(id), "url": assets.FileURL(id), "width": r.Width, "height": r.Height, "durationMs": r.DurationMs, "bytes": r.Size, "mimeType": r.MimeType, "hasAudio": value["hasAudio"]}
				if preview, ok := value["preview"].(map[string]any); ok && binding.Mode == "video" {
					previewID := str(preview["resourceId"])
					if previewID == "" {
						previewID = assets.ResourceID(str(preview["storageKey"]))
					}
					pr, e := tx.ResourceForUser(task.UserID, previewID)
					if e != nil || pr.Status != model.ResourceStatusReady || !strings.HasPrefix(pr.MimeType, "image/") {
						return fmt.Errorf("video preview not ready or wrong kind")
					}
					data["videoPreview"] = map[string]any{"content": assets.FileURL(pr.ID), "storageKey": "resource:" + pr.ID, "width": pr.Width, "height": pr.Height, "bytes": pr.Size, "mimeType": pr.MimeType}
				}
			}
			assetID := "task-output-" + task.ID + "-" + strconv.Itoa(index)
			payload := map[string]any{"id": assetID, "kind": outputMode, "title": truncate(task.Prompt, 240), "data": data, "coverUrl": data["url"], "sourceTaskId": task.ID}
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if s.options.ValidateOutputs != nil {
				if err = s.options.ValidateOutputs(ctx, tx, task.UserID, 1, int64(len(raw))); err != nil {
					return err
				}
			}
			now := time.Now()
			asset := model.Asset{ID: assetID, UserID: task.UserID, Kind: outputMode, Status: "ready", Title: truncate(task.Prompt, 240), PayloadJSON: string(raw), CreatedAt: now, UpdatedAt: now}
			if err = tx.InsertCanvasOutputAsset(&asset); err != nil {
				return err
			}
			o := model.CanvasTaskOutput{TaskID: task.ID, OutputIndex: index, OwnerID: task.UserID, AssetID: assetID, ResourceID: id, Kind: outputMode, PayloadJSON: string(raw), CreatedAt: now}
			if err = tx.InsertCanvasTaskOutput(&o); err != nil {
				return err
			}
			out = append(out, o)
		}
		return nil
	})
	return out, err
}
func resultValues(raw, mode string) ([]map[string]any, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	if mode == "previs" {
		if str(v["resourceId"]) == "" || str(v["previewResourceId"]) == "" {
			return nil, fmt.Errorf("previs artifacts missing")
		}
		return []map[string]any{{"resourceId": v["resourceId"], "kind": "video"}, {"resourceId": v["previewResourceId"], "kind": "image"}}, nil
	}
	if mode == "text" {
		if _, ok := v["text"].(string); !ok && v["rows"] != nil {
			v = map[string]any{"text": raw}
		}
		return []map[string]any{v}, nil
	}
	if mode == "audio" {
		if audios := objectList(v["audios"]); len(audios) > 0 && len(audios) <= 100 {
			return audios, nil
		}
	}
	if mode == "image" {
		images := objectList(v["images"])
		if len(images) > 0 && len(images) <= 100 {
			return images, nil
		}
	} else if value, ok := v[mode].(map[string]any); ok {
		return []map[string]any{value}, nil
	}
	return nil, fmt.Errorf("missing %s result", mode)
}
func outputData(o model.CanvasTaskOutput) map[string]any {
	var payload map[string]any
	_ = json.Unmarshal([]byte(o.PayloadJSON), &payload)
	data, _ := payload["data"].(map[string]any)
	return data
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func applyOutput(node map[string]any, task model.Task, o model.CanvasTaskOutput) error {
	if str(node["type"]) != o.Kind {
		return fmt.Errorf("output node kind changed")
	}
	m := metadata(node)
	data := outputData(o)
	m["content"], m["assetId"], m["status"], m["taskStatus"], m["taskProgress"], m["taskStage"] = data["content"], o.AssetID, "success", string(task.Status), 100, task.Stage
	m["taskId"], m["prompt"] = task.ID, task.Prompt
	for _, key := range []string{"richText", "errorDetails", "generationErrorCode", "failedPromptFingerprint", "resourceReloadAvailable", "runninghubWorkflowConfig", "workflowProvider", "runningHubWorkflowId", "runningHubWorkflowKind", "workflowParameters", "videoPreview"} {
		delete(m, key)
	}
	if o.Kind != "text" {
		produced := str(m["producedModelCandidate"])
		if produced == "" {
			produced = task.Model
		}
		delete(m, "producedModelCandidate")
		delete(m, "producedModel")
		if produced != "" {
			m["producedModel"] = produced
		}
		m["content"], m["storageKey"] = data["url"], data["storageKey"]
		if data["videoPreview"] != nil {
			m["videoPreview"] = data["videoPreview"]
		}
		m["naturalWidth"], m["naturalHeight"], m["bytes"], m["mimeType"], m["durationMs"], m["hasAudio"] = data["width"], data["height"], data["bytes"], data["mimeType"], data["durationMs"], data["hasAudio"]
		if m["locked"] != true && o.Kind != "audio" {
			fitMedia(node, data, o.Kind)
		}
	}
	if task.CompletedAt != nil {
		m["taskCompletedAt"] = task.CompletedAt.Format(time.RFC3339Nano)
	}
	m["taskMediaStage"], m["taskCreatedAt"], m["taskUpdatedAt"] = task.MediaStage, task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano)
	if task.StartedAt != nil {
		m["taskStartedAt"] = task.StartedAt.Format(time.RFC3339Nano)
		if task.CompletedAt != nil {
			m["taskDurationMs"] = task.CompletedAt.Sub(*task.StartedAt).Milliseconds()
		}
	}
	return nil
}
func numeric(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
func fitMedia(node, data map[string]any, kind string) {
	w, h := numeric(data["width"]), numeric(data["height"])
	if w <= 0 || h <= 0 {
		return
	}
	oldW, oldH := numeric(node["width"]), numeric(node["height"])
	maxW, maxH := oldW, oldH
	if kind == "video" {
		maxW, maxH = 720, 520
	}
	if kind == "image" {
		m := metadata(node)
		if m["generationType"] == "edit" && (str(m["size"]) == "" || str(m["size"]) == "auto") {
			return
		}
	}
	if maxW <= 0 || maxH <= 0 {
		return
	}
	scale := maxW / w
	if maxH/h < scale {
		scale = maxH / h
	}
	nw, nh := w*scale, h*scale
	p, _ := node["position"].(map[string]any)
	if p != nil {
		p["x"], p["y"] = numeric(p["x"])+(oldW-nw)/2, numeric(p["y"])+(oldH-nh)/2
	}
	node["width"], node["height"] = nw, nh
}
func selectVersion(doc commands.Document, node map[string]any) {
	root := str(metadata(node)["versionOfNodeId"])
	if root == "" {
		return
	}
	for _, n := range objectList(doc["nodes"]) {
		m := metadata(n)
		id := str(m["versionOfNodeId"])
		if id == "" {
			id = str(n["id"])
		}
		if id == root {
			m["versionPrimary"] = str(n["id"]) == str(node["id"])
		}
	}
}
