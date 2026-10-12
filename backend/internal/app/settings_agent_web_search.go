package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"strings"
	"time"
)

const agentWebSearchSettingKey = "agent_web_search"

type AgentWebSearchSettingRequest struct {
	Enabled          bool   `json:"enabled"`
	APIKey           string `json:"apiKey"`
	ClearAPIKey      bool   `json:"clearApiKey"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type PublicAgentWebSearchSetting struct {
	Enabled   bool       `json:"enabled"`
	HasAPIKey bool       `json:"hasApiKey"`
	Revision  int64      `json:"revision"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}
type agentWebSearchSettingValue struct {
	Enabled         bool   `json:"enabled"`
	EncryptedAPIKey string `json:"encryptedApiKey,omitempty"`
	Revision        int64  `json:"revision"`
}

func (s *Service) readAgentWebSearchSetting() (*model.SystemSetting, agentWebSearchSettingValue, error) {
	row, err := s.repo.SystemSettingOptional(agentWebSearchSettingKey)
	value := agentWebSearchSettingValue{}
	if err != nil {
		return nil, value, err
	}
	if row != nil && json.Unmarshal([]byte(row.ValueJSON), &value) != nil {
		return nil, value, BadAuthRequest("联网搜索配置格式无效")
	}
	return row, value, nil
}
func publicAgentWebSearchSetting(row *model.SystemSetting, value agentWebSearchSettingValue) *PublicAgentWebSearchSetting {
	result := &PublicAgentWebSearchSetting{Enabled: value.Enabled, HasAPIKey: value.EncryptedAPIKey != "", Revision: value.Revision}
	if row != nil {
		result.UpdatedAt = &row.UpdatedAt
	}
	return result
}
func (s *Service) AdminAgentWebSearchSetting(actor *model.User) (*PublicAgentWebSearchSetting, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	row, value, err := s.readAgentWebSearchSetting()
	if err != nil {
		return nil, err
	}
	return publicAgentWebSearchSetting(row, value), nil
}
func (s *Service) UpdateAgentWebSearchSetting(actor *model.User, input AgentWebSearchSettingRequest) (*PublicAgentWebSearchSetting, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	row, value, err := s.readAgentWebSearchSetting()
	if err != nil {
		return nil, err
	}
	if input.ExpectedRevision < 0 || input.ExpectedRevision != value.Revision {
		return nil, kernel.NewAppError(409, "联网搜索配置已修改，请重新读取后保存")
	}
	key := strings.TrimSpace(input.APIKey)
	if len(key) > 2048 || strings.ContainsAny(key, " \t\r\n") || (input.ClearAPIKey && key != "") {
		return nil, BadAuthRequest("Tavily API Key 格式无效")
	}
	if input.ClearAPIKey {
		value.EncryptedAPIKey = ""
	}
	if key != "" {
		value.EncryptedAPIKey, err = s.encryptSettingSecret(key)
		if err != nil {
			return nil, err
		}
	}
	if input.Enabled && value.EncryptedAPIKey == "" {
		return nil, BadAuthRequest("请配置 Tavily API Key 后启用联网搜索")
	}
	value.Enabled = input.Enabled
	value.Revision++
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	next := &model.SystemSetting{Key: agentWebSearchSettingKey, ValueJSON: string(data), UpdatedBy: actor.ID, UpdatedAt: time.Now()}
	previous := ""
	if row != nil {
		previous = row.ValueJSON
		next.CreatedAt = row.CreatedAt
	}
	if err := s.repo.SaveAgentWebSearchSetting(next, previous); err != nil {
		return nil, err
	}
	return publicAgentWebSearchSetting(next, value), nil
}
func (s *Service) agentWebSearchEnabled() (bool, error) {
	_, value, err := s.readAgentWebSearchSetting()
	return value.Enabled && value.EncryptedAPIKey != "", err
}
