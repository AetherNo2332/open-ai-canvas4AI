package app

import (
	"errors"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// Admission and queued messages use the same business ownership boundary.
func (s *Service) agentBusinessOwner(userID string, req CloudAgentRequest) (*model.CanvasProject, error) {
	definition, err := s.ResolveAgentDefinition(agentBusinessScope(req).Type, req.AgentDefinitionID)
	if err != nil {
		return nil, err
	}
	return s.authorizeAgentDefinition(definition, userID, req)
}

func (s *Service) authorizeAgentDefinition(definition AgentDefinition, userID string, req CloudAgentRequest) (*model.CanvasProject, error) {
	if definition.ReadOnly && req.PermissionMode != "read_only" {
		return nil, kernel.Forbidden("该 Agent 配置仅允许只读运行")
	}
	if !definition.AllowUserSkills && len(req.SkillIDs) > 0 {
		return nil, kernel.Forbidden("该 Agent 配置不允许选择账户技能")
	}
	return definition.authorize(s, userID, req)
}

func agentBuiltinObjectAuthorizer(businessType string) agentObjectAuthorizer {
	switch businessType {
	case "canvas":
		return (*Service).authorizeCanvasAgent
	case "drama":
		return (*Service).authorizeDramaAgent
	case AgentPlatformBusinessType:
		return (*Service).authorizePlatformAgent
	default:
		return nil
	}
}

func (s *Service) authorizeDramaAgent(userID string, req CloudAgentRequest) (*model.CanvasProject, error) {
	scope := agentBusinessScope(req)
	_, _, err := s.dramaForUser(userID, scope.ID)
	if err == nil && req.DramaEpisodeID != "" {
		_, err = s.repo.DramaEpisode(scope.ID, req.DramaEpisodeID)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = kernel.NotFound("短剧项目不存在")
	}
	return nil, err
}

func (s *Service) authorizeCanvasAgent(userID string, req CloudAgentRequest) (*model.CanvasProject, error) {
	canvas, err := s.repo.CanvasProjectForUser(userID, agentBusinessScope(req).ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = kernel.NotFound("画布不存在或尚未保存到服务端，请先完成画布同步")
	}
	return canvas, err
}

// Platform conversations are account-owned, not object-owned. The account ID
// comes exclusively from the authenticated request and never from client data.
func (s *Service) authorizePlatformAgent(userID string, req CloudAgentRequest) (*model.CanvasProject, error) {
	scope := agentBusinessScope(req)
	if strings.TrimSpace(userID) == "" || scope.Type != AgentPlatformBusinessType || scope.ID != userID {
		return nil, kernel.NotFound("平台 Agent 会话不存在")
	}
	return nil, nil
}
