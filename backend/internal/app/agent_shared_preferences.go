package app

import (
	"errors"
	"math"
	"regexp"
	"strings"

	"golang.org/x/text/language"
	"gorm.io/gorm"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type AgentSharedPreferences = model.AgentSharedPreferences

var agentPreferenceLanguage = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z]{4})?(-[a-zA-Z]{2}|-[0-9]{3})?$`)

func (s *Service) AgentSharedPreferences(userID string) (AgentSharedPreferences, error) {
	if strings.TrimSpace(userID) == "" {
		return AgentSharedPreferences{}, kernel.Unauthorized("请先登录")
	}
	prefs, err := s.repo.AgentSharedPreferences(userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentSharedPreferences{}, nil
	}
	return prefs, err
}

func (s *Service) UpdateAgentSharedPreferences(userID string, prefs AgentSharedPreferences) (AgentSharedPreferences, error) {
	if strings.TrimSpace(userID) == "" {
		return AgentSharedPreferences{}, kernel.Unauthorized("请先登录")
	}
	if prefs.Revision < 0 || prefs.Revision == math.MaxInt64 {
		return AgentSharedPreferences{}, BadAuthRequest("共享偏好 revision 无效")
	}
	if prefs.Language != "" {
		tag, err := language.Parse(prefs.Language)
		if !agentPreferenceLanguage.MatchString(prefs.Language) || err != nil {
			return AgentSharedPreferences{}, BadAuthRequest("语言需使用有效的语言标签，例如 zh-CN")
		}
		prefs.Language = tag.String()
	}
	if !validAgentPreference(prefs.Tone, "neutral", "professional", "casual") ||
		!validAgentPreference(prefs.Verbosity, "concise", "balanced", "detailed") ||
		!validAgentPreference(prefs.Format, "auto", "prose", "bullets") {
		return AgentSharedPreferences{}, BadAuthRequest("共享偏好的语气、详略或格式无效")
	}
	saved, err := s.repo.SaveAgentSharedPreferences(userID, prefs)
	if errors.Is(err, repository.ErrTaskStateConflict) {
		conflict := kernel.NewAppError(409, "共享偏好已在其他页面修改，请重新加载后保存")
		conflict.Reason = "agent_preferences_conflict"
		return AgentSharedPreferences{}, conflict
	}
	return saved, err
}

func validAgentPreference(value string, options ...string) bool {
	if value == "" {
		return true
	}
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
