package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func RegisterEcommerceRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerEcommerceSkillInstallRoutes(r, svc)
	registerEcommerceSkillCatalogRoutes(r, svc)
	registerEcommerceTaskSettingRoutes(r, svc)
	registerEcommerceOrchestrationRoutes(r, svc)
	registerEcommerceCreativeRoutes(r, svc)
	registerEcommerceKnowledgeRoutes(r, svc)
	registerEcommerceSkillPromptRoutes(r, svc)
	registerEcommerceProjectRoutes(r, svc)
	registerEcommerceRoundRoutes(r, svc)
}
