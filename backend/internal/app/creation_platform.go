package app

import (
	"context"
	"errors"
	"strings"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

// creationPlatform 是 cp.Platform 在组合根里的唯一实现：HTTP 适配层与 Go 业务编排都调用它，
// 它再进入既有的 CreateTask 准入、计价与 worker 执行，不建立第二条执行路径。
type creationPlatform struct {
	s *Service
}

// CreationPlatform 返回平台内部创作能力入口。
func (s *Service) CreationPlatform() cp.Platform { return &creationPlatform{s: s} }

var _ cp.Platform = (*creationPlatform)(nil)

// platformApplication 把合同 application 收敛为数据库记录值；首版只接受登记过的业务。
func platformApplication(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "canvas", "drama", "ecommerce", "test":
		return strings.TrimSpace(value), nil
	}
	return "", cp.NewError(cp.CodeInvalidRequest, "未登记的业务 application")
}

func requireCaller(caller cp.Caller) error {
	if strings.TrimSpace(caller.UserID) == "" {
		return cp.NewError(cp.CodeUnauthorized, "请先登录")
	}
	return nil
}

// platformTaskRequest 把合同请求翻译为既有任务输入。只写入能力参数、稳定资源引用与业务元数据；
// 协议、价格档、上游模型标识和凭据由 CreateTask 从服务端目录重建。
func (p *creationPlatform) platformTaskRequest(userID string, req cp.GenerationRequest) (CreateTaskRequest, error) {
	capability := string(req.Capability)
	config := map[string]any{}
	if req.Model.LogicalModelID == "" {
		config["channelId"], config["model"] = strings.TrimSpace(req.Model.ChannelID), strings.TrimSpace(req.Model.ModelKey)
	}
	options := map[string]any{}
	textOptions := map[string]any{"stream": false}
	for key, value := range req.Parameters {
		switch {
		case capability == "text" && key == "maxTokens":
			textOptions["maxOutputTokens"] = value
		case capability == "text" && key == "systemPrompt":
			config["systemPrompt"] = providerConfigOptionValue(value)
		case isCapabilityOptionFor(capability, canonicalModelOptionName(capability, key)):
			name := canonicalModelOptionName(capability, key)
			config[name] = providerConfigOptionValue(value)
			options[name] = value
		default:
			return CreateTaskRequest{}, cp.NewError(cp.CodeInvalidRequest, "不支持的生成参数："+key)
		}
	}
	input := map[string]any{
		"mode": capability, "prompt": strings.TrimSpace(req.Prompt), "config": config,
		"metadata": map[string]any{"source": "creation_platform", "application": req.Business.Application},
	}
	if req.Model.LogicalModelID != "" && len(options) > 0 {
		input["capabilityOptions"] = options
	}
	if capability == "text" {
		input["textOptions"] = textOptions
	}
	groups := map[string][]any{}
	for _, ref := range req.References {
		resource, err := p.s.repo.ResourceForUser(userID, strings.TrimSpace(ref.ResourceID))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CreateTaskRequest{}, cp.NewError(cp.CodeReferenceUnavailable, "参考素材不存在或不属于当前账号")
		}
		if err != nil {
			return CreateTaskRequest{}, err
		}
		if resource.Status != model.ResourceStatusReady || resource.Kind != ref.Kind {
			return CreateTaskRequest{}, cp.NewError(cp.CodeReferenceUnavailable, "参考素材尚未就绪或类型不符")
		}
		media := map[string]any{"id": resource.ID, "type": resource.Kind, "storageKey": "resource:" + resource.ID, "mimeType": resource.MimeType, "bytes": resource.Size, "width": resource.Width, "height": resource.Height, "durationMs": resource.DurationMs}
		if ref.Role != "" {
			media["role"] = ref.Role
		}
		if ref.Role == "mask" {
			input["mask"] = media
			continue
		}
		key := map[string]string{"image": "referenceImages", "video": "referenceVideos", "audio": "referenceAudios"}[ref.Kind]
		groups[key] = append(groups[key], media)
	}
	for key, list := range groups {
		input[key] = list
	}
	operation := strings.TrimSpace(req.Operation)
	if operation == "" {
		operation = capability
	}
	return CreateTaskRequest{
		Type: "canvas_" + capability, Operation: operation, Prompt: strings.TrimSpace(req.Prompt),
		Model: strings.TrimSpace(req.Model.ModelKey), LogicalModelID: strings.TrimSpace(req.Model.LogicalModelID),
		Input: input, TraceID: req.TraceID,
	}, nil
}

func (p *creationPlatform) Submit(_ context.Context, caller cp.Caller, req cp.GenerationRequest) (cp.SubmitReceipt, error) {
	if err := requireCaller(caller); err != nil {
		return cp.SubmitReceipt{}, err
	}
	if err := req.Validate(); err != nil {
		return cp.SubmitReceipt{}, err
	}
	application, err := platformApplication(req.Business.Application)
	if err != nil {
		return cp.SubmitReceipt{}, err
	}
	if replay, err := p.existingPlatformSubmission(caller.UserID, application, req); replay != nil || err != nil {
		return derefReceipt(replay), err
	}
	task, order, err := p.preparePlatformTask(caller.UserID, req)
	if err != nil {
		return cp.SubmitReceipt{}, err
	}
	if err := p.applyCostAuthorization(caller.UserID, req, task, order); err != nil {
		return cp.SubmitReceipt{}, err
	}
	if err := p.commitPlatformTask(task, order, platformSubmissionRecord(caller.UserID, application, req)); err != nil {
		// 并发同键：输家的事务整体回滚（任务、冻结、提交记录都不留下），再按赢家的记录回放或判冲突。
		if replay, lookupErr := p.existingPlatformSubmission(caller.UserID, application, req); replay != nil || lookupErr != nil {
			return derefReceipt(replay), lookupErr
		}
		return cp.SubmitReceipt{}, platformError(err)
	}
	snapshot, err := p.snapshot(caller.UserID, task.ID)
	if err != nil {
		return cp.SubmitReceipt{}, err
	}
	return cp.SubmitReceipt{Snapshot: snapshot}, nil
}

func (p *creationPlatform) Task(_ context.Context, caller cp.Caller, taskID string) (cp.TaskSnapshot, error) {
	if err := requireCaller(caller); err != nil {
		return cp.TaskSnapshot{}, err
	}
	return p.snapshot(caller.UserID, strings.TrimSpace(taskID))
}

// snapshot 只读取经平台提交、且属于调用者的任务；其他任务一律按不存在处理。
func (p *creationPlatform) snapshot(userID, taskID string) (cp.TaskSnapshot, error) {
	submission, err := p.s.repo.PlatformSubmissionForTask(userID, taskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cp.TaskSnapshot{}, cp.NewError(cp.CodeNotFound, "任务不存在")
	}
	if err != nil {
		return cp.TaskSnapshot{}, err
	}
	task, err := p.s.repo.TaskForUser(userID, taskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cp.TaskSnapshot{}, cp.NewError(cp.CodeNotFound, "任务不存在")
	}
	if err != nil {
		return cp.TaskSnapshot{}, err
	}
	var order *model.BillingOrder
	if task.BillingOrderID != "" {
		if order, err = p.s.repo.BillingOrder(task.BillingOrderID); err != nil {
			return cp.TaskSnapshot{}, err
		}
	}
	return platformSnapshot(*submission, *task, order), nil
}

func (p *creationPlatform) Models(context.Context, cp.Caller, cp.Capability) (cp.ModelCatalog, error) {
	return cp.ModelCatalog{}, cp.NewError(cp.CodeUnavailable, "模型目录将在 S4 接入")
}

func (p *creationPlatform) Quote(_ context.Context, caller cp.Caller, req cp.GenerationRequest) (cp.Quote, error) {
	if err := requireCaller(caller); err != nil {
		return cp.Quote{}, err
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = "quote"
	}
	if err := req.Validate(); err != nil {
		return cp.Quote{}, err
	}
	if _, err := platformApplication(req.Business.Application); err != nil {
		return cp.Quote{}, err
	}
	task, order, err := p.preparePlatformTask(caller.UserID, req)
	if err != nil {
		return cp.Quote{}, err
	}
	return p.quoteFor(caller.UserID, req, task, order)
}

func (p *creationPlatform) Events(context.Context, cp.Caller, cp.EventQuery) (cp.EventPage, error) {
	return cp.EventPage{}, cp.NewError(cp.CodeUnavailable, "事件将在 S3 接入")
}

func (p *creationPlatform) Cancel(context.Context, cp.Caller, string) (cp.TaskSnapshot, error) {
	return cp.TaskSnapshot{}, cp.NewError(cp.CodeUnavailable, "取消将在 S3 接入")
}

func (p *creationPlatform) Retry(context.Context, cp.Caller, string) (cp.TaskSnapshot, error) {
	return cp.TaskSnapshot{}, cp.NewError(cp.CodeUnavailable, "重试将在 S3 接入")
}

func (p *creationPlatform) RecoverMedia(context.Context, cp.Caller, string) (cp.TaskSnapshot, error) {
	return cp.TaskSnapshot{}, cp.NewError(cp.CodeUnavailable, "媒体恢复将在 S3 接入")
}

func (p *creationPlatform) Balance(_ context.Context, caller cp.Caller) (cp.CreditBalance, error) {
	if err := requireCaller(caller); err != nil {
		return cp.CreditBalance{}, err
	}
	account, err := p.s.repo.CreditAccount(caller.UserID)
	if err != nil {
		return cp.CreditBalance{}, err
	}
	return cp.CreditBalance{AvailableMicrocredits: account.AvailableMicrocredits, ReservedMicrocredits: account.ReservedMicrocredits}, nil
}

func (p *creationPlatform) BillingAggregate(_ context.Context, caller cp.Caller, business cp.BusinessRef) (cp.BillingAggregate, error) {
	if err := requireCaller(caller); err != nil {
		return cp.BillingAggregate{}, err
	}
	application, err := platformApplication(business.Application)
	if err != nil {
		return cp.BillingAggregate{}, err
	}
	projectRef := strings.TrimSpace(business.ProjectID)
	totals, err := p.s.repo.PlatformBillingTotals(caller.UserID, application, projectRef)
	if err != nil {
		return cp.BillingAggregate{}, err
	}
	return cp.BillingAggregate{
		Application: application, ProjectID: projectRef, TaskCount: totals.TaskCount,
		ReservedMicrocredits: totals.ReservedMicrocredits, ChargedMicrocredits: totals.ChargedMicrocredits,
		RefundedMicrocredits: totals.RefundedMicrocredits, PendingReviewCount: totals.PendingReviewCount,
	}, nil
}

// CreationPlatformError 供 HTTP 适配层把任意错误收敛为合同错误；无法归类时原样返回。
func CreationPlatformError(err error) error { return platformError(err) }

// platformError 把既有准入错误映射为稳定合同错误；已是合同错误的原样返回。
func platformError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := cp.AsError(err); ok {
		return err
	}
	var modelErr *ModelError
	if errors.As(err, &modelErr) && modelErr != nil {
		switch modelErr.ErrorCode {
		case ErrCodeModelCapabilityNotSupported, ErrCodeModelPriceNotConfigured:
			return cp.NewError(cp.CodeModelNotSupported, modelErr.Error())
		default:
			return cp.NewError(cp.CodeModelUnavailable, modelErr.Error())
		}
	}
	if mapped := platformRepositoryError(err); mapped != nil {
		return mapped
	}
	var appErr *AppError
	if errors.As(err, &appErr) && appErr != nil {
		if mapped := platformAppError(appErr); mapped != nil {
			return mapped
		}
	}
	return err
}

func platformRepositoryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrInsufficientCredits):
		return cp.NewError(cp.CodeInsufficientCredits, "积分不足")
	case errors.Is(err, repository.ErrActiveTaskLimit):
		return cp.NewError(cp.CodeActiveTaskLimit, "同时进行的任务已达上限")
	case errors.Is(err, repository.ErrLogicalModelUnavailable):
		return cp.NewError(cp.CodeModelUnavailable, "所选模型已停用或配置已更新")
	case errors.Is(err, repository.ErrBillingChargeLimit):
		return cp.NewError(cp.CodeCostLimitExceeded, "报价超过授权的费用上限")
	}
	return nil
}

func platformAppError(appErr *AppError) error {
	switch {
	case appErr.Status == 401:
		return cp.NewError(cp.CodeUnauthorized, appErr.Message)
	case appErr.Status == 403 && appErr.Reason == ReasonQuotaExceeded:
		return cp.NewError(cp.CodeInsufficientCredits, appErr.Message)
	case appErr.Status == 403:
		return cp.NewError(cp.CodeForbidden, appErr.Message)
	case appErr.Status == 404:
		return cp.NewError(cp.CodeNotFound, appErr.Message)
	case appErr.Status == 503:
		return cp.NewError(cp.CodeUnavailable, appErr.Message)
	case appErr.Status == 429:
		return cp.NewError(cp.CodeActiveTaskLimit, appErr.Message)
	case appErr.Status >= 400 && appErr.Status < 500:
		return cp.NewError(cp.CodeInvalidRequest, appErr.Message)
	}
	return nil
}
