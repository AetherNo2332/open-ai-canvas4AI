package app

import (
	"encoding/json"
	"errors"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/outbound"
)

const desktopPrivateHostsKey = "desktop.private-model-hosts"

type desktopHTTPServiceKey struct{}
type DesktopChannelSummary struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	BaseURL      string               `json:"baseUrl"`
	Enabled      bool                 `json:"enabled"`
	Models       []model.ChannelModel `json:"models"`
	HasAPIKey    bool                 `json:"hasApiKey"`
	HasSecretKey bool                 `json:"hasSecretKey"`
	HasHeaders   bool                 `json:"hasHeaders"`
}

func (s *Service) requireDesktopOwner(actor *model.User) error {
	if !s.desktopMode || actor == nil {
		return Forbidden("仅本地主机可修改设置")
	}
	profile, err := s.repo.DesktopProfile()
	if err != nil {
		return err
	}
	if profile.UserID != actor.ID {
		return Forbidden("仅本地主机可修改设置")
	}
	return nil
}
func (s *Service) desktopHostPolicy() *outbound.HostPolicy {
	s.desktopPolicyMu.RLock()
	defer s.desktopPolicyMu.RUnlock()
	return s.desktopPolicy
}
func (s *Service) loadDesktopPrivateHosts() error {
	var hosts []string
	setting, err := s.repo.SystemSettingOptional(desktopPrivateHostsKey)
	if err != nil {
		return err
	}
	if setting != nil {
		if err = json.Unmarshal([]byte(setting.ValueJSON), &hosts); err != nil {
			return errors.New("本地模型主机授权配置损坏")
		}
	}
	policy, err := outbound.NewHostPolicy(hosts)
	if err != nil {
		return err
	}
	s.desktopPolicyMu.Lock()
	s.desktopPolicy = policy
	s.desktopPolicyMu.Unlock()
	return nil
}
func (s *Service) DesktopPrivateHosts(actor *model.User) ([]string, error) {
	if err := s.requireDesktopOwner(actor); err != nil {
		return nil, err
	}
	return s.desktopHostPolicy().Hosts(), nil
}
func (s *Service) SetDesktopPrivateHosts(actor *model.User, hosts []string) error {
	if err := s.requireDesktopOwner(actor); err != nil {
		return err
	}
	policy, err := outbound.NewHostPolicy(hosts)
	if err != nil {
		return BadAuthRequest(err.Error())
	}
	data, _ := json.Marshal(policy.Hosts())
	if err = s.repo.SaveSystemSetting(&model.SystemSetting{Key: desktopPrivateHostsKey, ValueJSON: string(data), UpdatedBy: actor.ID}); err != nil {
		return err
	}
	s.desktopPolicyMu.Lock()
	s.desktopPolicy = policy
	s.desktopPolicyMu.Unlock()
	return nil
}
func (s *Service) DesktopChannels(actor *model.User) ([]DesktopChannelSummary, error) {
	if err := s.requireDesktopOwner(actor); err != nil {
		return nil, err
	}
	channels, err := s.repo.SystemChannels(true)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopChannelSummary, 0, len(channels))
	for _, c := range channels {
		items, err := s.AdminChannelModels(actor, c.ID)
		if err != nil {
			return nil, err
		}
		rawHeaders, err := s.decryptSettingSecret(c.HeadersJSON)
		if err != nil {
			return nil, err
		}
		headers, err := ParseOutboundHeadersJSON(rawHeaders)
		if err != nil {
			return nil, err
		}
		result = append(result, DesktopChannelSummary{ID: c.ID, Name: c.Name, BaseURL: c.BaseURL, Enabled: c.Enabled, Models: items, HasAPIKey: c.APIKey != "", HasSecretKey: c.SecretKey != "", HasHeaders: len(headers) > 0})
	}
	return result, nil
}
func (s *Service) SaveDesktopChannelModel(actor *model.User, channelID, id string, req ChannelModelRequest) (*model.ChannelModel, error) {
	if err := s.requireDesktopOwner(actor); err != nil {
		return nil, err
	}
	// Preserve the original execution/catalog contract without a website wallet.
	req.PriceConfigured = true
	req.UnitPriceMicrocredits = 0
	req.InputTokenPriceMicrocredits = 0
	req.OutputTokenPriceMicrocredits = 0
	req.CachedTokenPriceMicrocredits = 0
	req.PriceTiers = nil
	req.BillingMode = "fixed_request"
	return s.SaveAdminChannelModel(actor, channelID, id, req)
}
