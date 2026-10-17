package platform

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"infinite-canvas/backend/internal/kernel"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

const featureAvailabilitySettingKey = "feature_availability"

const (
	FeatureShortDrama            = "shortDrama"
	FeatureTaskCenter            = "taskCenter"
	FeatureCredits               = "credits"
	FeatureCustomChannels        = "customChannels"
	FeatureFrontendModels        = "frontendModels"
	FeaturePluginCenter          = "pluginCenter"
	FeatureSystemPlugins         = "systemPluginsVisibleToUsers"
	FeatureTimelineTranscription = "timelineTranscription"
	FeatureAgentSubagents        = "agentSubagents"
)

const (
	DirectorDeskPrevisOff    = "off"
	DirectorDeskPrevisCanary = "canary"
	DirectorDeskPrevisOn     = "on"
)

type FeatureAvailability struct {
	WelcomeEnabled               bool   `json:"welcomeEnabled"`
	ShortDramaEnabled            bool   `json:"shortDramaEnabled"`
	TaskCenterEnabled            bool   `json:"taskCenterEnabled"`
	CreditsEnabled               bool   `json:"creditsEnabled"`
	CustomChannelsEnabled        bool   `json:"customChannelsEnabled"`
	FrontendModelsEnabled        bool   `json:"frontendModelsEnabled"`
	PluginCenterEnabled          bool   `json:"pluginCenterEnabled"`
	SystemPluginsVisibleToUsers  bool   `json:"systemPluginsVisibleToUsers"`
	TimelineTranscriptionEnabled bool   `json:"timelineTranscriptionEnabled"`
	AgentSubagentsEnabled        bool   `json:"agentSubagentsEnabled"`
	DirectorDeskPrevisMode       string `json:"directorDeskPrevisMode"`
	// canary 分组:mode=canary 时生效。内部账号白名单 + 稳定哈希比例(万分比,0-10000)。
	DirectorDeskPrevisCanaryUsers       []string `json:"directorDeskPrevisCanaryUsers"`
	DirectorDeskPrevisCanaryBasisPoints int      `json:"directorDeskPrevisCanaryBasisPoints"`
}

type PublicFeatureAvailability struct {
	FeatureAvailability
	Configured bool      `json:"configured"`
	UpdatedBy  string    `json:"updatedBy,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt,omitempty"`
}

func DefaultFeatureAvailability() FeatureAvailability {
	// 缺少配置代表尚未由运维接管；前台模型需要明确配置后才开放。
	return FeatureAvailability{
		WelcomeEnabled:               true,
		ShortDramaEnabled:            true,
		TaskCenterEnabled:            true,
		CreditsEnabled:               true,
		CustomChannelsEnabled:        true,
		FrontendModelsEnabled:        false,
		PluginCenterEnabled:          true,
		SystemPluginsVisibleToUsers:  true,
		TimelineTranscriptionEnabled: true,
		AgentSubagentsEnabled:        false,
		DirectorDeskPrevisMode:       DirectorDeskPrevisOff,
	}
}

func (s *Service) FeatureAvailability() (*PublicFeatureAvailability, error) {
	setting, value, err := s.readFeatureAvailability()
	if err != nil {
		return nil, err
	}
	return publicFeatureAvailability(setting, value), nil
}

// UserFeatureAvailability 是登录用户视角的可用性:导演预演台模式按用户灰度
// 分群(白名单/比例桶)解析,其余开关保持全局值。管理端接口仍读全局原始值。
func (s *Service) UserFeatureAvailability(user *model.User) (*PublicFeatureAvailability, error) {
	setting, value, err := s.readFeatureAvailability()
	if err != nil {
		return nil, err
	}
	value.DirectorDeskPrevisMode = ResolveDirectorDeskPrevisMode(
		value.DirectorDeskPrevisMode,
		value.DirectorDeskPrevisCanaryUsers,
		value.DirectorDeskPrevisCanaryBasisPoints,
		user.ID,
	)
	return publicFeatureAvailability(setting, value), nil
}

func (s *Service) AdminFeatureAvailability(actor *model.User) (*PublicFeatureAvailability, error) {
	if err := s.requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.FeatureAvailability()
}

func (s *Service) UpdateFeatureAvailability(actor *model.User, value FeatureAvailability) (*PublicFeatureAvailability, error) {
	if err := s.requireAdmin(actor); err != nil {
		return nil, err
	}
	current, before, err := s.readFeatureAvailability()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	setting := model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: string(encoded), UpdatedBy: actor.ID}
	if current != nil {
		setting.CreatedAt = current.CreatedAt
	}
	if err := s.repo.SaveSystemSetting(&setting); err != nil {
		return nil, err
	}
	if err := s.appendAdminAudit(actor, "feature_availability.update", "system_setting", featureAvailabilitySettingKey, "更新功能开放配置", map[string]any{"before": before, "after": value}); err != nil {
		return nil, err
	}
	return publicFeatureAvailability(&setting, value), nil
}

func (s *Service) FeatureEnabled(feature string) (bool, error) {
	_, value, err := s.readFeatureAvailability()
	if err != nil {
		return false, err
	}
	switch feature {
	case FeatureShortDrama:
		return value.ShortDramaEnabled, nil
	case FeatureTaskCenter:
		return value.TaskCenterEnabled, nil
	case FeatureCredits:
		return value.CreditsEnabled, nil
	case FeatureCustomChannels:
		return value.CustomChannelsEnabled, nil
	case FeatureFrontendModels:
		return value.FrontendModelsEnabled, nil
	case FeaturePluginCenter:
		return value.PluginCenterEnabled, nil
	case FeatureSystemPlugins:
		return value.SystemPluginsVisibleToUsers, nil
	case FeatureTimelineTranscription:
		return value.TimelineTranscriptionEnabled, nil
	case FeatureAgentSubagents:
		return value.AgentSubagentsEnabled, nil
	default:
		return false, errors.New("未知功能开放配置")
	}
}

func (s *Service) RequireFeature(feature string) error {
	enabled, err := s.FeatureEnabled(feature)
	if err != nil {
		return err
	}
	if enabled {
		return nil
	}
	switch feature {
	case FeatureShortDrama:
		return kernel.Forbidden("短剧创作暂未开放")
	case FeatureTaskCenter:
		return kernel.Forbidden("任务中心暂未开放")
	case FeatureCredits:
		return kernel.Forbidden("积分功能暂未开放")
	case FeatureCustomChannels:
		return kernel.Forbidden("自定义渠道暂未开放")
	case FeatureFrontendModels:
		return kernel.Forbidden("前台模型目录暂未开放")
	case FeaturePluginCenter:
		return kernel.Forbidden("插件中心暂未开放")
	case FeatureSystemPlugins:
		return kernel.Forbidden("系统插件暂未向普通用户展示")
	case FeatureTimelineTranscription:
		return kernel.Forbidden("字幕转写暂未开放")
	case FeatureAgentSubagents:
		return kernel.Forbidden("动态子代理暂未开放")
	default:
		return kernel.Forbidden("该功能暂未开放")
	}
}

func (s *Service) readFeatureAvailability() (*model.SystemSetting, FeatureAvailability, error) {
	setting, err := s.repo.SystemSetting(featureAvailabilitySettingKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, DefaultFeatureAvailability(), nil
	}
	if err != nil {
		return nil, FeatureAvailability{}, err
	}
	// 以全开放默认值为基底，避免已有三字段配置在升级后因缺少新字段而意外关闭功能。
	value := DefaultFeatureAvailability()
	if strings.TrimSpace(setting.ValueJSON) == "" || json.Unmarshal([]byte(setting.ValueJSON), &value) != nil {
		return nil, FeatureAvailability{}, errors.New("功能开放配置格式无效")
	}
	if !validDirectorDeskPrevisMode(value.DirectorDeskPrevisMode) {
		value.DirectorDeskPrevisMode = DirectorDeskPrevisOff
	}
	value.normalizeDirectorDeskPrevisCanary()
	return setting, value, nil
}

func validDirectorDeskPrevisMode(mode string) bool {
	return mode == DirectorDeskPrevisOff || mode == DirectorDeskPrevisCanary || mode == DirectorDeskPrevisOn
}

// ResolveDirectorDeskPrevisMode 按用户解析导演预演台模式(灰度分群的唯一真源):
// on → 全量;off/非法 → 全量关;canary → 白名单用户 + 稳定哈希万分比桶。
// 空用户(未登录)不进入比例桶,匿名体验永远保守。
func ResolveDirectorDeskPrevisMode(mode string, canaryUsers []string, canaryBasisPoints int, userID string) string {
	if mode == DirectorDeskPrevisOn {
		return DirectorDeskPrevisOn
	}
	if mode != DirectorDeskPrevisCanary {
		return DirectorDeskPrevisOff
	}
	id := strings.TrimSpace(userID)
	if id == "" {
		return DirectorDeskPrevisOff
	}
	for _, entry := range canaryUsers {
		if strings.TrimSpace(entry) == id {
			return DirectorDeskPrevisCanary
		}
	}
	if canaryBasisPoints <= 0 {
		return DirectorDeskPrevisOff
	}
	if canaryBasisPoints > 10000 {
		canaryBasisPoints = 10000
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte("director-desk-previs:" + id))
	if h.Sum32()%10000 < uint32(canaryBasisPoints) {
		return DirectorDeskPrevisCanary
	}
	return DirectorDeskPrevisOff
}

// 归一化 canary 分组参数:trim 白名单、剔除空项、限制比例到 [0,10000]。
func (v *FeatureAvailability) normalizeDirectorDeskPrevisCanary() {
	users := make([]string, 0, len(v.DirectorDeskPrevisCanaryUsers))
	for _, entry := range v.DirectorDeskPrevisCanaryUsers {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			users = append(users, trimmed)
		}
	}
	v.DirectorDeskPrevisCanaryUsers = users
	if v.DirectorDeskPrevisCanaryBasisPoints < 0 {
		v.DirectorDeskPrevisCanaryBasisPoints = 0
	}
	if v.DirectorDeskPrevisCanaryBasisPoints > 10000 {
		v.DirectorDeskPrevisCanaryBasisPoints = 10000
	}
}

func publicFeatureAvailability(setting *model.SystemSetting, value FeatureAvailability) *PublicFeatureAvailability {
	result := &PublicFeatureAvailability{FeatureAvailability: value, Configured: setting != nil}
	if setting != nil {
		result.UpdatedBy = setting.UpdatedBy
		result.UpdatedAt = setting.UpdatedAt
	}
	return result
}

// PublicFeatureAvailability projects the persisted feature flags without
// exposing the platform service internals to compatibility callers.
func ProjectFeatureAvailability(setting *model.SystemSetting, value FeatureAvailability) *PublicFeatureAvailability {
	return publicFeatureAvailability(setting, value)
}
