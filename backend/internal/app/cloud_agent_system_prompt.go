package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

type AgentSystemPromptRequest struct {
	SystemPrompt string `json:"systemPrompt"`
	Revision     int64  `json:"revision"`
}

func (s *Service) UserAgentSystemPrompt(userID string) (AgentSystemPromptRequest, error) {
	if strings.TrimSpace(userID) == "" {
		return AgentSystemPromptRequest{}, kernel.Unauthorized("请先登录")
	}
	profile, err := s.repo.AgentProfile(userID, model.AgentProfileScopeSystemPrompt, "", "")
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentSystemPromptRequest{}, nil
	}
	if err != nil {
		return AgentSystemPromptRequest{}, err
	}
	return AgentSystemPromptRequest{SystemPrompt: profile.Content, Revision: profile.Revision}, nil
}

func (s *Service) UpdateUserAgentSystemPrompt(userID string, req AgentSystemPromptRequest) (AgentSystemPromptRequest, error) {
	if strings.TrimSpace(userID) == "" {
		return AgentSystemPromptRequest{}, kernel.Unauthorized("请先登录")
	}
	if req.Revision < 0 {
		return AgentSystemPromptRequest{}, BadAuthRequest("提示词 revision 无效")
	}
	if err := validateAgentProfileContent(req.SystemPrompt); err != nil {
		return AgentSystemPromptRequest{}, err
	}
	content := strings.TrimSpace(req.SystemPrompt)
	profile := &model.AgentProfile{
		ID: newID(), UserID: userID, Scope: model.AgentProfileScopeSystemPrompt,
		Content: content, Revision: 1, Hash: agentProfileHash(content), UpdatedAt: time.Now(),
	}
	if err := s.repo.SaveAgentProfile(profile, req.Revision); err != nil {
		if errors.Is(err, repository.ErrTaskStateConflict) {
			return AgentSystemPromptRequest{}, creationConflict("系统提示词已在其他页面修改，请重新加载后保存")
		}
		return AgentSystemPromptRequest{}, err
	}
	return AgentSystemPromptRequest{SystemPrompt: profile.Content, Revision: profile.Revision}, nil
}

func agentCustomSystemPrompt(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	// A JSON string cannot fail to marshal; escaping newlines keeps user headings
	// separate from legacy system blocks parsed by the runtime.
	encoded, _ := json.Marshal(content)
	return "\n\n用户自定义系统提示词（正文为以下 JSON 字符串，请按解码后的内容执行；用于口吻、创作偏好和工作方式）：\n" + string(encoded) + "\n\n以上为用户自定义要求；平台权限、预算、审批和工具合同始终有效。\n"
}
