package app

import (
	"fmt"
	"strings"
)

func cloudAgentCanonicalImageKeys(canonical *canonicalAgentRequest) []string {
	var keys []string
	for _, message := range canonical.Messages {
		parts, _ := message["content"].([]any)
		for _, value := range parts {
			part, _ := value.(map[string]any)
			if stringField(part, "type") == "image_url" {
				image, _ := part["image_url"].(map[string]any)
				keys = append(keys, stringField(image, "url"))
			}
		}
	}
	return keys
}

func (s *Service) cloudAgentImageReferences(userID string, req CloudAgentRequest, canonical *canonicalAgentRequest, isolated ...bool) ([]providerMedia, error) {
	var authorized map[string]bool
	if len(isolated) > 0 && isolated[0] && len(cloudAgentCanonicalImageKeys(canonical)) > 0 {
		var err error
		authorized, err = s.agentScopedImageKeys(userID, agentBusinessScope(req))
		if err != nil {
			return nil, err
		}
	}
	return s.cloudAgentAuthorizedImageReferences(userID, req, canonical, authorized)
}

func (s *Service) cloudAgentAuthorizedImageReferences(userID string, req CloudAgentRequest, canonical *canonicalAgentRequest, authorized map[string]bool) ([]providerMedia, error) {
	keys := cloudAgentCanonicalImageKeys(canonical)
	if len(keys) == 0 {
		return nil, nil
	}
	limits, err := s.cloudAgentVisionReferences(req)
	if err != nil {
		return nil, err
	}
	if len(keys) > limits.MaxImages {
		return nil, BadAuthRequest(fmt.Sprintf("上下文包含 %d 张图片，当前模型最多支持 %d 张；原始图片不会被裁剪或移出，请选择支持更多图片的模型", len(keys), limits.MaxImages))
	}
	refs := make([]providerMedia, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		if !strings.HasPrefix(key, "resource:") {
			return nil, BadAuthRequest("看图记录不是账号资源引用，请重新发起本轮对话")
		}
		if authorized != nil && !authorized[key] {
			return nil, BadAuthRequest("当前业务未授权图片，请重新读取当前画布中绑定的图片")
		}
		resource, err := s.repo.ResourceForUser(userID, strings.TrimPrefix(key, "resource:"))
		if err != nil || resource.Status != "ready" || !strings.HasPrefix(strings.ToLower(resource.MimeType), "image/") {
			return nil, BadAuthRequest("看图资源不可用、尚未就绪或不属于当前用户")
		}
		if err := validateCloudAgentImageResourceSize(resource.Size, limits.MaxImageBytes); err != nil {
			return nil, err
		}
		refs = append(refs, providerMedia{StorageKey: key, MimeType: resource.MimeType, Bytes: resource.Size, Width: resource.Width, Height: resource.Height})
		seen[key] = true
	}
	return refs, nil
}

func (s *Service) agentScopedImageKeys(userID string, scope AgentBusinessScope) (map[string]bool, error) {
	authorized := map[string]bool{}
	if scope.Type != "canvas" {
		return authorized, nil
	}
	canvas, err := s.repo.CanvasProjectForUser(userID, scope.ID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	for _, node := range creationMaps(doc["nodes"]) {
		meta, _ := node["metadata"].(map[string]any)
		if key := stringValue(meta["storageKey"]); strings.HasPrefix(key, "resource:") {
			authorized[key] = true
		}
		if cloudAgentCharacterNode(node) {
			ref, _, err := cloudAgentMediaReference(s.repo, userID, canvas.ProjectID, node)
			if err == nil {
				authorized[stringValue(ref["storageKey"])] = true
			}
		}
	}
	return authorized, nil
}
