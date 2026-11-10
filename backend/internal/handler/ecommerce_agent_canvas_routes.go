package handler

import (
	"errors"
	"net/http"
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/service"
)

func registerEcommerceAgentCanvasRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/canvas/placeholder", func(c *gin.Context) {
		if _, err := currentUser(c, svc); err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Count       int    `json:"count"`
			ImageCount  int    `json:"imageCount"`
			AspectRatio string `json:"aspectRatio"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		count := req.Count
		if count == 0 {
			count = req.ImageCount
		}
		if count < 1 || count > 15 {
			fail(c, http.StatusBadRequest, errors.New("占位页数须为 1 至 15"))
			return
		}
		if req.AspectRatio == "" {
			req.AspectRatio = "1:1"
		}
		ok(c, ecommerceagent.Placeholder(count, req.AspectRatio))
	})
	r.POST("/canvas/save", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		var req struct {
			ProjectID    string                      `json:"projectId"`
			PosterCanvas ecommerceagent.PosterCanvas `json:"posterCanvas"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		version, err := svc.SaveEcommerceAgentCanvas(user.ID, req.ProjectID, req.PosterCanvas)
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, version)
	})
	r.GET("/canvas/:projectId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.EcommerceAgentCanvas(user.ID, c.Param("projectId"))
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, view)
	})
}
