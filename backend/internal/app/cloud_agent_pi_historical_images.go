package app

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/model"
)

func (s *Service) piModelStepImageReferences(run *model.CloudAgentExecution, state *cloudAgentRuntime, canonical *canonicalAgentRequest) ([]providerMedia, error) {
	if state.VisionRequestTail {
		return s.piProjectVisionTail(run, state, canonical)
	}
	return s.piLegacyModelStepImageReferences(run, state, canonical)
}

func (s *Service) piLegacyModelStepImageReferences(run *model.CloudAgentExecution, state *cloudAgentRuntime, canonical *canonicalAgentRequest) ([]providerMedia, error) {
	keys := cloudAgentCanonicalImageKeys(canonical)
	if len(keys) == 0 {
		return nil, nil
	}
	var authorized map[string]bool
	if state.Context != nil {
		scope := agentBusinessScope(state.Request)
		var err error
		authorized, err = s.agentScopedImageKeys(run.UserID, scope)
		if err != nil {
			return nil, err
		}
		if scope.Type == "canvas" && run.ParentID != "" && piHasUnboundImage(keys, authorized) {
			historical, err := s.piHistoricalCanvasImages(run)
			if err != nil {
				return nil, err
			}
			*canonical = projectUnboundPiHistoricalImages(*canonical, historical, authorized)
		}
	}
	budget := state.VisionBatchBudget
	if budget.MaxImages == 0 && budget.MaxCost == 0 {
		if configured, err := s.cloudAgentVisionBatchBudget(state.Request); err == nil {
			budget = configured
		}
	}
	*canonical, _ = projectCloudAgentVisionRequest(*canonical, budget, state.DeliveredImageSHAs, state.DeliveredImageComparisons)
	return s.cloudAgentAuthorizedImageReferences(run.UserID, state.Request, canonical, authorized)
}

func piHasUnboundImage(keys []string, authorized map[string]bool) bool {
	for _, key := range keys {
		if strings.HasPrefix(key, "resource:") && !authorized[key] {
			return true
		}
	}
	return false
}

type piHistoricalImage struct {
	key     string
	caption string
}

func piHistoryImageAt(parts []any, index int) piHistoricalImage {
	part, _ := parts[index].(map[string]any)
	if index == 0 || stringValue(part["type"]) != "image_url" {
		return piHistoricalImage{}
	}
	image, _ := part["image_url"].(map[string]any)
	key := stringValue(image["url"])
	captionPart, _ := parts[index-1].(map[string]any)
	caption := stringValue(captionPart["text"])
	if !strings.HasPrefix(key, "resource:") || stringValue(captionPart["type"]) != "text" ||
		stringValue(cloudAgentImageReceipt(caption)["nodeId"]) == "" {
		return piHistoricalImage{}
	}
	return piHistoricalImage{key: key, caption: caption}
}

func (s *Service) piHistoricalCanvasImages(run *model.CloudAgentExecution) (map[piHistoricalImage]bool, error) {
	session, entries, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
	if err != nil {
		return nil, err
	}
	if session.AgentConversationBinding != run.AgentConversationBinding || session.CanvasID != run.CanvasID {
		return nil, BadAuthRequest("Pi 会话与当前业务范围不一致")
	}
	branch, err := cloudAgentPiActiveBranch(entries, session.ActiveLeafID)
	if err != nil {
		return nil, err
	}
	owners := make(map[string]string, len(entries))
	for _, entry := range entries {
		owners[entry.EntryID] = entry.RunID
	}
	historical := map[piHistoricalImage]bool{}
	for _, entry := range branch {
		if entry.Type != "message" || !strings.Contains(string(entry.Message), "image_url") {
			continue
		}
		var message struct {
			Role          string `json:"role"`
			CanvasContent []any  `json:"canvasContent"`
		}
		if err := json.Unmarshal(entry.Message, &message); err != nil {
			return nil, err
		}
		if message.Role != "user" {
			continue
		}
		for index := range message.CanvasContent {
			if image := piHistoryImageAt(message.CanvasContent, index); image.key != "" {
				historical[image] = owners[entry.ID] != "" && owners[entry.ID] != run.ID
			}
		}
	}
	return historical, nil
}

func projectUnboundPiHistoricalImages(canonical canonicalAgentRequest, historical map[piHistoricalImage]bool, authorized map[string]bool) canonicalAgentRequest {
	canonical.Messages = append([]map[string]any(nil), canonical.Messages...)
	for messageIndex, message := range canonical.Messages {
		parts, _ := message["content"].([]any)
		if stringValue(message["role"]) != "user" || len(parts) == 0 {
			continue
		}
		var projected []any
		for index := range parts {
			image := piHistoryImageAt(parts, index)
			if !historical[image] || authorized[image.key] {
				continue
			}
			if projected == nil {
				projected = append([]any(nil), parts...)
			}
			projected[index] = map[string]any{"type": "text", "text": "这张历史图片已不再绑定于当前画布，未发送原图；需要图片时请重新读取当前节点。"}
		}
		if projected != nil {
			copy := cloneStringAnyMap(message)
			copy["content"] = projected
			canonical.Messages[messageIndex] = copy
		}
	}
	return canonical
}
