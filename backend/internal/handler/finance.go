package handler

import (
	"net/http"
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func RegisterFinanceRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerWalletRoutes(r, svc)
	registerFinanceIdentitySettingRoutes(r, svc)
	registerFinanceBillingSettingRoutes(r, svc)
	registerFinanceEmailSettingRoutes(r, svc)
	registerFinanceModelReadRoutes(r, svc)
	registerFinanceModelImportRoutes(r, svc)
	registerFinanceModelBatchRoutes(r, svc)
	registerFinanceModelMutationRoutes(r, svc)
	registerFinanceRedemptionRoutes(r, svc)
	registerFinanceBillingOrderRoutes(r, svc)
}

func saveChannelModel(c *gin.Context, svc *service.Service, id string) {
	user, err := currentUser(c, svc)
	if err != nil {
		failService(c, err)
		return
	}
	var req service.ChannelModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	item, err := svc.SaveAdminChannelModel(user, c.Param("id"), id, req)
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"model": adminChannelModel(*item)})
}
