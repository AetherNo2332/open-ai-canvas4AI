package app

import (
	"math"
	"strings"
	"unicode/utf8"
)

func validateCloudAgentRequest(req *CloudAgentRequest) error {
	if req == nil {
		return BadAuthRequest("请求不能为空")
	}
	// Checks run in this fixed order; the first failing one is reported.
	for _, check := range []func(*CloudAgentRequest) error{
		validateCloudAgentRequestBasics,
		validateCloudAgentRequestModelIDs,
		validateCloudAgentRequestFocus,
		validateCloudAgentRequestModelRoute,
		validateCloudAgentRequestBudgetBounds,
		validateCloudAgentRequestSkills,
		validateCloudAgentRequestScope,
	} {
		if err := check(req); err != nil {
			return err
		}
	}
	return nil
}

func validateCloudAgentRequestBasics(req *CloudAgentRequest) error {
	// IDs and protocol selectors are identifiers, not free-form text. Keep their
	// validation in one place so byte/rune and Unicode handling cannot drift.
	if err := validateCloudAgentID(req.CanvasID, "画布 ID", 80); err != nil {
		return err
	}
	req.CanvasID = strings.TrimSpace(req.CanvasID)
	if err := validateCloudAgentPrompt(req.Prompt, 16000); err != nil {
		return err
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	// Historical runs must remain readable and cancellable after workflow retirement.
	if req.Workflow != "" && req.Workflow != "image_to_director" {
		return BadAuthRequest("无效的 Agent 工作流")
	}
	if err := validateCloudAgentID(req.IdempotencyKey, "幂等键", 128); err != nil || utf8.RuneCountInString(req.IdempotencyKey) < 8 {
		return BadAuthRequest("需要 8–128 个字符的幂等键")
	}
	if req.PermissionMode != "read_only" && req.PermissionMode != "request_approval" && req.PermissionMode != "auto" {
		return BadAuthRequest("无效的 Agent 执行权限")
	}
	return nil
}

func validateCloudAgentRequestModelIDs(req *CloudAgentRequest) error {
	for value, spec := range map[string]struct {
		label string
		limit int
	}{
		"model":           {"模型标识", 160},
		"logicalModelId":  {"逻辑模型 ID", 80},
		"channelId":       {"渠道 ID", 80},
		"channelModelKey": {"渠道模型标识", 160},
	} {
		if value == "model" && req.Model == "" || value == "logicalModelId" && req.LogicalModelID == "" || value == "channelId" && req.ChannelID == "" || value == "channelModelKey" && req.ChannelModelKey == "" {
			continue
		}
		var candidate string
		switch value {
		case "model":
			candidate = req.Model
		case "logicalModelId":
			candidate = req.LogicalModelID
		case "channelId":
			candidate = req.ChannelID
		case "channelModelKey":
			candidate = req.ChannelModelKey
		}
		if err := validateCloudAgentID(candidate, spec.label, spec.limit); err != nil {
			return err
		}
	}
	return nil
}

// validateCloudAgentRequestFocus checks the reasoning mode and trims and
// de-duplicates the focus node IDs.
func validateCloudAgentRequestFocus(req *CloudAgentRequest) error {
	if req.ReasoningMode != "" && !validCloudAgentReasoningMode(req.ReasoningMode) {
		return BadAuthRequest("无效的 Agent 推理模式")
	}
	if len(req.FocusNodeIDs) > 8 {
		return BadAuthRequest("画布焦点节点最多 8 个")
	}
	seenFocus := make(map[string]bool, len(req.FocusNodeIDs))
	for i, id := range req.FocusNodeIDs {
		id = strings.TrimSpace(id)
		if err := validateCloudAgentID(id, "画布焦点节点 ID", 80); err != nil || seenFocus[id] {
			return BadAuthRequest("画布焦点节点 ID 无效或重复")
		}
		req.FocusNodeIDs[i] = id
		seenFocus[id] = true
	}
	return nil
}

func validateCloudAgentRequestModelRoute(req *CloudAgentRequest) error {
	if req.LogicalModelID != "" {
		if req.ChannelID != "" || req.ChannelModelKey != "" {
			return BadAuthRequest("逻辑模型和系统渠道不能混用")
		}
	} else if req.ChannelID == "" || req.ChannelModelKey == "" {
		return BadAuthRequest("请选择后端受管文本模型；Agent 不接受浏览器自定义密钥或上游地址")
	} else if req.Model != "" && req.Model != req.ChannelModelKey {
		return BadAuthRequest("渠道模型标识与 model 不一致")
	}
	return nil
}

func validateCloudAgentRequestBudgetBounds(req *CloudAgentRequest) error {
	if req.Budget.MaxGenerationTasks < 0 || req.Budget.MaxVideoSeconds < 0 || req.Budget.MaxSteps < 0 {
		return BadAuthRequest("生成任务、视频秒数和模型调用步数预算不能为负数")
	}
	return nil
}

func validateCloudAgentRequestSkills(req *CloudAgentRequest) error {
	seen := map[string]bool{}
	for i, id := range req.SkillIDs {
		id = strings.TrimSpace(id)
		if err := validateCloudAgentID(id, "技能 ID", 80); err != nil || seen[id] {
			return BadAuthRequest("技能 ID 无效或重复")
		}
		req.SkillIDs[i] = id
		seen[id] = true
	}
	return nil
}

// validateCloudAgentRequestScope checks the read-only budget, the context
// scope and the credit limit.
func validateCloudAgentRequestScope(req *CloudAgentRequest) error {
	if req.PermissionMode == "read_only" && (req.Budget.MaxGenerationTasks != 0 || req.Budget.MaxVideoSeconds != 0) {
		return BadAuthRequest("只读模式不能设置生成预算")
	}
	if len(req.ContextScope) > 1 || (len(req.ContextScope) == 1 && req.ContextScope[0] != "canvas") {
		return BadAuthRequest("当前仅支持已保存画布摘要，其他上下文尚未开放")
	}
	if len(req.ContextScope) == 1 && req.ContextScope[0] == "canvas" {
		req.ContextScope[0] = "canvas"
	}
	if math.IsNaN(req.Budget.MaxCredits) || math.IsInf(req.Budget.MaxCredits, 0) || req.Budget.MaxCredits <= 0 || req.Budget.MaxCredits > 1000000 {
		return BadAuthRequest("本轮积分上限必须大于 0 且不超过 1000000")
	}
	return nil
}
