package handler

import (
	"net/http"
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerFinanceBillingOrderRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/admin/users/:id/credits/adjust", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.AdminCreditAdjustmentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		account, err := svc.AdminAdjustCredits(user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"account": account})
	})
	r.GET("/admin/billing-orders", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, limit, err := parsePaginationQuery(c, 20)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		items, err := svc.AdminBillingOrderPage(user, service.AdminListQuery{Keyword: c.Query("keyword"), Status: c.DefaultQuery("status", "review"), Page: page, Limit: limit})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, items)
	})
	r.POST("/admin/billing-orders/batch-resolve", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.ResolveBillingBatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.ResolveBillingOrders(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/billing-orders/:id/resolve", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.ResolveBillingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		order, err := svc.ResolveBillingOrder(user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"order": order})
	})
}
