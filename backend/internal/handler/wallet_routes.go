package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
	"yingce/backend/internal/service"
)

func registerWalletRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/wallet", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, limit, err := parsePaginationQuery(c, 30)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		wallet, err := svc.Wallet(user, c.Query("type"), page, limit)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, wallet)
	})
	r.POST("/wallet/redeem", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceRateLimit(c, "redeem:"+user.ID, 10, time.Hour) {
			return
		}
		var req struct {
			Code string `json:"code"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		account, err := svc.RedeemCredits(user, req.Code, c.ClientIP())
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"account": account})
	})
	r.POST("/wallet/checkin", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		account, granted, err := svc.CheckinCredits(user)
		if err != nil {
			failService(c, err)
			return
		}
		if !granted {
			fail(c, http.StatusConflict, service.BadAuthRequest("今天已经签到过了"))
			return
		}
		ok(c, gin.H{"account": account, "granted": true})
	})
}
