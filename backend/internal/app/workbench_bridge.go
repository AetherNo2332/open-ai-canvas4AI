package app

import (
	"fmt"
	"strings"

	"yingce/backend/internal/assets"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/workbench"
)

const (
	WorkbenchKindImage = workbench.KindImage
	WorkbenchKindVideo = workbench.KindVideo
)

type (
	WorkbenchToolInput   = workbench.ToolInput
	WorkbenchPresetInput = workbench.PresetInput
	WorkbenchToolView    = workbench.ToolView
	WorkbenchPresetView  = workbench.PresetView
	WorkbenchTaskInput   = workbench.TaskInput
)

type workbenchAdminGate struct{ svc *Service }

func (g workbenchAdminGate) RequireAdmin(user *model.User) error {
	if g.svc == nil {
		return Unauthorized("请先登录")
	}
	return g.svc.RequireAdmin(user)
}
func (g workbenchAdminGate) AppendAudit(actor *model.User, action, targetType, targetID, summary string, metadata any) error {
	if g.svc == nil {
		return Unauthorized("请先登录")
	}
	return g.svc.appendAdminAudit(actor, action, targetType, targetID, summary, metadata)
}

func (g workbenchAdminGate) WorkbenchResourceAccess(resourceID string) (string, error) {
	if g.svc == nil {
		return "", Unauthorized("请先登录")
	}
	resource, err := g.svc.repo.Resource(strings.TrimSpace(resourceID))
	if err != nil {
		return "", NotFound("工具展示资源不存在")
	}
	if resource.Kind != "image" && resource.Kind != "video" && resource.Kind != "audio" || resource.Status != model.ResourceStatusReady {
		return "", BadAuthRequest("工具展示资源不可用")
	}
	access, err := g.svc.resolveResourceAccess(resource, ResourceAccessOptions{Purpose: assets.PurposeDisplay})
	if err != nil {
		return "", err
	}
	return access.URL, nil
}

func (s *Service) workbenchDomain() *workbench.Service {
	if s == nil {
		return workbench.New(nil, nil, nil)
	}
	if s.workbench != nil {
		return s.workbench
	}
	return workbench.New(s.repo, workbenchAdminGate{svc: s}, workbenchAdminGate{svc: s})
}

func (s *Service) EnsureBuiltinWorkbenchCatalog() error {
	return s.workbenchDomain().EnsureBuiltinCatalog()
}

func (s *Service) PublicWorkbenchTools(kind string) ([]WorkbenchToolView, error) {
	if kind == workbench.KindImage {
		if err := s.RequireFeature(FeatureImageWorkbench); err != nil {
			return nil, err
		}
	} else if kind == workbench.KindVideo {
		if err := s.RequireFeature(FeatureVideoWorkbench); err != nil {
			return nil, err
		}
	} else {
		return nil, BadAuthRequest("工作台类型无效")
	}
	return s.workbenchDomain().PublicTools(kind)
}

func (s *Service) AdminWorkbenchTools(actor *model.User, kind string) ([]WorkbenchToolView, error) {
	return s.workbenchDomain().AdminTools(actor, kind)
}
func (s *Service) CreateWorkbenchTool(actor *model.User, req WorkbenchToolInput) (*WorkbenchToolView, error) {
	return s.workbenchDomain().CreateTool(actor, req)
}
func (s *Service) UpdateWorkbenchTool(actor *model.User, id string, req WorkbenchToolInput) (*WorkbenchToolView, error) {
	return s.workbenchDomain().UpdateTool(actor, id, req)
}
func (s *Service) SetWorkbenchToolEnabled(actor *model.User, id string, enabled bool) error {
	return s.workbenchDomain().SetToolEnabled(actor, id, enabled)
}
func (s *Service) CreateWorkbenchPreset(actor *model.User, req WorkbenchPresetInput) (*WorkbenchPresetView, error) {
	return s.workbenchDomain().CreatePreset(actor, req)
}
func (s *Service) UpdateWorkbenchPreset(actor *model.User, id string, req WorkbenchPresetInput) (*WorkbenchPresetView, error) {
	return s.workbenchDomain().UpdatePreset(actor, id, req)
}
func (s *Service) SetWorkbenchPresetEnabled(actor *model.User, id string, enabled bool) error {
	return s.workbenchDomain().SetPresetEnabled(actor, id, enabled)
}

func (s *Service) CreateWorkbenchTask(userID string, req WorkbenchTaskInput, traceID, requestID string) (*model.Task, error) {
	return s.createWorkbenchTask(userID, req, traceID, requestID, "")
}

// createWorkbenchTaskUnderBudget is the Agent path: the same workbench rules,
// with the order admitted against the Agent's budget grant.
func (s *Service) createWorkbenchTaskUnderBudget(userID string, req WorkbenchTaskInput, grantID string) (*model.Task, error) {
	return s.createWorkbenchTask(userID, req, "", "", grantID)
}

func (s *Service) createWorkbenchTask(userID string, req WorkbenchTaskInput, traceID, requestID, grantID string) (*model.Task, error) {
	if req.Kind == WorkbenchKindImage {
		if err := s.RequireFeature(FeatureImageWorkbench); err != nil {
			return nil, err
		}
	} else if req.Kind == WorkbenchKindVideo {
		if err := s.RequireFeature(FeatureVideoWorkbench); err != nil {
			return nil, err
		}
	} else {
		return nil, BadAuthRequest("工作台类型无效")
	}
	resolved, err := s.workbenchDomain().ResolveTask(userID, req)
	if err != nil {
		return nil, err
	}
	providerConfig, err := s.resolveWorkbenchProviderConfig(userID, req.Kind, resolved.Operation, resolved.Model, resolved.LogicalModelID, resolved.ChannelID, resolved.Parameters, resolved.Input)
	if err != nil {
		return nil, err
	}
	resolved.Input["config"] = providerConfig
	modelName, ok := providerConfig["model"].(string)
	if !ok || strings.TrimSpace(modelName) == "" {
		return nil, BadAuthRequest("所选系统模型不可用")
	}
	resolved.Model = modelName
	taskReq := CreateTaskRequest{Type: resolved.TaskType, Operation: resolved.Operation, Prompt: resolved.Prompt, Model: resolved.Model, LogicalModelID: resolved.LogicalModelID, Input: resolved.Input, TraceID: traceID, RequestID: requestID, workbenchKind: req.Kind}
	submission := adapterSubmission{UserID: userID, TaskReq: taskReq, Business: cp.BusinessRef{Application: "ecommerce", ObjectType: "workbench_" + req.Kind, ObjectID: req.ToolID}, BudgetGrantID: grantID}
	// 批次项按“批次 + 序号”幂等：整批重提时已接受的项回放原任务，不重新建任务或冻结费用。
	if req.Batch != nil && workbench.ValidTaskBatch(req.Batch.ID, req.Batch.Index, req.Batch.Count) {
		operation := fmt.Sprintf("%s:%d", req.Batch.ID, req.Batch.Index)
		submission.Key, submission.Business.ProjectID, submission.Business.OperationID = "workbench:"+operation, req.Batch.ID, operation
	}
	task, _, err := (&creationPlatform{s: s}).submitResolved(submission)
	return task, err
}

func (s *Service) WorkbenchTasks(userID, kind string, activeOnly bool, limit int) ([]TaskSummary, error) {
	if kind == WorkbenchKindImage {
		if err := s.RequireFeature(FeatureImageWorkbench); err != nil {
			return nil, err
		}
	} else if kind == WorkbenchKindVideo {
		if err := s.RequireFeature(FeatureVideoWorkbench); err != nil {
			return nil, err
		}
	} else {
		return nil, BadAuthRequest("工作台类型无效")
	}
	return s.TasksWithOptions(userID, TaskListOptions{Limit: limit, ActiveOnly: activeOnly, WorkbenchKind: kind})
}

func (s *Service) resolveWorkbenchProviderConfig(userID, kind, operation, modelName, logicalModelID, channelID string, parameters map[string]any, input map[string]any) (map[string]any, error) {
	config := map[string]any{"mode": kind}
	if kind == WorkbenchKindImage {
		for _, key := range []string{"size", "quality", "imageResolution", "count"} {
			if value, exists := parameters[key]; exists {
				config[key] = providerConfigOptionValue(value)
			}
		}
	} else if kind == WorkbenchKindVideo {
		for inputKey, configKey := range map[string]string{
			"seconds": "videoSeconds", "ratio": "size", "resolution": "vquality",
			"generateAudio": "videoGenerateAudio", "watermark": "videoWatermark",
		} {
			if value, exists := parameters[inputKey]; exists {
				config[configKey] = providerConfigOptionValue(value)
			}
		}
	} else {
		return nil, BadAuthRequest("工作台类型无效")
	}
	if logicalModelID != "" {
		config["model"] = modelName
		return config, nil
	}
	channelID = strings.TrimSpace(channelID)
	modelName = strings.TrimSpace(modelName)
	if channelID == "" || modelName == "" {
		return nil, BadAuthRequest("所选系统模型不可用")
	}
	config["channelId"] = channelID
	config["model"] = modelName
	input["config"] = config
	// Match CreateTask's JSON shape so reference counts affect admission and pricing.
	normalizedInput, err := normalizeTaskInput(input)
	if err != nil {
		return nil, err
	}
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, err
	}
	resolved, err := s.resolveSystemChannelModelSelection(access, normalizedInput, "canvas_"+kind, operation)
	if err != nil {
		return nil, err
	}
	providerConfig, _ := resolved["config"].(map[string]any)
	if len(providerConfig) == 0 {
		return nil, BadAuthRequest("所选系统模型不可用")
	}
	return providerConfig, nil
}
