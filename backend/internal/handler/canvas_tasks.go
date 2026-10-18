package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
)

func RegisterCanvasTaskRoutes(r *gin.RouterGroup, svc *tasks.Service, principal CanvasPrincipalResolver) {
	r.GET("/canvas-runtime/canvases/:canvasId/task-submissions/:operationId", func(c *gin.Context) {
		p, err := principal(c)
		if canvasCommandError(c, err) {
			return
		}
		out, err := svc.ReadSubmission(c.Request.Context(), p, c.Param("canvasId"), c.Query("documentEpoch"), c.Query("clientId"), c.Param("operationId"))
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.POST("/canvas-runtime/tasks/:taskId/uncertainty/resolve", func(c *gin.Context) {
		p, err := principal(c)
		if canvasCommandError(c, err) {
			return
		}
		var req struct {
			Command commands.Command `json:"command"`
		}
		if !decodeCanvasCommand(c, &req) {
			return
		}
		out, err := svc.ResolveUncertainty(c.Request.Context(), p, c.Param("taskId"), req.Command)
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.POST("/canvas-runtime/canvases/:canvasId/previs-tasks", func(c *gin.Context) {
		p, err := principal(c)
		if canvasCommandError(c, err) {
			return
		}
		var req tasks.PrevisSubmitRequest
		if !decodeCanvasCommand(c, &req) {
			return
		}
		if req.Command.CanvasID != c.Param("canvasId") {
			canvasCommandError(c, &commands.Error{Reason: "invalid_envelope", Message: "任务画布 ID 与路径不符"})
			return
		}
		out, err := svc.SubmitPrevis(c.Request.Context(), p, req)
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.GET("/canvas-runtime/tasks/:taskId/materialization", func(c *gin.Context) {
		p, err := principal(c)
		if canvasCommandError(c, err) {
			return
		}
		out, err := svc.ReadMaterialization(c.Request.Context(), p, c.Param("taskId"))
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.POST("/canvas-runtime/tasks/:taskId/materialization/retry", func(c *gin.Context) {
		p, err := principal(c)
		if canvasCommandError(c, err) {
			return
		}
		if canvasCommandError(c, svc.RetryMaterialization(c.Request.Context(), p, c.Param("taskId"))) {
			return
		}
		out, err := svc.ReadMaterialization(c.Request.Context(), p, c.Param("taskId"))
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.POST("/canvas-runtime/canvases/:canvasId/tasks", func(c *gin.Context) {
		p, err := principal(c)
		if canvasCommandError(c, err) {
			return
		}
		var req tasks.SubmitRequest
		if !decodeCanvasCommand(c, &req) {
			return
		}
		if req.Command.CanvasID != c.Param("canvasId") {
			canvasCommandError(c, &commands.Error{Reason: "invalid_envelope", Message: "任务画布 ID 与路径不一致"})
			return
		}
		out, err := svc.Submit(c.Request.Context(), p, req)
		if canvasCommandError(c, err) {
			return
		}
		commandOK(c, out)
	})
}
