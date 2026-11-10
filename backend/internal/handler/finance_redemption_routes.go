package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
	"yingce/backend/internal/service"
)

func registerFinanceRedemptionRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/redeem-batches", func(c *gin.Context) {
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
		items, err := svc.AdminRedeemBatchPage(user, service.AdminListQuery{Keyword: c.Query("keyword"), Status: c.Query("validity"), Page: page, Limit: limit})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, items)
	})
	r.POST("/admin/redeem-batches", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceRateLimit(c, "redeem-batch-create:"+user.ID, 20, time.Hour) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.CreateRedeemBatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.AdminCreateRedeemBatch(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		ok(c, result)
	})
	r.GET("/admin/redeem-batches/:id/codes", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, limit, err := parsePaginationQuery(c, 50)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.AdminRedeemCodePage(user, c.Param("id"), c.Query("status"), page, limit)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		ok(c, result)
	})
	r.POST("/admin/redeem-batches/:id/disable", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		count, err := svc.AdminDisableRedeemBatch(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"disabledCount": count})
	})
	r.POST("/admin/redeem-batches/:id/codes/:codeId/disable", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.AdminDisableRedeemCode(user, c.Param("id"), c.Param("codeId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})
}
