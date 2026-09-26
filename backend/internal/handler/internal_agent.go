package handler

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

// RegisterInternalAgentRoutes is mounted outside /api so the public web proxy
// cannot expose the worker protocol. The token is additionally checked on
// every request; user ownership is checked by the service for each leased run.
func RegisterInternalAgentRoutes(r *gin.Engine, svc *service.Service) {
	secret := os.Getenv("CANVAS_AGENT_INTERNAL_TOKEN")
	if secret == "" {
		return
	}
	group := r.Group("/internal-agent")
	group.Use(func(c *gin.Context) {
		provided := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if len(provided) != len(secret) || subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	group.POST("/claim", func(c *gin.Context) {
		var input struct {
			Owner string `json:"owner" binding:"required"`
		}
		if err := c.ShouldBindJSON(&input); err != nil || len(input.Owner) > 80 {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		claimed, err := svc.ClaimPiAgent(input.Owner)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"run": claimed})
	})
	group.GET("/runs/:id", func(c *gin.Context) {
		run, err := svc.PiAgentSnapshot(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"run": run})
	})
	group.POST("/runs/:id/renew", func(c *gin.Context) {
		if err := svc.RenewPiAgentLease(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"renewed": true})
	})
	group.POST("/runs/:id/messages", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var input service.PiMessageCheckpoint
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if err := svc.PiCheckpointMessage(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), input); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"saved": true})
	})
	group.POST("/runs/:id/no-tool-turn", func(c *gin.Context) {
		var input struct {
			TaskID string `json:"taskId" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		decision, err := svc.PiNoToolTurn(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), input.TaskID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, decision)
	})
	group.POST("/runs/:id/model-steps", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		var input service.PiModelStepRequest
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		step, err := svc.PiModelStep(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, step)
	})
	group.GET("/runs/:id/model-steps/:taskId", func(c *gin.Context) {
		step, err := svc.PiModelStepView(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), c.Param("taskId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, step)
	})
	group.POST("/runs/:id/model-steps/:taskId/ack", func(c *gin.Context) {
		if err := svc.PiModelStepAck(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), c.Param("taskId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"acknowledged": true})
	})
	group.POST("/runs/:id/model-steps/:taskId/fail", func(c *gin.Context) {
		if err := svc.PiFailModelStep(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), c.Param("taskId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"failed": true})
	})
	group.POST("/runs/:id/tool-batches", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
		var input service.PiToolBatchRequest
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if err := svc.PiToolBatch(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), input); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"accepted": true})
	})
	group.POST("/runs/:id/tool-calls/:callId/advance", func(c *gin.Context) {
		receipt, err := svc.PiToolAdvance(c.GetHeader("X-Agent-User-ID"), c.Param("id"), c.GetHeader("X-Agent-Worker-ID"), c.Param("callId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, receipt)
	})
}
