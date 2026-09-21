package app

import (
	"encoding/json"
	"strings"
)

// Keep cross-field validation server-side rather than adding provider-specific
// root schema combinators. The same contract is advertised by both media tools.
const cloudAgentModelSelectionDescription = "模型选择必填：优先原样复制 model_list 返回的 selectionId（服务端签发的一次性凭证，不要修改其中任何字符）。兼容期也接受把 selection 展开成顶层字段：非空 logicalModelId，或同时提供非空 channelId 和 channelModelKey。selectionId 与展开字段互斥，未使用的选择字段省略或传空字符串，不得传 null 或仅含空白的字符串。缺失、混用、被改写或不完整均在提交前拒绝，不会自动选择或切换模型。"

// validateCloudAgentModelSelection 校验并**归一化**模型选择：优先解开 selectionId，
// 否则沿用旧的三字段契约。归一化后下游代码只看到三个字段，不需要知道凭证存在。
func (s *Service) validateCloudAgentModelSelection(userID, raw string, a *cloudAgentMediaArgs) error {
	// Go's JSON decoder accepts null for string fields; the tool contract does
	// not. Check presence/type before interpreting the decoded selection.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return cloudAgentJSONArgumentError(err)
	}
	if value, exists := fields["selectionId"]; exists {
		var text *string
		if err := json.Unmarshal(value, &text); err != nil || text == nil {
			return cloudAgentFieldError("selectionId", "type_mismatch", "selectionId 必须是字符串，不能为 null")
		}
	}
	for _, field := range []string{"logicalModelId", "channelId", "channelModelKey"} {
		if value, exists := fields[field]; exists {
			var text *string
			if err := json.Unmarshal(value, &text); err != nil || text == nil {
				return cloudAgentFieldError(field, "type_mismatch", "模型选择字段必须是字符串，不能为 null")
			}
			if *text != "" && strings.TrimSpace(*text) == "" {
				return cloudAgentFieldError(field, "invalid_value", "模型选择字段不能仅包含空白字符")
			}
		}
	}
	if a.SelectionID != "" {
		if a.LogicalModelID != "" || a.ChannelID != "" || a.ChannelModelKey != "" {
			return cloudAgentFieldError("selectionId", "mutually_exclusive", "模型选择冲突：selectionId 与 logicalModelId/channelId/channelModelKey 不得混用，只能给一种")
		}
		selection, err := s.resolveCloudAgentSelectionID(userID, a.SelectionID)
		if err != nil {
			return err
		}
		a.LogicalModelID, a.ChannelID, a.ChannelModelKey = selection.LogicalModelID, selection.ChannelID, selection.ChannelModelKey
		return nil
	}
	switch {
	case a.LogicalModelID != "" && (a.ChannelID != "" || a.ChannelModelKey != ""):
		return cloudAgentFieldError("logicalModelId", "mutually_exclusive", "模型选择冲突：logicalModelId 与 channelId/channelModelKey 不得混用，请复制 model_list 的一种 selection")
	case a.LogicalModelID != "":
		return nil
	case a.ChannelID == "" && a.ChannelModelKey == "":
		return cloudAgentFieldError("logicalModelId", "required", "缺少模型选择：必须复制 model_list 的 logicalModelId 或完整的 channelId/channelModelKey，不会自动选择模型")
	case a.ChannelID == "":
		return cloudAgentFieldError("channelId", "required", "系统渠道模型选择不完整：缺少 channelId，请复制 model_list 的完整 selection")
	case a.ChannelModelKey == "":
		return cloudAgentFieldError("channelModelKey", "required", "系统渠道模型选择不完整：缺少 channelModelKey，请复制 model_list 的完整 selection")
	default:
		return nil
	}
}
