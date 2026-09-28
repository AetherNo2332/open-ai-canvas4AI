package handler

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

const (
	internalAgentWireVersion      = "canvas-pi-wire/v1"
	internalAgentPiSDKVersion     = "0.87.1"
	internalAgentSessionFormat    = "3"
	internalAgentProtocolMismatch = "Pi Agent worker protocol version mismatch"
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
		if c.GetHeader("X-Agent-Protocol-Version") != internalAgentWireVersion ||
			c.GetHeader("X-Pi-SDK-Version") != internalAgentPiSDKVersion ||
			c.GetHeader("X-Pi-Session-Format") != internalAgentSessionFormat {
			c.AbortWithStatusJSON(http.StatusUpgradeRequired, gin.H{
				"code": http.StatusUpgradeRequired, "data": nil,
				"msg": internalAgentProtocolMismatch, "reason": "agent_protocol_mismatch",
			})
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
		run, err := svc.PiAgentSnapshot(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"run": run})
	})
	group.GET("/runs/:id/skills/:nativeName/file", func(c *gin.Context) {
		parseInt := func(value string, fallback int) int {
			if value == "" {
				return fallback
			}
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return -1
			}
			return parsed
		}
		page, err := svc.PiSkillFile(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), service.PiSkillFileRequest{
			NativeName: c.Param("nativeName"), Path: c.Query("path"), Offset: parseInt(c.Query("offset"), 0), Limit: parseInt(c.Query("limit"), 0),
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, page)
	})
	group.POST("/runs/:id/renew", func(c *gin.Context) {
		if err := svc.RenewPiAgentLease(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c)); err != nil {
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
		outcome, err := svc.PiCheckpointMessageOutcome(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, outcome)
	})
	group.POST("/runs/:id/context-compactions", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var input service.PiContextCompactionStart
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		operation, err := svc.PiBeginContextCompaction(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, operation)
	})
	group.GET("/runs/:id/context-compactions/:operationId", func(c *gin.Context) {
		operation, err := svc.PiContextCompaction(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), c.Param("operationId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, operation)
	})
	group.POST("/runs/:id/context-compactions/:operationId/commit", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var input service.PiContextCompactionCommit
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		revision, err := svc.PiCommitContextCompaction(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), c.Param("operationId"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"committed": true, "sessionRevision": revision})
	})
	group.POST("/runs/:id/no-tool-turn", func(c *gin.Context) {
		var input struct {
			TaskID string `json:"taskId" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		decision, err := svc.PiNoToolTurn(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), input.TaskID)
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
		step, err := svc.PiModelStep(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, step)
	})
	group.GET("/runs/:id/model-steps/:taskId", func(c *gin.Context) {
		step, err := svc.PiModelStepView(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), c.Param("taskId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, step)
	})
	group.POST("/runs/:id/model-steps/:taskId/ack", func(c *gin.Context) {
		if err := svc.PiModelStepAck(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), c.Param("taskId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"acknowledged": true})
	})
	group.POST("/runs/:id/model-steps/:taskId/fail", func(c *gin.Context) {
		decision, err := svc.PiFailModelStepResult(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), c.Param("taskId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, decision)
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
		if err := svc.PiToolBatch(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), input); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"accepted": true})
	})
	group.POST("/runs/:id/fail", func(c *gin.Context) {
		var input struct {
			Reason string `json:"reason"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		if err := c.ShouldBindJSON(&input); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if err := svc.PiFailRun(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), input.Reason); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"failed": true})
	})
	group.POST("/runs/:id/tool-calls/:callId/advance", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var input struct {
			TaskID string `json:"taskId"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		receipt, err := svc.PiToolAdvance(c.GetHeader("X-Agent-User-ID"), c.Param("id"), internalAgentOwner(c), input.TaskID, c.Param("callId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, receipt)
	})
}

func internalAgentOwner(c *gin.Context) string {
	workerID := strings.TrimSpace(c.GetHeader("X-Agent-Worker-ID"))
	epoch := strings.TrimSpace(c.GetHeader("X-Agent-Session-Epoch"))
	if workerID == "" || epoch == "" {
		return ""
	}
	return workerID + "@" + epoch
}
