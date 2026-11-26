package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W05 参考图出图 job：页面与 Agent 工具都只入队，提交、轮询与回写都在这里。
//
// 任务提交才是计费单位，所以 job 与任务分开：入队只表达意图，用户取消时删一行 job 即可，
// 不会产生「已扣费又没人要」的产物。job 与 extract 同构：TaskID 为空 → 提交并写回；
// 非空 → 读结果、落库、收尾。区别只在落哪里：extract 落资产身份行，这里落 image 行与指针。

// shortplayImageJobRun 是一轮 job 执行结果；Status 为空表示仍在途（下一 tick 继续读）。
type shortplayImageJobRun struct {
	Status    string
	LastError string
	Payload   string
	TaskID    string
}

// shortplayTaskHook 仅用于测试注入任务提交；生产路径始终走 CreateTask 准入 + 唤醒执行器。
func (s *Service) SetShortplayTaskHook(hook func(userID string, req CreateTaskRequest) (*model.Task, error)) {
	s.shortplayTaskMu.Lock()
	defer s.shortplayTaskMu.Unlock()
	s.shortplayTaskHook = hook
}

func (s *Service) submitShortplayTask(userID string, req CreateTaskRequest) (*model.Task, error) {
	s.shortplayTaskMu.Lock()
	hook := s.shortplayTaskHook
	s.shortplayTaskMu.Unlock()
	if hook != nil {
		return hook(userID, req)
	}
	task, err := s.CreateTask(userID, req)
	if err == nil {
		s.wakeTaskDispatcher()
	}
	return task, err
}

// QueueShortplayAssetImages 按筛选条件批量入队出图，返回三档计数。
// 单个资产也走这里（AssetIDs 一项）—— 单个与批量只有筛选条件不同。
func (s *Service) QueueShortplayAssetImages(userID, idOrProject string, req shortplay.QueueAssetImagesRequest) (shortplay.ImageQueueResult, error) {
	owner, err := s.shortplayRowForUser(userID, idOrProject)
	if err != nil {
		return shortplay.ImageQueueResult{}, err
	}
	assets, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return shortplay.ImageQueueResult{}, err
	}
	wanted := make(map[int]bool, len(req.AssetIDs))
	for _, id := range req.AssetIDs {
		wanted[id] = true
	}
	kind := strings.TrimSpace(req.Kind)
	result := shortplay.ImageQueueResult{JobIDs: []string{}}
	for _, asset := range assets {
		if len(wanted) > 0 && !wanted[asset.ID] {
			result.Skipped++
			continue
		}
		if kind != "" && asset.Kind != kind {
			result.Skipped++
			continue
		}
		if asset.Locked || (req.OnlyMissing && asset.ImageStatus != "") || (req.OnlyFailed && asset.ImageStatus != shortplay.ImageFailed) {
			result.Skipped++
			continue
		}
		job := &model.ShortplayJob{
			ID:          newID(),
			ShortplayID: owner.ID,
			Kind:        shortplay.JobAssetImage,
			Status:      shortplay.JobQueued,
			DedupeKey:   shortplay.JobDedupeKey(shortplay.JobAssetImage, owner.ID+"|"+strconv.Itoa(asset.ID)),
			PayloadJSON: shortplay.AssetImageJobPayload{AssetID: asset.ID}.Encode(),
		}
		existing, created, enqueueErr := s.repo.EnqueueShortplayJob(job)
		if enqueueErr != nil {
			return shortplay.ImageQueueResult{}, enqueueErr
		}
		if created {
			result.Queued++
			result.JobIDs = append(result.JobIDs, job.ID)
			continue
		}
		if existing != nil {
			result.Reused++
			result.JobIDs = append(result.JobIDs, existing.ID)
		}
	}
	return result, nil
}

// tickShortplayAssetImages 领取并推进至多若干条出图 job；单条失败只记录，循环继续。
func (s *Service) tickShortplayAssetImages(ctx context.Context) {
	if s.IsDraining() {
		return
	}
	for i := 0; i < shortplayExtractPerTick; i++ {
		job, err := s.repo.ClaimShortplayJob(shortplay.JobAssetImage)
		if err != nil {
			slog.Warn("shortplay asset image claim failed", "error", err)
			return
		}
		if job == nil {
			return
		}
		run := s.runShortplayAssetImageJob(ctx, job)
		if run.Status == "" {
			continue
		}
		if err := s.repo.FinishShortplayJob(job.ID, run.Status, run.LastError, run.Payload); err != nil {
			slog.Warn("shortplay asset image finish failed", "jobId", job.ID, "error", err)
		}
	}
}

// runShortplayAssetImageJob 执行一轮：未提交就提交，已提交就读结果回写。
func (s *Service) runShortplayAssetImageJob(ctx context.Context, job *model.ShortplayJob) shortplayImageJobRun {
	owner, project, asset, err := s.shortplayImageJobTargets(job)
	if err != nil {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	if strings.TrimSpace(job.TaskID) == "" {
		return s.submitShortplayAssetImage(project, owner, job, asset)
	}
	return s.applyShortplayAssetImage(project, owner, job, asset)
}

// shortplayImageJobTargets 读出作品、容器行与目标资产；任何缺失都让 job 明确失败。
func (s *Service) shortplayImageJobTargets(job *model.ShortplayJob) (*model.Shortplay, *model.Project, *model.ShortplayAsset, error) {
	payload := shortplay.ParseAssetImageJob(job.PayloadJSON)
	if payload.AssetID <= 0 {
		return nil, nil, nil, errors.New("出图任务缺少资产序号")
	}
	owner, err := s.repo.ShortplayByID(job.ShortplayID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, errors.New("短剧不存在或已删除")
	}
	if err != nil {
		return nil, nil, nil, err
	}
	project, err := s.repo.ProjectForUser(owner.UserID, owner.ProjectID)
	if err != nil {
		return nil, nil, nil, errors.New("作品容器行不存在，无法提交出图任务")
	}
	asset, err := s.repo.ShortplayAssetByID(owner.ID, payload.AssetID)
	if errors.Is(err, repository.ErrShortplayAssetNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, fmt.Errorf("资产 %d 不属于这部短剧", payload.AssetID)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	return owner, project, asset, nil
}

func (s *Service) submitShortplayAssetImage(project *model.Project, owner *model.Shortplay, job *model.ShortplayJob, asset *model.ShortplayAsset) shortplayImageJobRun {
	// 锁定 = 别自动重做这张图；工具入口已经拒过一次，这里再判一次是状态而非常量。
	if asset.Locked {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "资产「" + asset.Name + "」的参考图已锁定，自动出图跳过；要重做先解除锁定"}
	}
	settings := parseShortplaySettings(owner.SettingsJSON).Normalized()
	prompt, appearance, operation, parent, err := s.shortplayImagePrompt(owner, asset, settings)
	if err != nil {
		// 父图在换或尚未就绪：本轮不排队、也不写错误，下一 tick 再看。
		if errors.Is(err, errShortplayImagePending) {
			return shortplayImageJobRun{}
		}
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	choice, err := s.shortplayImageModel(owner, asset.Kind)
	if err != nil {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	ratio := shortplay.AssetImageRatio(settings.Settings.Assets, asset.Kind)
	request := s.dramaImageRequestFor(shortplayDramaChoice(choice), ratio, settings.Settings.Assets.ResolutionFor(asset.Kind))
	if request.Issue != "" {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: request.Issue}
	}
	config := map[string]any{"count": "1"}
	request.apply(config)
	applyShortplayChannelSelection(config, choice)
	input := map[string]any{
		"config": config, "mode": "image", "prompt": prompt,
		"metadata": map[string]any{
			"source": "shortplay_asset_image", "domainProjectId": project.ID,
			"shortplayId": owner.ID, "shortplayJobId": job.ID, "assetId": asset.ID,
		},
	}
	sourceImageID := 0
	if parent != nil {
		reference, refErr := s.shortplayAssetImageReference(project.UserID, parent.ImageResourceID)
		if refErr != nil {
			s.shortplayRecordAssetImageFailure(owner.ID, asset.ID, repository.ShortplayAssetImageWrite{
				SourceImageID: parent.CurrentImageID, Prompt: prompt, Appearance: appearance,
			})
			s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageFailed)
			return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: refErr.Error()}
		}
		input["referenceImages"] = []any{reference}
		sourceImageID = parent.CurrentImageID
	}
	payload := shortplay.AssetImageJobPayload{AssetID: asset.ID, SourceImageID: sourceImageID, Prompt: prompt, Appearance: appearance}
	s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageQueued)
	task, err := s.submitShortplayTask(project.UserID, CreateTaskRequest{
		ProjectID:      project.ID,
		Type:           "canvas_image",
		Operation:      operation,
		Prompt:         prompt,
		LogicalModelID: choice.LogicalModelID,
		Model:          choice.ModelKey,
		Input:          input,
	})
	if err != nil {
		s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "提交出图任务失败：" + err.Error()}
	}
	s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageRunning)
	now := time.Now()
	job.TaskID, job.Status, job.StartedAt, job.UpdatedAt = task.ID, shortplay.JobRunning, &now, now
	job.LastError = ""
	if err := s.repo.UpdateShortplayJobRunState(job.ID, task.ID, payload.Encode()); err != nil {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	return shortplayImageJobRun{}
}

// errShortplayImagePending 表示本轮不排队（父图在换或尚未就绪），下一轮再看。
var errShortplayImagePending = errors.New("父资产参考图尚未就绪，等待下一轮")

// shortplayImagePrompt 渲染一次出图提示词；衍生形象以父资产当前图为底模。
// 返回 (提示词, 用于留档的外貌原文, 任务 operation, 底模父资产, 错误)。
func (s *Service) shortplayImagePrompt(owner *model.Shortplay, asset *model.ShortplayAsset, settings shortplaySettings) (string, string, string, *model.ShortplayAsset, error) {
	appearance := firstNonEmpty(asset.Appearance, asset.DisplayDescription(), asset.Name)
	prompt := production.CastImagePrompt(settings.Settings.Assets, asset.Kind, asset.Name, appearance, settings.Settings.StylePrompt, "")
	if asset.DerivedFrom == 0 {
		return prompt, appearance, "text_to_image", nil, nil
	}
	parent, err := s.repo.ShortplayAssetByID(owner.ID, asset.DerivedFrom)
	if errors.Is(err, repository.ErrShortplayAssetNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", "", nil, errors.New("父资产已删除，无法生成衍生形象；请重新创建这套形象")
	}
	if err != nil {
		return "", "", "", nil, err
	}
	if parent.ImageStatus == shortplay.ImageQueued || parent.ImageStatus == shortplay.ImageRunning {
		return "", "", "", nil, errShortplayImagePending
	}
	if parent.CurrentImageID == 0 || strings.TrimSpace(parent.ImageResourceID) == "" {
		if parent.ImageStatus == shortplay.ImageFailed {
			return "", "", "", nil, errors.New("父资产参考图生成失败，请先重试父资产")
		}
		return "", "", "", nil, errShortplayImagePending
	}
	resolvedParent := shortplay.ResolveAsset(parent, parent)
	appearance = firstNonEmpty(asset.Appearance, asset.DisplayDescription(), resolvedParent.Appearance, resolvedParent.Description, asset.Name)
	rule := firstNonEmpty(asset.DisplayRenderRule(), shortplay.DefaultRenderRule)
	parts := make([]string, 0, 3)
	for _, part := range []string{rule, appearance, settings.Settings.StylePrompt} {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n"), appearance, "image_to_image", parent, nil
}

func (s *Service) applyShortplayAssetImage(project *model.Project, owner *model.Shortplay, job *model.ShortplayJob, asset *model.ShortplayAsset) shortplayImageJobRun {
	payload := shortplay.ParseAssetImageJob(job.PayloadJSON)
	task, err := s.repo.Task(strings.TrimSpace(job.TaskID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "任务记录丢失"}
	}
	if err != nil {
		return shortplayImageJobRun{}
	}
	switch task.Status {
	case model.TaskStatusSucceeded:
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		s.shortplayRecordAssetImageFailure(owner.ID, asset.ID, repository.ShortplayAssetImageWrite{
			SourceImageID: payload.SourceImageID, Prompt: payload.Prompt, Appearance: payload.Appearance,
		})
		s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: firstNonEmpty(task.Error, "生成失败")}
	default:
		return shortplayImageJobRun{}
	}
	resourceID := shortplayTaskResource(task)
	if resourceID == "" {
		s.shortplayRecordAssetImageFailure(owner.ID, asset.ID, repository.ShortplayAssetImageWrite{
			SourceImageID: payload.SourceImageID, Prompt: payload.Prompt, Appearance: payload.Appearance,
		})
		s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "出图任务没有返回图片"}
	}
	settings := parseShortplaySettings(owner.SettingsJSON).Normalized()
	ratio := shortplay.AssetImageRatio(settings.Settings.Assets, asset.Kind)
	if resource, err := s.repo.ResourceForUser(project.UserID, resourceID); err == nil && resource != nil &&
		dramaImageRatioMismatch(resource.Width, resource.Height, ratio) {
		s.shortplayRecordAssetImageFailure(owner.ID, asset.ID, repository.ShortplayAssetImageWrite{
			SourceImageID: payload.SourceImageID, Prompt: payload.Prompt, Appearance: payload.Appearance,
		})
		s.shortplayMarkAssetImageStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: fmt.Sprintf(
			"图片模型返回了 %d×%d 的参考图，与要求的 %s 不符；结果未采用，请换用支持 %s 的图片模型后重新出图",
			resource.Width, resource.Height, dramaRatioLabel(ratio), ratio)}
	}
	applied, err := s.repo.SetShortplayAssetImageFrom(owner.ID, asset.ID, repository.ShortplayAssetImageWrite{
		ResourceID: resourceID, SourceImageID: payload.SourceImageID,
		Prompt: payload.Prompt, Appearance: payload.Appearance,
	}, shortplay.ImageInFlight())
	if err != nil {
		return shortplayImageJobRun{}
	}
	if !applied {
		// CAS 没命中：这张图在等待期间被别的写入收口了（用户重做）。不算失败。
		return shortplayImageJobRun{Status: shortplay.JobSucceeded}
	}
	return shortplayImageJobRun{Status: shortplay.JobSucceeded}
}

func (s *Service) shortplayMarkAssetImageStatus(shortplayID string, assetID int, status string) {
	if err := s.repo.UpdateShortplayAssetImageStatus(shortplayID, assetID, status); err != nil {
		_ = err
	}
}

func (s *Service) shortplayRecordAssetImageFailure(shortplayID string, assetID int, write repository.ShortplayAssetImageWrite) {
	if err := s.repo.RecordShortplayAssetImageFailure(shortplayID, assetID, write); err != nil {
		_ = err
	}
}

// shortplayAssetImageReference 把父资产当前图构造成任务输入里的参考图对象。
func (s *Service) shortplayAssetImageReference(userID, resourceID string) (map[string]any, error) {
	resource, err := s.repo.ResourceForUser(userID, resourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("父资产参考图不可用：资源不存在或不属于当前账号")
		}
		return nil, err
	}
	if resource.Kind != "image" || !strings.HasPrefix(resource.MimeType, "image/") {
		return nil, errors.New("父资产参考图不可用：不是图片资源")
	}
	if resource.Status != model.ResourceStatusReady {
		return nil, errors.New("父资产参考图不可用：资源尚未就绪")
	}
	return map[string]any{
		"id": "参考图1", "name": "参考图1", "type": resource.MimeType, "mimeType": resource.MimeType,
		"storageKey": "resource:" + resource.ID, "bytes": resource.Size,
		"width": resource.Width, "height": resource.Height, "role": "reference_image",
	}, nil
}

// shortplayImageModel 取某类参考图实际使用的图片模型：按类别覆盖优先，未选时跟随默认
// 图片模型，两者都没有就按平台目录第一个可用项兜底。
func (s *Service) shortplayImageModel(owner *model.Shortplay, kind string) (shortplayModelChoice, error) {
	models := parseShortplaySettings(owner.SettingsJSON).Models
	if choice := models.imageFor(kind); !choice.empty() {
		return choice, nil
	}
	options, _, err := s.dramaModelOptions(owner.UserID, "image")
	if err != nil {
		return shortplayModelChoice{}, errors.New("读取图片模型失败：" + err.Error())
	}
	if len(options) == 0 {
		return shortplayModelChoice{}, errors.New("没有可用的图片模型，无法出参考图")
	}
	return shortplayModelChoice{
		LogicalModelID: options[0].Choice.LogicalModelID,
		ChannelID:      options[0].Choice.ChannelID,
		ModelKey:       options[0].Choice.ModelKey,
	}, nil
}

// shortplayTaskResource 从任务结果里取图片资源 ID；短剧只关心「有没有一张图」。
func shortplayTaskResource(task *model.Task) string {
	if task == nil {
		return ""
	}
	if id, _ := taskOutputResource(task.ResultJSON, task.Type); id != "" {
		return id
	}
	var value any
	if json.Unmarshal([]byte(task.ResultJSON), &value) != nil {
		return ""
	}
	var find func(any) string
	find = func(node any) string {
		switch item := node.(type) {
		case map[string]any:
			if id, ok := item["resourceId"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
			for _, child := range item {
				if id := find(child); id != "" {
					return id
				}
			}
		case []any:
			for _, child := range item {
				if id := find(child); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return find(value)
}
