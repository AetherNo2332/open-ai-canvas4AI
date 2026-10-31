package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"yingce/backend/internal/canvas/tasks"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/platform"
	"yingce/backend/internal/repository"
)

type canvasTaskSubmitter struct {
	s         *Service
	admission *taskAdmission
}

func (s *Service) CanvasTaskSubmitter() tasks.TaskSubmitter { return &canvasTaskSubmitter{s: s} }
func (d *canvasTaskSubmitter) Prepare(ctx context.Context, p identity.Principal, req tasks.TaskRequest) (tasks.PreparedTask, error) {
	if !d.s.canvasCommandMode || !p.HasScope("generation.submit") || p.Role == "viewer" {
		return tasks.PreparedTask{}, Forbidden("没有生成权限")
	}
	if req.Mode == "previs" {
		// 服务端白模渲染器已下线；预演由浏览器导演台视口录制。
		return tasks.PreparedTask{}, BadAuthRequest("服务端预演渲染已下线，请在导演台中预览")
	}
	input, err := normalizeTaskInput(req.Input)
	if err != nil {
		return tasks.PreparedTask{}, err
	}
	if unsafeCanvasTaskInput(input) || isTextReplayTaskRequest(input) {
		return tasks.PreparedTask{}, BadAuthRequest("任务只能使用主机已配置的执行渠道")
	}
	config, _ := input["config"].(map[string]any)
	if req.LogicalModelID == "" && (config == nil || strValue(config["channelId"]) == "" || strValue(config["model"]) == "") {
		return tasks.PreparedTask{}, InvalidModelSelection("请先选择主机配置的模型")
	}
	input["mode"], input["prompt"] = req.Mode, req.Prompt
	typ := "canvas_" + req.Mode
	if req.Type != "" && req.Type != typ {
		return tasks.PreparedTask{}, BadAuthRequest("任务类型与生成模式不符")
	}
	if strings.HasPrefix(req.Operation, "cloud_agent") {
		return tasks.PreparedTask{}, BadAuthRequest("不支持内置 Agent 任务")
	}
	preparation := &creationTaskPreparation{}
	task, err := d.s.CreateTask(p.OwnerID, CreateTaskRequest{Type: typ, Operation: req.Operation, Prompt: req.Prompt, LogicalModelID: req.LogicalModelID, Input: input, admission: d.admission, creationPrepare: preparation})
	if err != nil {
		return tasks.PreparedTask{}, err
	}
	// Existing creation preparation returns private normalized input before
	// secret protection. Protect the host-resolved credentials BEFORE persistence.
	var normalized map[string]any
	if err = json.Unmarshal([]byte(task.InputJSON), &normalized); err != nil {
		return tasks.PreparedTask{}, err
	}
	selection := tasks.PreparedTask{}
	if task.LogicalModelID == "" {
		cfg, _ := normalized["config"].(map[string]any)
		channel, err := d.s.repo.SystemChannel(strValue(cfg["channelId"]))
		if err != nil || !channel.Enabled || channel.Scope != model.ChannelScopeSystem {
			return tasks.PreparedTask{}, InvalidModelSelection("所选渠道已停用")
		}
		item, err := d.s.repo.ChannelModelByKey(channel.ID, strValue(cfg["model"]))
		if err != nil || !item.Enabled {
			return tasks.PreparedTask{}, InvalidModelSelection("所选模型已停用")
		}
		task.ChannelModelID, task.Model, task.Provider = item.ID, item.ModelKey, "managed"
		selection.ChannelID, selection.ChannelUpdatedAt, selection.ModelUpdatedAt = channel.ID, channel.UpdatedAt, item.UpdatedAt
	}
	if err = d.s.protectTaskSecrets(normalized); err != nil {
		return tasks.PreparedTask{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return tasks.PreparedTask{}, err
	}
	task.InputJSON = string(raw)
	if preparation.Order != nil {
		task.BillingOrderID = preparation.Order.ID
	}
	policy, err := d.s.RuntimePolicy()
	if err != nil {
		return tasks.PreparedTask{}, err
	}
	selection.Task, selection.BillingOrder, selection.ActiveTaskLimit = *task, preparation.Order, policy.Task.ActiveTaskLimit
	return selection, nil
}
func (d *canvasTaskSubmitter) Commit(ctx context.Context, tx *repository.Repository, prepared tasks.PreparedTask) error {
	// SQLite serializes writes. Read quotas in this transaction rather than
	// taking storageMu after the database write lock (inverse order can deadlock
	// with provider completion). No wake or nested write transaction here.
	if prepared.ChannelID != "" {
		channel, err := tx.SystemChannel(prepared.ChannelID)
		if err != nil || !channel.Enabled || channel.Scope != model.ChannelScopeSystem || !channel.UpdatedAt.Equal(prepared.ChannelUpdatedAt) {
			return InvalidModelSelection("渠道配置已变化，请重新选择")
		}
		item, err := tx.ChannelModelByKey(prepared.ChannelID, prepared.Task.Model)
		if err != nil || !item.Enabled || item.ID != prepared.Task.ChannelModelID || !item.UpdatedAt.Equal(prepared.ModelUpdatedAt) {
			return InvalidModelSelection("模型配置已变化，请重新选择")
		}
	}
	domain := platform.New(tx, d.s.coordinator, platformHost{svc: d.s})
	policy, err := domain.RuntimePolicy()
	if err != nil {
		return err
	}
	usage, err := tx.UserStorageUsage(prepared.Task.UserID)
	if err != nil {
		return err
	}
	bytes := int64(len(prepared.Task.Prompt) + len(prepared.Task.InputJSON))
	if err = validateTaskStorageQuotaWithPolicy(usage, bytes, policy.Resource); err != nil {
		return err
	}
	if policy.Task.ActiveTaskLimit < 1 {
		return fmt.Errorf("invalid task admission limit")
	}
	return tx.CommitPreparedTask(&prepared.Task, prepared.BillingOrder, policy.Task.ActiveTaskLimit)
}
func (d *canvasTaskSubmitter) Wake() { d.s.wakeTaskDispatcher() }
func strValue(v any) string          { s, _ := v.(string); return strings.TrimSpace(s) }
func unsafeCanvasTaskInput(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for key, child := range x {
			switch strings.ToLower(key) {
			case "apikey", "secretkey", "authorization", "password", "headers", "baseurl", "agentrequests", "cloudagent", "agentrunid", "generationid", "approvalid":
				return true
			}
			if unsafeCanvasTaskInput(child) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if unsafeCanvasTaskInput(child) {
				return true
			}
		}
	}
	return false
}

func (s *Service) CanvasTaskOptions() tasks.ServiceOptions {
	return tasks.ServiceOptions{PrepareStoryboardInput: prepareCanvasStoryboardInput, PrepareOutput: s.prepareCanvasTaskOutput, ValidateOutputs: func(ctx context.Context, tx *repository.Repository, owner string, count int, bytes int64) error {
		domain := platform.New(tx, s.coordinator, platformHost{svc: s})
		policy, err := domain.RuntimePolicy()
		if err != nil {
			return err
		}
		usage, err := tx.UserStorageUsage(owner)
		if err != nil {
			return err
		}
		if usage.AssetCount+int64(count) > policy.Resource.AssetCount {
			return QuotaExceeded("本地素材数量已达到上限，请清理或调整存储限制")
		}
		return validateStructuredStorageQuotaWithPolicy(usage, "asset", true, bytes, policy.Resource)
	}}
}
func (s *Service) startCanvasMaterializer() {
	if s.canvasTasks == nil {
		return
	}
	s.runWorkerLoop(func(ctx context.Context) {
		_ = s.repo.RecoverCanvasMaterializationJobs()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			_ = s.canvasTasks.DrainMaterializations(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}
