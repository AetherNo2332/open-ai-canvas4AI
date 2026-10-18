package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
	"net/http"
	"strconv"
)

func registerToolSnapshotAdminRoutes(r *gin.RouterGroup, svc *service.Service) {
	group := r.Group("/admin/agent")
	group.Use(func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		if err = svc.RequireAdmin(actor); err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		c.Next()
	})
	health := func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		data, err := svc.AdminToolSnapshotHealth(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, data)
	}
	group.GET("/tool-snapshot-health", health)
	group.POST("/tool-snapshot-checks", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		data, err := svc.AdminRecheckToolSnapshots(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, data)
	})
	group.GET("/tool-snapshots", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, _ := strconv.Atoi(c.Query("page"))
		pageSize, _ := strconv.Atoi(c.Query("pageSize"))
		rows, err := svc.AdminListToolSnapshots(actor, service.ToolSnapshotListQuery{Page: page, PageSize: pageSize, Source: c.Query("source"), Tool: c.Query("tool"), Version: c.Query("version"), RunID: c.Query("runId")})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, rows)
	})
	group.GET("/tool-snapshots/compare", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		data, err := svc.AdminCompareToolSnapshots(actor, c.Query("left"), c.Query("right"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, data)
	})
	group.GET("/tool-snapshots/:id", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		data, err := svc.AdminToolSnapshotDetail(actor, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, data)
	})
	group.PATCH("/runtime-instances/:id/drain", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var input service.ToolRuntimeDrain
		if err = c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		data, err := svc.AdminDrainToolRuntime(actor, c.Param("id"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, data)
	})
}
