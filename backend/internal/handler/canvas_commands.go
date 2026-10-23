package handler

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"io"
	"net/http"
	"strconv"
)

type CanvasPrincipalResolver func(*gin.Context) (identity.Principal, error)

func canvasCommandError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	err = commands.PersistenceError(err)
	var typed *commands.Error
	if !errors.As(err, &typed) {
		var admission *kernel.AppError
		if errors.As(err, &admission) && admission.Status >= 400 && admission.Status < 500 {
			c.AbortWithStatusJSON(admission.Status, gin.H{"code": admission.Code, "data": gin.H{"reason": admission.Reason}, "msg": admission.Message})
			return true
		}
		c.AbortWithStatusJSON(500, gin.H{"code": 500, "data": nil, "msg": "画布命令未提交"})
		return true
	}
	status := 400
	switch typed.Reason {
	case "permission_denied":
		status = 403
	case "entity_deleted", "receipt_missing", "history_empty":
		status = 404
	case "revision_conflict", "entity_version_conflict", "document_epoch_mismatch", "history_conflict", "operation_id_reused", "node_locked", "edit_proof_invalid":
		status = 409
	}
	c.AbortWithStatusJSON(status, gin.H{"code": status, "data": gin.H{"reason": typed.Reason, "field": typed.Field}, "msg": typed.Message})
	return true
}
func decodeCanvasCommand(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"code": 400, "data": gin.H{"reason": "invalid_envelope"}, "msg": "命令格式无效"})
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		c.AbortWithStatusJSON(400, gin.H{"code": 400, "data": gin.H{"reason": "invalid_envelope"}, "msg": "命令必须只有一个 JSON 对象"})
		return false
	}
	return true
}
func commandOK(c *gin.Context, data any) { c.JSON(200, gin.H{"code": 0, "data": data, "msg": "ok"}) }
func RegisterCanvasCommandRoutes(r *gin.RouterGroup, svc *commands.Service, principal CanvasPrincipalResolver) {
	resolve := func(c *gin.Context) (identity.Principal, bool) {
		p, err := principal(c)
		return p, !canvasCommandError(c, err)
	}
	r.GET("/desktop/capabilities", func(c *gin.Context) {
		if _, ok := resolve(c); ok {
			commandOK(c, commands.Capabilities())
		}
	})
	r.POST("/desktop/canvases", func(c *gin.Context) {
		p, ok := resolve(c)
		if !ok {
			return
		}
		var req commands.CreateCanvasRequest
		if !decodeCanvasCommand(c, &req) {
			return
		}
		out, err := svc.Create(c.Request.Context(), p, req)
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.GET("/desktop/canvases/:canvasId/snapshot", func(c *gin.Context) {
		p, ok := resolve(c)
		if !ok {
			return
		}
		out, err := svc.ReadSnapshot(c.Request.Context(), p, c.Param("canvasId"))
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.GET("/desktop/canvases/:canvasId/history-state", func(c *gin.Context) {
		p, ok := resolve(c)
		if !ok {
			return
		}
		out, err := svc.ReadHistoryState(c.Request.Context(), p, c.Param("canvasId"), c.Query("documentEpoch"), c.Query("clientId"))
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.POST("/desktop/canvases/:canvasId/commands", func(c *gin.Context) {
		p, ok := resolve(c)
		if !ok {
			return
		}
		var req commands.Command
		if !decodeCanvasCommand(c, &req) {
			return
		}
		if req.CanvasID != c.Param("canvasId") {
			canvasCommandError(c, &commands.Error{Reason: "invalid_envelope", Message: "命令画布 ID 与路径不一致"})
			return
		}
		out, err := svc.Execute(c.Request.Context(), p, req)
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.GET("/desktop/canvases/:canvasId/receipts/:operationId", func(c *gin.Context) {
		p, ok := resolve(c)
		if !ok {
			return
		}
		out, err := svc.GetReceipt(c.Request.Context(), p, c.Param("canvasId"), c.Query("documentEpoch"), c.Query("clientId"), c.Param("operationId"))
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	r.GET("/desktop/canvases/:canvasId/events", func(c *gin.Context) {
		p, ok := resolve(c)
		if !ok {
			return
		}
		after, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
		if err != nil {
			canvasCommandError(c, &commands.Error{Reason: "invalid_cursor", Message: "事件游标无效"})
			return
		}
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "250"))
		if err != nil {
			canvasCommandError(c, &commands.Error{Reason: "invalid_cursor", Message: "事件数量无效"})
			return
		}
		out, err := svc.ReadEvents(c.Request.Context(), p, c.Param("canvasId"), after, limit)
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
	for _, action := range []string{"undo", "redo"} {
		r.POST("/desktop/canvases/:canvasId/"+action, func(c *gin.Context) {
			p, ok := resolve(c)
			if !ok {
				return
			}
			var req struct {
				DocumentEpoch string `json:"documentEpoch"`
				ClientID      string `json:"clientId"`
				OperationID   string `json:"operationId"`
			}
			if !decodeCanvasCommand(c, &req) {
				return
			}
			var out commands.Receipt
			var err error
			if action == "undo" {
				out, err = svc.Undo(c.Request.Context(), p, c.Param("canvasId"), req.DocumentEpoch, req.ClientID, req.OperationID)
			} else {
				out, err = svc.Redo(c.Request.Context(), p, c.Param("canvasId"), req.DocumentEpoch, req.ClientID, req.OperationID)
			}
			if !canvasCommandError(c, err) {
				commandOK(c, out)
			}
		})
	}
	r.POST("/desktop/canvases/:canvasId/restore", func(c *gin.Context) {
		p, ok := resolve(c)
		if !ok {
			return
		}
		var req struct {
			DocumentEpoch    string            `json:"documentEpoch"`
			ClientID         string            `json:"clientId"`
			OperationID      string            `json:"operationId"`
			Document         commands.Document `json:"document"`
			ExpectedRevision *int64            `json:"expectedRevision"`
		}
		if !decodeCanvasCommand(c, &req) {
			return
		}
		if req.ExpectedRevision == nil {
			canvasCommandError(c, commands.BadAuthRequest("恢复版本需要当前修订号"))
			return
		}
		out, err := svc.RestoreVersion(c.Request.Context(), p, c.Param("canvasId"), req.DocumentEpoch, req.ClientID, req.OperationID, *req.ExpectedRevision, req.Document)
		if !canvasCommandError(c, err) {
			commandOK(c, out)
		}
	})
}
