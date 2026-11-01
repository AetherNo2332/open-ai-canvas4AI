package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// prepareCloudAgentDirectorPreview 校验场景、镜头与摄影机并冻结预演参数。
func prepareCloudAgentDirectorPreview(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentDirectorPreview, error) {
	args, err := cloudAgentDirectorDecode(call)
	if err != nil {
		return nil, err
	}
	if args.Template != "" || args.Title != "" || len(args.Patch) > 0 {
		return nil, cloudAgentFieldError("patch", "unexpected_field", "preview 只接受 nodeId、sceneId、shotId 和 expectedSceneHash")
	}
	_, doc, err := cloudAgentPrevisCanvas(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	scene, nodeID, err := cloudAgentDirectorResolveScene(doc, args)
	if err != nil {
		return nil, err
	}
	if scene == nil {
		return nil, cloudAgentFieldError("sceneId", "required", "preview 必须指定 sceneId 或已绑定场景的 nodeId")
	}
	if args.ExpectedSceneHash != "" && args.ExpectedSceneHash != creationHash(scene) {
		return nil, &cloudAgentFieldArgumentError{error: &cloudAgentArgumentError{creationConflict("导演场景已变化，本次未预演；请重新读取")}, Field: "expectedSceneHash", Issue: "stale_snapshot"}
	}
	shotID := args.ShotID
	if shotID == "" {
		shotID = stringValue(scene["activeShotId"])
	}
	encoded, _ := json.Marshal(map[string]any{"sceneId": stringValue(scene["id"]), "shotId": shotID})
	inner := cloudAgentCall{ID: call.ID}
	inner.Function.Name, inner.Function.Arguments = cloudAgentDirectorTool, string(encoded)
	result, err := cloudAgentPrevisPreview(repo, userID, canvasID, inner)
	if err != nil {
		return nil, err
	}
	fields := result.(map[string]any)
	return &cloudAgentDirectorPreview{
		CallID: call.ID, CanvasID: canvasID, NodeID: nodeID, SceneID: stringValue(fields["sceneId"]), ShotID: shotID,
		Duration: numberValue(fields["duration"], 5), FPS: int(numberValue(fields["fps"], 24)),
	}, nil
}

func cloudAgentDirectorPreviewPending(state *cloudAgentRuntime, call cloudAgentCall) bool {
	return state != nil && state.DirectorPreview != nil && state.DirectorPreview.CallID == call.ID && call.Function.Name == cloudAgentDirectorTool
}

func cloudAgentDirectorPreviewReceipt(preview *cloudAgentDirectorPreview, status string) map[string]any {
	return map[string]any{"action": "preview", "status": status, "requestId": preview.RequestID, "canvasId": preview.CanvasID, "nodeId": preview.NodeID, "sceneId": preview.SceneID, "shotId": preview.ShotID, "duration": preview.Duration, "fps": preview.FPS, "execution": "browser_previs_viewport"}
}

// startCloudAgentDirectorPreview 发出 director_open 并把调用挂起为 awaiting_viewport。
// 等待只在检查点里：浏览器回传或超时都由下一次推进处理。
func (s *Service) startCloudAgentDirectorPreview(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		preview, err := prepareCloudAgentDirectorPreview(repo, run.UserID, state.Request.CanvasID, call)
		if err != nil {
			if !cloudAgentToolErrorIsBusiness(err) {
				return err
			}
			cloudAgentRecordToolResult(current, state, call, nil, err)
			return cloudAgentSave(current, state)
		}
		preview.RequestID = "dpv_" + cloudAgentID(run.UserID, run.ID+":director-preview:"+state.PiToolBatchTaskID+fmt.Sprintf(":%d:", state.Step)+call.ID)[2:]
		preview.Deadline = time.Now().UTC().Add(cloudAgentDirectorPreviewTimeout())
		state.DirectorPreview = preview
		state.event(run.ID, "director_open", cloudAgentDirectorOpenPayload(preview.CanvasID, preview.NodeID, preview.SceneID, preview.ShotID, "preview", preview.RequestID))
		receipt := cloudAgentDirectorPreviewReceipt(preview, "awaiting_viewport")
		receipt["callId"], receipt["toolName"], receipt["deadline"] = call.ID, call.Function.Name, preview.Deadline
		state.event(run.ID, "director_preview_requested", receipt)
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "waiting_tool", cloudAgentDirectorPreviewWaitKind, preview.RequestID, cloudAgentDirectorPreviewWaitLabel
		return cloudAgentSave(current, state)
	})
}

// advanceCloudAgentDirectorPreview 只处理超时：未到期时什么都不写，保持挂起。
func (s *Service) advanceCloudAgentDirectorPreview(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	if time.Now().UTC().Before(state.DirectorPreview.Deadline) {
		return nil
	}
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		preview := state.DirectorPreview
		result := cloudAgentDirectorPreviewReceipt(preview, "timed_out")
		result["reason"], result["retryable"] = cloudAgentDirectorViewportReason, true
		result["guidance"] = "导演台页面未打开或未在时限内完成录制；请提示用户打开画布中的导演台后再预览，或继续其他工作"
		state.DirectorPreview = nil
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
		cloudAgentRecordToolResult(current, state, call, result, nil)
		return cloudAgentSave(current, state)
	})
}

// SubmitCloudAgentDirectorPreview 接收浏览器导演台的录制结果并恢复挂起的 director 调用。
// 运行归属、requestId 与资源归属都在这里校验；videoResourceId 与 errorMessage 必须二选一。
func (s *Service) SubmitCloudAgentDirectorPreview(userID, runID, requestID, videoResourceID, errorMessage string) error {
	notFound := &kernel.AppError{Status: kernel.CodeNotFound, Code: kernel.CodeNotFound, Reason: cloudAgentDirectorPreviewNotFound, Message: "导演台预演请求不存在或已过期"}
	requestID, videoResourceID, errorMessage = strings.TrimSpace(requestID), strings.TrimSpace(videoResourceID), strings.TrimSpace(errorMessage)
	if requestID == "" || len(requestID) > 120 {
		return notFound
	}
	if (videoResourceID == "") == (errorMessage == "") {
		return BadAuthRequest("videoResourceId 与 error 必须且只能提供一个")
	}
	if utf8.RuneCountInString(errorMessage) > cloudAgentDirectorPreviewErrorMax || strings.ContainsAny(errorMessage, "\x00\r\n") {
		return BadAuthRequest("error 无效")
	}
	defer s.wakeAgentTools()
	// 工具推进可能同时在写同一运行；revision 冲突时重读后再试，避免把并发误报给浏览器。
	for attempt := 0; ; attempt++ {
		err := s.submitCloudAgentDirectorPreviewOnce(userID, runID, requestID, videoResourceID, errorMessage, notFound)
		if !errors.Is(err, repository.ErrCreationConflict) || attempt >= 2 {
			return err
		}
	}
}

func (s *Service) submitCloudAgentDirectorPreviewOnce(userID, runID, requestID, videoResourceID, errorMessage string, notFound error) error {
	run, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return notFound
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		// 无法解析的运行不可能有挂起的预演请求。
		return notFound
	}
	preview := state.DirectorPreview
	if cloudAgentRunTerminal(run.Status) || preview == nil || preview.RequestID != requestID || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != preview.CallID || !time.Now().UTC().Before(preview.Deadline) {
		return notFound
	}
	call := state.Calls[state.CallIndex]
	var result map[string]any
	var toolErr error
	if videoResourceID != "" {
		resource, err := s.repo.ResourceForUser(userID, videoResourceID)
		if err != nil || resource == nil || resource.Status != model.ResourceStatusReady || resource.Size <= 0 || !strings.HasPrefix(resource.MimeType, "video/") {
			return BadAuthRequest("预演视频资源不存在、未就绪或不属于当前账号")
		}
		result = cloudAgentDirectorPreviewReceipt(preview, "completed")
		result["videoResourceId"] = resource.ID
		result["text"] = "导演台已在浏览器视口录制白模预演并保存为视频资源"
	} else {
		toolErr = BadAuthRequest("导演台预演录制失败：" + errorMessage)
	}
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.DirectorPreview = nil
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
		cloudAgentRecordToolResult(current, &state, call, result, toolErr)
		return cloudAgentSave(current, &state)
	})
}

func cloudAgentDirectorPreviewAction(call cloudAgentCall) bool {
	var args struct {
		Action string `json:"action"`
	}
	return json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Action == "preview"
}
