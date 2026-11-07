package app

import (
	"context"
	"encoding/json"
	"fmt"

	as "yingce/backend/internal/agentservice"
)

// dramaAgentAdapter lets the drama Agent read its project and drive the
// existing production service. Every rule (models, budget, review) stays in
// the drama service; the adapter only translates calls.
type dramaAgentAdapter struct{ s *Service }

func (dramaAgentAdapter) Application() string  { return as.ApplicationDrama }
func (dramaAgentAdapter) SpaceTypes() []string { return []string{as.SpaceDramaProject} }

func (a dramaAgentAdapter) ValidateSpace(_ context.Context, ownerID string, scope as.Scope) error {
	return a.s.validateBuiltinAgentSpace(ownerID, scope)
}

func (dramaAgentAdapter) Tools() []as.ToolSpec {
	return []as.ToolSpec{
		{Name: "drama_summary", Description: "读取当前短剧项目的制作状态、分集与待办", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "drama_set_running", Description: "开始或暂停自动制作；开始前由短剧服务校验模型与预算", Parameters: json.RawMessage(`{"type":"object","properties":{"running":{"type":"boolean"}},"required":["running"]}`), Charges: true},
		{Name: "drama_update_shot", Description: "修改一个镜头的台词或画面提示词：镜头 ID 与要改的字段",
			Parameters: json.RawMessage(`{"type":"object","properties":{"shotId":{"type":"string"},"dialogue":{"type":"string"},"imagePrompt":{"type":"string"},"action":{"type":"string"}},"required":["shotId"]}`)},
		{Name: "drama_generate_segments", Description: "为指定分集提交剧情段视频重生成：分集 ID 与剧情段序号列表", Charges: true,
			Parameters: json.RawMessage(`{"type":"object","properties":{"episodeId":{"type":"string"},"segments":{"type":"array","items":{"type":"integer"}}},"required":["episodeId","segments"]}`)},
		{Name: "drama_approve_episode", Description: "审阅采用指定分集；短剧服务校验生成都已完成", Charges: true,
			Parameters: json.RawMessage(`{"type":"object","properties":{"episodeId":{"type":"string"}},"required":["episodeId"]}`)},
		{Name: "drama_retry_failed", Description: "重试本项目所有失败的任务", Charges: true,
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "drama_reference_asset", Description: "校验并引用一个素材：返回素材身份，供本项目的生成使用",
			Parameters: json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"]}`)},
	}
}

func (a dramaAgentAdapter) Execute(_ context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	if err := a.bindChargedOperation(call); err != nil {
		return as.ToolResult{}, err
	}
	switch call.Name {
	case "drama_summary":
		summary, err := a.s.DramaSummary(call.OwnerID, call.Scope.SpaceID)
		if err != nil {
			return as.ToolResult{}, err
		}
		return as.ToolResult{Text: fmt.Sprintf("短剧《%s》：状态 %s，阶段 %s，共 %d 集，%d 个角色/场景设定。", summary.Project.Name, summary.Production.Status, summary.Production.Stage, len(summary.Episodes), summary.CastCount)}, nil
	case "drama_set_running":
		var args struct {
			Running bool `json:"running"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "参数无效")
		}
		var summary DramaSummary
		var err error
		if args.Running && call.WorkItemID != "" {
			summary, err = a.s.SetDramaRunningUnderWork(call.OwnerID, call.Scope.SpaceID, true, call.WorkItemID, call.BudgetGrantID)
		} else {
			summary, err = a.s.SetDramaRunning(call.OwnerID, call.Scope.SpaceID, args.Running)
		}
		if err != nil {
			return as.ToolResult{}, err
		}
		return as.ToolResult{Text: "制作状态：" + summary.Production.Status, Operation: "drama:" + call.Scope.SpaceID}, nil
	case "drama_update_shot":
		return a.updateShot(call)
	case "drama_generate_segments":
		return a.generateSegments(call)
	case "drama_approve_episode":
		return a.approveEpisode(call)
	case "drama_retry_failed":
		retried, err := a.s.RetryFailedDrama(call.OwnerID, call.Scope.SpaceID)
		if err != nil {
			return as.ToolResult{}, err
		}
		return as.ToolResult{Text: fmt.Sprintf("失败任务重试结果：%v", retried["retried"])}, nil
	case "drama_reference_asset":
		var args struct {
			ResourceID string `json:"resourceId"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil || args.ResourceID == "" {
			return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "需要素材 ID")
		}
		resource, err := a.s.resolveAgentAssetReference(call.OwnerID, args.ResourceID)
		if err != nil {
			return as.ToolResult{}, err
		}
		return as.ToolResult{Text: fmt.Sprintf("素材 %s（%s，%d 字节）可在本项目使用，来源可追溯。", resource.ID, resource.MimeType, resource.Size), Operation: resource.ID}, nil
	}
	return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不存在")
}

func (a dramaAgentAdapter) updateShot(call as.ToolInvocation) (as.ToolResult, error) {
	if call.OperationID != "" {
		return a.updateShotAtomically(call)
	}
	var args struct {
		ShotID      string  `json:"shotId"`
		Dialogue    *string `json:"dialogue"`
		ImagePrompt *string `json:"imagePrompt"`
		Action      *string `json:"action"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || args.ShotID == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "需要镜头 ID")
	}
	shot, err := a.s.UpdateDramaShot(call.OwnerID, call.Scope.SpaceID, args.ShotID, UpdateDramaShotRequest{Dialogue: args.Dialogue, ImagePrompt: args.ImagePrompt, Action: args.Action})
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: "镜头 " + shot.ID + " 已更新。", Operation: shot.ID}, nil
}

func (a dramaAgentAdapter) generateSegments(call as.ToolInvocation) (as.ToolResult, error) {
	var args struct {
		EpisodeID string `json:"episodeId"`
		Segments  []int  `json:"segments"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || args.EpisodeID == "" || len(args.Segments) == 0 {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "需要分集与剧情段序号")
	}
	result, err := a.s.GenerateDramaSegments(call.OwnerID, call.Scope.SpaceID, args.EpisodeID, GenerateDramaSegmentsRequest{Segments: args.Segments})
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("剧情段生成已提交：%v", result["submitted"]), Operation: args.EpisodeID}, nil
}

func (a dramaAgentAdapter) approveEpisode(call as.ToolInvocation) (as.ToolResult, error) {
	var args struct {
		EpisodeID string `json:"episodeId"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || args.EpisodeID == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "需要分集 ID")
	}
	detail, err := a.s.ApproveDramaEpisode(call.OwnerID, call.Scope.SpaceID, args.EpisodeID)
	if err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("分集 %s 已审阅采用。", detail.Episode.ID), Operation: detail.Episode.ID}, nil
}
