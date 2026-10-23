package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

func decodeAgentMessageBody[T any](c *gin.Context, target *T) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var value *T
	if err := decoder.Decode(&value); err != nil {
		fail(c, http.StatusBadRequest, err)
		return false
	}
	if value == nil {
		fail(c, http.StatusBadRequest, errors.New("请求必须是 JSON 对象"))
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fail(c, http.StatusBadRequest, errors.New("请求必须只包含一个 JSON 对象"))
		return false
	}
	*target = *value
	return true
}
func agentMessageWriteAllowed(c *gin.Context, svc *service.Service, userID string) bool {
	policy, available := loadRuntimePolicy(c, svc)
	return available && enforceRateLimit(c, "tasks:"+userID, policy.Request.TaskCreatePerMinute, time.Minute)
}
func RegisterAgentMessageQueueRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/agent/ui-settings", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.AgentUISettings(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PATCH("/agent/ui-settings", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var input struct {
			Revision        *int64  `json:"revision"`
			DefaultSendMode *string `json:"defaultSendMode"`
		}
		if !decodeAgentMessageBody(c, &input) {
			return
		}
		if input.Revision == nil || input.DefaultSendMode == nil {
			fail(c, http.StatusBadRequest, errors.New("需要发送方式和设置版本"))
			return
		}
		view, err := svc.UpdateAgentUISettings(user.ID, service.AgentUISetting{Revision: *input.Revision, DefaultSendMode: *input.DefaultSendMode})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.POST("/agent/conversations/:id/messages", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var input service.AgentConversationMessageRequest
		if !decodeAgentMessageBody(c, &input) {
			return
		}
		if !agentMessageWriteAllowed(c, svc, user.ID) {
			return
		}
		receipt, err := svc.SendAgentConversationMessage(user.ID, c.Param("id"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, receipt)
	})
	r.GET("/agent/conversations/:id/queue", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.AgentMessageQueue(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PATCH("/agent/conversations/:id/queue", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var input service.AgentMessageQueueUpdate
		if !decodeAgentMessageBody(c, &input) {
			return
		}
		view, err := svc.UpdateAgentMessageQueue(user.ID, c.Param("id"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PATCH("/agent/conversations/:id/queue/:messageId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var input service.AgentQueuedMessageUpdate
		if !decodeAgentMessageBody(c, &input) {
			return
		}
		if !agentMessageWriteAllowed(c, svc, user.ID) {
			return
		}
		view, err := svc.UpdateAgentQueuedMessage(user.ID, c.Param("id"), c.Param("messageId"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.DELETE("/agent/conversations/:id/queue/:messageId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		revision, err := strconv.ParseInt(c.Query("revision"), 10, 64)
		if err != nil || revision < 1 {
			fail(c, http.StatusBadRequest, errors.New("需要有效的消息版本"))
			return
		}
		view, err := svc.CancelAgentQueuedMessage(user.ID, c.Param("id"), c.Param("messageId"), revision)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
}
