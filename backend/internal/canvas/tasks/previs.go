package tasks

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"time"
)

func (s *Service) SubmitPrevis(ctx context.Context, p identity.Principal, req PrevisSubmitRequest) (TaskHandle, error) {
	if !p.HasScope("generation.submit") || p.Role == "viewer" {
		return TaskHandle{}, &commands.Error{Reason: "permission_denied", Message: "没有预演提交权限"}
	}
	if len(req.Command.Operations) > 0 {
		return TaskHandle{}, commands.BadAuthRequest("预演提交不能夹带画布操作")
	}
	id := ""
	receipt, fresh, err := s.commands.ExecuteHost(ctx, p, req.Command, "task_submit", req, false, func(tx *repository.Repository, doc commands.Document) (commands.Document, map[string]bool, error) {
		scene := commands.PrevisScene(doc, req.SceneID)
		if scene == nil {
			return nil, nil, commands.BadAuthRequest("预演场景不存在")
		}
		status := str(scene["renderTaskStatus"])
		if status == "queued" || status == "running" || status == "submission_uncertain" {
			return nil, nil, commands.BadAuthRequest("场景已有待完成预演")
		}
		input, err := commands.PreparePrevisRender(doc, req.Command.CanvasID, req.SceneID, req.ShotID, req.Command.OperationID, req.Options)
		if err != nil {
			return nil, nil, err
		}
		for _, resourceID := range input.Assets {
			r, e := tx.ResourceForUser(p.OwnerID, resourceID)
			if e != nil || r.Status != model.ResourceStatusReady || r.Size <= 0 || r.Size > 64<<20 {
				return nil, nil, commands.BadAuthRequest("预演输入资源不可用")
			}
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, nil, err
		}
		if len(raw) > 4<<20 {
			return nil, nil, commands.BadAuthRequest("预演输入超过4 MiB")
		}
		normalized := map[string]any{}
		if err = json.Unmarshal(raw, &normalized); err != nil {
			return nil, nil, err
		}
		prepared, err := s.submitter.Prepare(ctx, p, TaskRequest{Mode: "previs", Type: model.TaskTypePrevisRender, Prompt: "白模预演", Input: normalized})
		if err != nil {
			return nil, nil, err
		}
		if prepared.Task.ID == "" || prepared.Task.UserID != p.OwnerID || prepared.Task.Status != model.TaskStatusQueued {
			return nil, nil, commands.BadAuthRequest("预演任务准入失败")
		}
		prepared.Task.ProjectID = req.Command.CanvasID
		if err = s.submitter.Commit(ctx, tx, prepared); err != nil {
			return nil, nil, err
		}
		b := model.CanvasTaskBinding{TaskID: prepared.Task.ID, OwnerID: p.OwnerID, CanvasID: req.Command.CanvasID, Epoch: req.Command.DocumentEpoch, ActorID: p.ID, ClientID: req.Command.ClientID, OperationID: req.Command.OperationID, Mode: "previs", ConsumerJSON: `{"kind":"previs"}`, ResultContextJSON: string(raw), CreatedAt: time.Now()}
		if err = tx.InsertCanvasTaskBinding(&b); err != nil {
			return nil, nil, err
		}
		for _, rid := range input.Assets {
			if err = tx.SaveCanvasTaskResource(b.TaskID, b.OwnerID, rid); err != nil {
				return nil, nil, err
			}
		}
		scene["renderTaskId"], scene["renderTaskStatus"] = b.TaskID, "queued"
		id = b.TaskID
		return doc, map[string]bool{"project:previsScenes": true}, nil
	})
	if err != nil {
		return TaskHandle{}, err
	}
	if id == "" {
		b, e := s.repo.CanvasTaskBindingForOperation(p.OwnerID, req.Command.CanvasID, req.Command.DocumentEpoch, p.ID, req.Command.ClientID, req.Command.OperationID)
		if e != nil {
			return TaskHandle{}, e
		}
		id = b.TaskID
	}
	if fresh {
		s.submitter.Wake()
	}
	return TaskHandle{TaskID: id, Receipt: receipt}, nil
}
