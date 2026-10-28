package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

type AgentSystemPromptRequest struct {
	SystemPrompt string `json:"systemPrompt"`
	Revision     int64  `json:"revision"`
}

const agentSystemPromptMaxRunes = 12000

func validateAgentSystemPromptContent(content string) error {
	if !utf8.ValidString(content) || utf8.RuneCountInString(content) > agentSystemPromptMaxRunes {
		return BadAuthRequest("Agent 系统提示词必须是有效 UTF-8，且不超过 12000 个字符")
	}
	for _, r := range content {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return BadAuthRequest("Agent 系统提示词不能包含控制字符")
		}
	}
	return nil
}

func agentContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
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
	if err := validateAgentSystemPromptContent(req.SystemPrompt); err != nil {
		return AgentSystemPromptRequest{}, err
	}
	content := strings.TrimSpace(req.SystemPrompt)
	profile := &model.AgentProfile{
		ID: newID(), UserID: userID, Scope: model.AgentProfileScopeSystemPrompt,
		Content: content, Revision: 1, Hash: agentContentHash(content), UpdatedAt: time.Now(),
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
