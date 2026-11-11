package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerAuthRegistrationRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/auth/register", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "register:"+c.ClientIP(), policy.Request.RegisterPerHour, time.Hour) {
			return
		}
		req.LoginContext = loginRequestContext(c)
		result, err := svc.Register(req)
		if err != nil {
			failService(c, err)
			return
		}
		setSessionCookie(c, result.Session, result.MaxAgeSecs)
		ok(c, gin.H{"user": result.User})
	})
	r.POST("/auth/email-code", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req struct {
			Email string `json:"email"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "email-code:"+c.ClientIP(), policy.Request.EmailCodePerHour, time.Hour) {
			return
		}
		if !enforceRateLimit(c, "registration-email-account:"+passwordResetRateLimitSubject(req.Email), 10, time.Hour) {
			return
		}
		if err := svc.SendRegistrationEmailCode(req.Email); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"sent": true})
	})
	r.POST("/auth/password-reset-code", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req struct {
			Email string `json:"email"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "password-reset-code-ip:"+c.ClientIP(), policy.Request.EmailCodePerHour, time.Hour) {
			return
		}
		if !enforceRateLimit(c, "password-reset-code-account:"+passwordResetRateLimitSubject(req.Email), policy.Request.EmailCodePerHour, time.Hour) {
			return
		}
		if err := svc.SendPasswordResetEmailCode(req.Email); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"sent": true})
	})
	r.POST("/auth/password-reset", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.PasswordResetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "password-reset-ip:"+c.ClientIP(), policy.Request.LoginIPPerTenMinutes, 10*time.Minute) {
			return
		}
		if !enforceRateLimit(c, "password-reset-account:"+passwordResetRateLimitSubject(req.Email), policy.Request.LoginAccountPerTenMinutes, 10*time.Minute) {
			return
		}
		if err := svc.ResetPassword(req); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"reset": true})
	})
}
