package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W05 音色样本 job：与 shortplay_asset_image.go 逐层同构。
//
// 差别只有三处：产物落 shortplay_asset_audios 并推进 current_audio_id；提示词来源是
// voice 文本而不是 appearance；只有角色有音色槽。
//
// 页面与 Agent 都只入队；提交才是计费单位，所以提交、轮询与回写都在这里。

// QueueShortplayAssetAudio 给角色资产排音色样本 job，返回三档计数。
// 非角色、锁定、没有音色文本都计入 Skipped：批量入口里混着道具与场景是常态。
func (s *Service) QueueShortplayAssetAudio(userID, idOrProject string, req shortplay.QueueAssetAudioRequest) (shortplay.AudioQueueResult, error) {
	owner, err := s.shortplayRowForUser(userID, idOrProject)
	if err != nil {
		return shortplay.AudioQueueResult{}, err
	}
	assets, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return shortplay.AudioQueueResult{}, err
	}
	wanted := make(map[int]bool, len(req.AssetIDs))
	for _, id := range req.AssetIDs {
		if id > 0 {
			wanted[id] = true
		}
	}
	result := shortplay.AudioQueueResult{JobIDs: []string{}}
	for index := range assets {
		asset := assets[index]
		if len(wanted) > 0 && !wanted[asset.ID] {
			result.Skipped++
			continue
		}
		if asset.Kind != shortplay.AssetCharacter || asset.Locked || strings.TrimSpace(asset.Voice) == "" {
			result.Skipped++
			continue
		}
		if (req.OnlyMissing && asset.AudioStatus != "") || (req.OnlyFailed && asset.AudioStatus != shortplay.ImageFailed) {
			result.Skipped++
			continue
		}
		job := &model.ShortplayJob{
			ID:          newID(),
			ShortplayID: owner.ID,
			Kind:        shortplay.JobAssetAudio,
			Status:      shortplay.JobQueued,
			DedupeKey:   shortplay.JobDedupeKey(shortplay.JobAssetAudio, owner.ID+"|"+strconv.Itoa(asset.ID)),
			PayloadJSON: shortplay.AssetAudioJobPayload{AssetID: asset.ID}.Encode(),
		}
		existing, created, enqueueErr := s.repo.EnqueueShortplayJob(job)
		if enqueueErr != nil {
			return shortplay.AudioQueueResult{}, enqueueErr
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

// tickShortplayAssetAudio 与出图同构：先收口已提交任务，再领取新排队。
func (s *Service) tickShortplayAssetAudio(ctx context.Context) {
	if !s.shortplayCanDispatch(ctx) {
		return
	}
	for i := 0; i < shortplayExtractPerTick; i++ {
		if !s.shortplayCanDispatch(ctx) {
			return
		}
		job, err := s.takeOrphanedShortplayJob(ctx, shortplay.JobAssetAudio)
		if err != nil {
			slog.Warn("shortplay asset audio orphan recovery failed", "error", err)
			return
		}
		if job == nil {
			job, err = s.repo.RunningShortplayJob(shortplay.JobAssetAudio)
			if err != nil {
				slog.Warn("shortplay asset audio running lookup failed", "error", err)
				return
			}
		}
		if job == nil {
			if !s.shortplayCanDispatch(ctx) {
				return
			}
			job, err = s.repo.ClaimShortplayJob(shortplay.JobAssetAudio)
			if err != nil {
				slog.Warn("shortplay asset audio claim failed", "error", err)
				return
			}
		}
		if job == nil {
			return
		}
		run := s.runShortplayAssetAudioJob(job)
		if run.Status == "" {
			// 未提交任务（等父资产样本）：退回 queued 让下一 tick 再判。
			if strings.TrimSpace(job.TaskID) == "" {
				if err := s.repo.ReleaseShortplayJob(job.ID); err != nil {
					slog.Warn("shortplay asset audio release failed", "jobId", job.ID, "error", err)
				}
			}
			continue
		}
		if err := s.finishShortplayJob(job, run.Status, run.LastError, run.Payload); err != nil {
			slog.Warn("shortplay asset audio finish failed", "jobId", job.ID, "error", err)
		}
	}
}

// runShortplayAssetAudioJob 执行一轮：未提交就提交，已提交就读结果回写。
func (s *Service) runShortplayAssetAudioJob(job *model.ShortplayJob) shortplayImageJobRun {
	owner, project, asset, err := s.shortplayAudioJobTargets(job)
	if err != nil {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	if strings.TrimSpace(job.TaskID) == "" {
		return s.submitShortplayAssetAudio(project, owner, job, asset)
	}
	return s.applyShortplayAssetAudio(project, owner, job, asset)
}

// shortplayAudioJobTargets 读出作品、容器行与目标资产；任何缺失都让 job 明确失败。
func (s *Service) shortplayAudioJobTargets(job *model.ShortplayJob) (*model.Shortplay, *model.Project, *model.ShortplayAsset, error) {
	payload := shortplay.ParseAssetAudioJob(job.PayloadJSON)
	if payload.AssetID <= 0 {
		return nil, nil, nil, errors.New("音色任务缺少资产序号")
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
		return nil, nil, nil, errors.New("作品容器行不存在，无法提交音色任务")
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

func (s *Service) submitShortplayAssetAudio(project *model.Project, owner *model.Shortplay, job *model.ShortplayJob, asset *model.ShortplayAsset) shortplayImageJobRun {
	// 只有角色有音色槽：工具调用与 job 派发之间有一整个 tick，kind 可能刚被改掉。
	if asset.Kind != shortplay.AssetCharacter {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "只有角色资产能生成音色样本"}
	}
	if asset.Locked {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "资产「" + asset.Name + "」已锁定，自动生成跳过；要重做先解除锁定"}
	}
	prompt := strings.TrimSpace(asset.Voice)
	if prompt == "" {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "该角色没有音色文本（voice）；先在资产设定里写一句声音描述再生成"}
	}
	choice, err := s.shortplayAudioModel(owner)
	if err != nil {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	// 衍生形象先等父资产音色落定：父样本在途时本轮不排队，避免刚生成就落后于父资产。
	// 参考音不送：音频能力规格恒为空，且 openai-audio / async-audio 协议本身不发参考音；
	// 「记了来源就必须真发出去」，所以 sourceAudioId 保持空。
	if asset.DerivedFrom != 0 {
		parent, parentErr := s.repo.ShortplayAssetByID(owner.ID, asset.DerivedFrom)
		if errors.Is(parentErr, repository.ErrShortplayAssetNotFound) || errors.Is(parentErr, gorm.ErrRecordNotFound) {
			return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "父资产已删除，无法生成衍生形象的音色样本"}
		}
		if parentErr != nil {
			return shortplayImageJobRun{}
		}
		if parent.AudioStatus == shortplay.ImageQueued || parent.AudioStatus == shortplay.ImageRunning {
			return shortplayImageJobRun{}
		}
	}
	config := map[string]any{"audioFormat": "mp3"}
	applyShortplayChannelSelection(config, choice)
	input := map[string]any{
		"config": config, "mode": "audio", "prompt": prompt,
		"metadata": map[string]any{
			"source": "shortplay_asset_audio", "domainProjectId": project.ID,
			"shortplayId": owner.ID, "shortplayJobId": job.ID, "assetId": asset.ID,
		},
	}
	payload := shortplay.AssetAudioJobPayload{AssetID: asset.ID, Prompt: prompt}
	job.PayloadJSON = payload.Encode()
	_, err = s.submitShortplayTaskForJob(job, project.UserID, CreateTaskRequest{
		ProjectID:      project.ID,
		Type:           "canvas_audio",
		Operation:      shortplay.AudioOperation,
		Prompt:         prompt,
		LogicalModelID: choice.LogicalModelID,
		Model:          choice.ModelKey,
		Input:          input,
	}, payload.Encode())
	if err != nil {
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "提交音频任务失败：" + err.Error()}
	}
	job.LastError = ""
	return shortplayImageJobRun{}
}

func (s *Service) applyShortplayAssetAudio(project *model.Project, owner *model.Shortplay, job *model.ShortplayJob, asset *model.ShortplayAsset) shortplayImageJobRun {
	payload := shortplay.ParseAssetAudioJob(job.PayloadJSON)
	task, err := s.repo.Task(strings.TrimSpace(job.TaskID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.shortplayMarkAssetAudioStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "任务记录丢失"}
	}
	if err != nil {
		return shortplayImageJobRun{}
	}
	switch task.Status {
	case model.TaskStatusSucceeded:
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		s.shortplayRecordAssetAudioFailure(owner.ID, asset.ID, repository.ShortplayAssetAudioWrite{
			SourceAudioID: payload.SourceAudioID, Prompt: payload.Prompt,
		})
		s.shortplayMarkAssetAudioStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: firstNonEmpty(task.Error, "生成失败")}
	case model.TaskStatusSubmissionUncertain:
		s.shortplayRecordAssetAudioFailure(owner.ID, asset.ID, repository.ShortplayAssetAudioWrite{
			SourceAudioID: payload.SourceAudioID, Prompt: payload.Prompt,
		})
		s.shortplayMarkAssetAudioStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "上游提交结果待核对，未自动重发"}
	default:
		return shortplayImageJobRun{}
	}
	resourceID := shortplayTaskResource(task)
	if resourceID == "" {
		s.shortplayRecordAssetAudioFailure(owner.ID, asset.ID, repository.ShortplayAssetAudioWrite{
			SourceAudioID: payload.SourceAudioID, Prompt: payload.Prompt,
		})
		s.shortplayMarkAssetAudioStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "音频任务没有返回音频"}
	}
	// 只接受音频资源：音色样本会被下游视频任务当参考音，错类型进不去。
	if resource, resErr := s.repo.ResourceForUser(project.UserID, resourceID); resErr == nil && resource != nil &&
		(resource.Kind != "audio" || !strings.HasPrefix(resource.MimeType, "audio/")) {
		s.shortplayRecordAssetAudioFailure(owner.ID, asset.ID, repository.ShortplayAssetAudioWrite{
			SourceAudioID: payload.SourceAudioID, Prompt: payload.Prompt,
		})
		s.shortplayMarkAssetAudioStatus(owner.ID, asset.ID, shortplay.ImageFailed)
		return shortplayImageJobRun{Status: shortplay.JobFailed, LastError: "音频任务返回的不是音频资源，结果未采用"}
	}
	applied, err := s.repo.SetShortplayAssetAudioFrom(owner.ID, asset.ID, repository.ShortplayAssetAudioWrite{
		ResourceID: resourceID, SourceAudioID: payload.SourceAudioID, Prompt: payload.Prompt,
	}, shortplay.AudioInFlight())
	if err != nil {
		return shortplayImageJobRun{}
	}
	if !applied {
		// CAS 没命中：这段样本在等待期间被别的写入收口了（用户重做）。按已被取代处理。
		return shortplayImageJobRun{Status: shortplay.JobSucceeded}
	}
	return shortplayImageJobRun{Status: shortplay.JobSucceeded}
}

func (s *Service) shortplayMarkAssetAudioStatus(shortplayID string, assetID int, status string) {
	if err := s.repo.UpdateShortplayAssetAudioStatus(shortplayID, assetID, status); err != nil {
		_ = err
	}
}

func (s *Service) shortplayRecordAssetAudioFailure(shortplayID string, assetID int, write repository.ShortplayAssetAudioWrite) {
	if err := s.repo.RecordShortplayAssetAudioFailure(shortplayID, assetID, write); err != nil {
		_ = err
	}
}

// shortplayAudioModel 取音色样本使用的音频模型：设置里的 audio 槽优先，
// 未选时跟随平台目录第一个可用项（与 shortplayImageModel 同一策略）。
func (s *Service) shortplayAudioModel(owner *model.Shortplay) (shortplayModelChoice, error) {
	if choice := parseShortplaySettings(owner.SettingsJSON).Models.Audio; !choice.empty() {
		return choice, nil
	}
	options, _, err := s.dramaModelOptions(owner.UserID, "audio")
	if err != nil {
		return shortplayModelChoice{}, errors.New("读取音频模型失败：" + err.Error())
	}
	if len(options) == 0 {
		return shortplayModelChoice{}, errors.New("没有可用的音频模型，无法生成音色样本")
	}
	return shortplayModelChoice{
		LogicalModelID: options[0].Choice.LogicalModelID,
		ChannelID:      options[0].Choice.ChannelID,
		ModelKey:       options[0].Choice.ModelKey,
	}, nil
}
