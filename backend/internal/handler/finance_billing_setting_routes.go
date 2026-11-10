package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerFinanceBillingSettingRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/settings/credits", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, err := svc.AdminCreditPolicy(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"policy": policy})
	})
	r.PATCH("/admin/settings/credits", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.CreditPolicy
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		policy, err := svc.UpdateCreditPolicy(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"policy": policy})
	})
	r.GET("/admin/settings/billing-auto-refund", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		setting, err := svc.AdminBillingAutoRefundSetting(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
	r.PATCH("/admin/settings/billing-auto-refund", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.BillingAutoRefundSetting
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		setting, err := svc.UpdateBillingAutoRefundSetting(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
}
