package app

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Only user-editable media options cross the approval boundary. Targets,
// references, prompt, snapshot and generation mode remain server-authored.
type CloudAgentMediaSettings struct {
	ImageResolution            string `json:"imageResolution,omitempty"`
	ImageParameters            string `json:"imageParameters,omitempty"`
	DurationSeconds            *int   `json:"durationSeconds,omitempty"`
	VideoGenerateAudio         *bool  `json:"videoGenerateAudio,omitempty"`
	VideoWatermark             *bool  `json:"videoWatermark,omitempty"`
	VideoArkPrivateAssetUpload *bool  `json:"videoArkPrivateAssetUpload,omitempty"`
	LogicalModelID             string `json:"logicalModelId,omitempty"`
	ChannelID                  string `json:"channelId,omitempty"`
	ChannelModelKey            string `json:"channelModelKey,omitempty"`
	Size                       string `json:"size"`
	Quality                    string `json:"quality"`
}

// updateCloudAgentMediaApproval rebuilds the complete prepared admission for an
// edited approval. It is called inside MutateCloudAgent so the new prepared
// hash, resource lease set and runtime checkpoint commit atomically.
func (s *Service) updateCloudAgentMediaApproval(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, settings CloudAgentMediaSettings) error {
	if state == nil || state.Approval == nil {
		return creationConflict("审批不存在或已过期")
	}
	call := state.Approval.Call
	if call.Function.Name == "generate_media_batch" {
		return s.updateCloudAgentMediaBatchApproval(repo, run, state, settings)
	}
	if call.Function.Name != "generate_media" {
		return BadAuthRequest("当前审批不是图片生成，不能修改生成参数")
	}
	var args cloudAgentMediaArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return err
	}
	if err := applyCloudAgentMediaApprovalSettings(&args, settings); err != nil {
		return err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	call.Function.Arguments = string(raw)
	req, plan, err := s.prepareCloudAgentMedia(run, state, call)
	if err != nil {
		return err
	}
	task, preparation, err := s.prepareCloudAgentEditedMedia(run, state, &req, plan)
	if err != nil {
		return err
	}
	args = plan.Args
	raw, err = json.Marshal(args)
	if err != nil {
		return err
	}
	call.Function.Arguments = string(raw)
	name, err := s.cloudAgentMediaModelName(args)
	if err != nil {
		return err
	}
	canvas, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
	if err != nil {
		return err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return err
	}
	prepared, err := prepareCloudAgentMediaApproval(repo, run.UserID, doc, plan, req, task, preparation.Order)
	if err != nil {
		return err
	}
	// Editing an approval changes the prepared inputs and quote, but it is still
	// the same user-approved generation intent until the user submits a new
	// generation from the canvas.
	if previous := state.Approval.Prepared; previous != nil && previous.GenerationID != "" {
		prepared.GenerationID = previous.GenerationID
		prepared.Hash = creationHash(prepared)
	}
	if err := replaceCloudAgentPreparedLeases(repo, run.UserID, run.ID, state.Approval.ID, prepared); err != nil {
		return err
	}
	state.Calls[state.CallIndex] = call
	state.Approval.Call = call
	state.Approval.CallHash = cloudAgentApprovalCallHash(call)
	state.Approval.ModelName = name
	state.Approval.Preview = cloudAgentMediaApprovalPreview(plan, name)
	state.Approval.Prepared = prepared
	return nil
}

func applyCloudAgentMediaApprovalSettings(args *cloudAgentMediaArgs, settings CloudAgentMediaSettings) error {
	if args.Mode != "image" && args.Mode != "video" {
		return BadAuthRequest("仅图片或视频生成审批支持修改模型和规格")
	}
	args.LogicalModelID, args.ChannelID, args.ChannelModelKey = settings.LogicalModelID, settings.ChannelID, settings.ChannelModelKey
	// These settings now come directly from the user, not the Agent's proposal.
	args.Size, args.ImageResolution, args.Quality = settings.Size, settings.ImageResolution, settings.Quality
	args.ImageParameters = settings.ImageParameters
	if args.Mode == "video" {
		if settings.DurationSeconds != nil {
			if *settings.DurationSeconds < 0 {
				return BadAuthRequest("视频时长不能为负数")
			}
			args.Duration = *settings.DurationSeconds
		}
		args.VideoGenerateAudio = settings.VideoGenerateAudio
		args.VideoWatermark = settings.VideoWatermark
		args.VideoArkPrivateAssetUpload = settings.VideoArkPrivateAssetUpload
	}
	if args.Mode == "image" && (settings.DurationSeconds != nil || settings.VideoGenerateAudio != nil || settings.VideoWatermark != nil || settings.VideoArkPrivateAssetUpload != nil) {
		return BadAuthRequest("视频参数仅适用于视频生成审批")
	}
	return nil
}

func (s *Service) prepareCloudAgentEditedMedia(run *model.CloudAgentExecution, state *cloudAgentRuntime, req *CreateTaskRequest, plan *cloudAgentMediaPlan) (*model.Task, *creationTaskPreparation, error) {
	// Dry admission uses the same model, ownership and option checks as task
	// submission, without reserving credits or creating a generation task.
	// DecideCloudAgentApproval already holds storageMu for this mutation.
	preparation := &creationTaskPreparation{}
	req.creationPrepare = preparation
	req.callerHoldsStorageMu = true
	task, err := s.CreateTask(run.UserID, *req)
	if err != nil {
		return nil, nil, err
	}
	var resolved map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &resolved); err != nil {
		return nil, nil, err
	}
	requested, _ := req.Input["config"].(map[string]any)
	resolvedConfig, _ := resolved["config"].(map[string]any)
	if stringValue(resolved["imageResolution"]) != "" && stringValue(requested["quality"]) == stringValue(resolved["imageResolution"]) {
		// 用户在审批卡里点选的档位已换算进像素尺寸，不再作为质量参数参与核对。
		delete(requested, "quality")
		plan.Args.Quality = ""
	}
	if err := validateCloudAgentResolvedMediaOptions(requested, resolvedConfig); err != nil {
		return nil, nil, err
	}
	if err := applyCloudAgentResolvedMediaDefaults(req, plan, task); err != nil {
		return nil, nil, err
	}
	if err := validateCloudAgentMediaArgs(plan.Args, state); err != nil {
		return nil, nil, err
	}
	return task, preparation, nil
}

// Batch edits retain the server-prepared targets, prompts and references. Only
// editable settings are replaced, and every item is admitted again before the
// quote, leases and approval checkpoint are replaced in the same transaction.
func (s *Service) updateCloudAgentMediaBatchApproval(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, settings CloudAgentMediaSettings) error {
	previous := state.MediaBatch
	call := state.Approval.Call
	if !cloudAgentBatchApprovalEditable(previous, state.Approval.ID, call.ID) {
		return creationConflict("批量生成审批不存在或已过期")
	}
	args, err := decodeCloudAgentMediaBatchArgs(call.Function.Arguments)
	if err != nil {
		return err
	}
	if len(args.Items) != len(previous.Items) {
		return creationConflict("批量生成审批条目已变化")
	}
	batch := *previous
	batch.Items = make([]cloudAgentMediaBatchItem, 0, len(previous.Items))
	prepared := make([]cloudAgentMediaBatchPrepared, 0, len(previous.Items))
	resourceIDs := []string{}
	seen := map[string]bool{}
	var total int64
	videoSeconds := 0
	for index, item := range previous.Items {
		if item.NodeID != args.Items[index].NodeID || item.Status != cloudAgentMediaBatchItemPending || item.Attempts != 0 {
			return creationConflict("批量生成审批条目已变化")
		}
		entry, stored, err := s.prepareCloudAgentBatchApprovalEdit(run, state, call.ID, index, item, args.Mode, settings)
		if err != nil {
			return err
		}
		item = entry.item
		name := entry.model
		plan := entry.plan

		for _, id := range item.ResourceIDs {
			if !seen[id] {
				seen[id] = true
				resourceIDs = append(resourceIDs, id)
			}
		}
		total += item.Amount
		videoSeconds += item.Duration
		batch.Items = append(batch.Items, item)
		prepared = append(prepared, cloudAgentMediaBatchPrepared{plan: plan, item: item, model: name})
		if index == 0 {
			batch.ModelName = name
			args.LogicalModelID, args.ChannelID, args.ChannelModelKey = stored.LogicalModelID, stored.ChannelID, stored.ChannelModelKey
			args.Size, args.Quality, args.ImageResolution, args.ImageParameters = stored.Size, stored.Quality, stored.ImageResolution, stored.ImageParameters
			args.VideoGenerateAudio, args.VideoWatermark, args.VideoArkPrivateAssetUpload = stored.VideoGenerateAudio, stored.VideoWatermark, stored.VideoArkPrivateAssetUpload
		}
		// An explicit duration applies to the whole batch, including items
		// that previously had individual overrides. Omission preserves them.
		if settings.DurationSeconds != nil {
			args.Items[index].Duration = stored.Duration
			args.Duration = stored.Duration
		}
	}
	if err := validateCloudAgentBatchEditedBudget(repo, run, state, &batch, args.Mode, total, videoSeconds); err != nil {
		return err
	}
	batch.CapMicrocredits = total * int64(1+batch.MaxRetries)
	if raw, err := json.Marshal(batch); err != nil {
		return err
	} else if len(raw) > cloudAgentMediaBatchStateLimit {
		return BadAuthRequest("批量生成审批内容过长，请拆分批次")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	call.Function.Arguments = string(raw)
	if err := repo.ReplaceCloudAgentResourceLeases(run.UserID, run.ID, state.Approval.ID, resourceIDs, time.Now().UTC().Add(cloudAgentMediaBatchLeaseLifetime)); err != nil {
		return err
	}
	state.MediaBatch = &batch
	state.Calls[state.CallIndex] = call
	state.Approval.Call = call
	state.Approval.CallHash = cloudAgentApprovalCallHash(call)
	state.Approval.ModelName = batch.ModelName
	state.Approval.Preview = cloudAgentMediaBatchApprovalPreview(&batch, prepared)
	return nil
}

func cloudAgentBatchApprovalEditable(batch *cloudAgentMediaBatch, approvalID, callID string) bool {
	return batch != nil && batch.ID == approvalID && batch.CallID == callID && batch.Phase == cloudAgentMediaBatchPhaseApproval
}

func replaceCloudAgentPreparedLeases(repo *repository.Repository, userID, runID, approvalID string, prepared *cloudAgentPreparedMedia) error {
	ids := make([]string, 0, len(prepared.ResourceSignatures))
	for id := range prepared.ResourceSignatures {
		ids = append(ids, id)
	}
	return repo.ReplaceCloudAgentResourceLeases(userID, runID, approvalID, ids, prepared.Quote.ExpiresAt)
}

func validateCloudAgentResolvedMediaOptions(requested, resolved map[string]any) error {
	for _, key := range []string{"size", "videoSeconds", "vquality", "quality", "count", "videoGenerateAudio"} {
		actual := stringValue(resolved[key])
		// 带分辨率档位的图片模型在 admission 时把 quality 归一为 imageResolution。
		if key == "quality" && actual == "" {
			actual = stringValue(resolved["imageResolution"])
		}
		if value := stringValue(requested[key]); value != "" && !strings.EqualFold(value, actual) {
			return creationConflict("模型解析后的生成规格与审批参数不同，请重新读取目录并审批")
		}
	}
	return nil
}

func (s *Service) prepareCloudAgentBatchApprovalEdit(run *model.CloudAgentExecution, state *cloudAgentRuntime, callID string, index int, item cloudAgentMediaBatchItem, mode string, settings CloudAgentMediaSettings) (cloudAgentMediaBatchPrepared, cloudAgentMediaArgs, error) {
	var itemArgs cloudAgentMediaArgs
	if err := decodeCloudAgentJSONObject(item.Arguments, &itemArgs); err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	if itemArgs.Mode != mode || itemArgs.NodeID != item.NodeID {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, creationConflict("批量生成审批条目不一致")
	}
	if err := applyCloudAgentMediaApprovalSettings(&itemArgs, settings); err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	itemCall, err := cloudAgentMediaBatchItemCall(callID, index, itemArgs)
	if err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	// Keep the approval in the state: user-selected settings must not be
	// replaced by the Agent's configuration inheritance rules.
	req, plan, err := s.prepareCloudAgentMedia(run, state, itemCall)
	if err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	task, preparation, err := s.prepareCloudAgentEditedMedia(run, state, &req, plan)
	if err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	item.ResolvedHash, err = cloudAgentResolvedMediaHash(task)
	if err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	item.SpecHash, err = cloudAgentResolvedMediaSpecHash(task)
	if err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	name, err := s.cloudAgentMediaModelName(plan.Args)
	if err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	stored := plan.Args
	stored.Prompt, stored.SnapshotHash = itemArgs.Prompt, ""
	raw, err := json.Marshal(stored)
	if err != nil {
		return cloudAgentMediaBatchPrepared{}, cloudAgentMediaArgs{}, err
	}
	item.Arguments, item.Duration, item.Amount = string(raw), stored.Duration, 0
	if preparation.Order != nil {
		item.Amount = preparation.Order.AmountMicrocredits
	}
	item.ResourceIDs = cloudAgentMediaBatchResourceIDs(req.Input)
	return cloudAgentMediaBatchPrepared{plan: plan, item: item, model: name}, stored, nil
}

func validateCloudAgentBatchEditedBudget(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, batch *cloudAgentMediaBatch, mode string, total int64, videoSeconds int) error {
	budget := state.Request.Budget
	if budget.MaxGenerationTasks > 0 && state.Generations+len(batch.Items) > budget.MaxGenerationTasks {
		return BadAuthRequest("批量生成超过本轮生成次数预算")
	}
	if mode == "video" && budget.MaxVideoSeconds > 0 && state.VideoSeconds+videoSeconds > budget.MaxVideoSeconds {
		return BadAuthRequest("批量生成超过本轮视频时长预算")
	}
	orders, err := repo.BillingOrdersByTaskIDs(run.UserID, state.TaskIDs)
	if err != nil {
		return err
	}
	remaining := int64(math.Floor(budget.MaxCredits * float64(CreditScale)))
	for _, order := range orders {
		remaining -= order.AmountMicrocredits
	}
	if total > remaining {
		return BadAuthRequest("批量生成超过本轮积分预算")
	}

	return nil
}
