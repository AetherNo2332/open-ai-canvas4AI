package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerArkPortraitSessionRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/ark-portraits", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		overview, err := svc.ArkPortraitOverview(c.Request.Context(), user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, overview)
	})
	r.POST("/ark-portraits/sessions", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "ark-portrait-session:"+user.ID) {
			return
		}
		var req struct {
			Name string `json:"name"`
			Lang string `json:"lang"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		session, err := svc.CreateArkPortraitSession(c.Request.Context(), user, req.Name, req.Lang)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"session": session})
	})
	r.GET("/ark-portraits/sessions/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		session, err := svc.ArkPortraitSessionStatus(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"session": session})
	})
	r.POST("/ark-portraits/sessions/:id/check", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "ark-portrait-check:"+user.ID) {
			return
		}
		session, err := svc.CheckArkPortraitSession(c.Request.Context(), user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"session": session})
	})
	r.POST("/ark-portraits/sessions/:id/cancel", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		session, err := svc.CancelArkPortraitSession(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"session": session})
	})
	// 认证页完成后由终端用户浏览器打开（多为未登录的手机），只凭一次性 state 识别会话。
	r.GET("/ark-portraits/callback/:state", func(c *gin.Context) {
		if !enforceArkPortraitRate(c, svc, "ark-portrait-callback:"+c.ClientIP()) {
			return
		}
		target := svc.HandleArkPortraitCallback(c.Request.Context(), c.Param("state"), c.Request.URL.Query())
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Redirect(http.StatusFound, target)
	})
}
