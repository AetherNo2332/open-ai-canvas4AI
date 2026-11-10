package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/service"
)

func registerEcommerceAgentPlanningRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/agent/quote", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req ecommerceagent.Request
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		quote, err := svc.QuoteEcommerceAgent(user.ID, req)
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, quote)
	})
	r.POST("/agent/analyze", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req ecommerceagent.Request
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.AnalyzeEcommerceAgent(c.Request.Context(), user.ID, req)
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/agent/recommend", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req ecommerceagent.Request
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.AnalyzeEcommerceAgent(c.Request.Context(), user.ID, req)
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, gin.H{"brief": result.Brief, "recommendations": result.Recommendations, "styleLibrary": result.StyleLibrary})
	})
	r.POST("/agent/help-write", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var req struct {
			Text     string `json:"text"`
			Platform string `json:"platform"`
			Language string `json:"language"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.HelpWriteEcommerceAgent(c.Request.Context(), user.ID, req.Text, req.Platform, req.Language)
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/agent/generate", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req ecommerceagent.Request
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.GenerateEcommerceAgent(c.Request.Context(), user.ID, req, func(ecommerceagent.Event) error { return nil })
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, view)
	})
}
