package handler

import (
	"net/http"
	"strconv"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterAgentSessionRoutes 挂载通用 Agent Session 的 HTTP 适配。所有者只来自登录会话；
// 请求体里的 Scope 只说明目标业务空间，归属由服务端校验。失败时信封 reason 携带 Agent 错误码。
func RegisterAgentSessionRoutes(r *gin.RouterGroup, svc *service.Service) {
	group := r.Group("/agent/v2")
	group.GET("/definitions", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		views, err := service.AgentSessionsFor(svc).Definitions(c.Request.Context(), caller, c.Query("application"))
		if err != nil {
			failAgentService(c, err)
			return
		}
		ok(c, gin.H{"definitions": views})
	})
	group.POST("/sessions", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req as.CreateSessionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failAgentService(c, as.NewError(as.CodeInvalidRequest, "请求格式错误"))
			return
		}
		view, err := service.AgentSessionsFor(svc).Create(c.Request.Context(), caller, req)
		if err != nil {
			failAgentService(c, err)
			return
		}
		ok(c, view)
	})
	group.GET("/sessions", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		req := as.ListSessionsRequest{Cursor: c.Query("cursor")}
		if raw := c.Query("limit"); raw != "" {
			limit, err := strconv.Atoi(raw)
			if err != nil || limit < 1 {
				failAgentService(c, as.NewError(as.CodeInvalidRequest, "limit 必须是正整数"))
				return
			}
			req.Limit = limit
		}
		if c.Query("application") != "" || c.Query("spaceType") != "" || c.Query("spaceId") != "" {
			req.Scope = &as.Scope{Application: c.Query("application"), SpaceType: c.Query("spaceType"), SpaceID: c.Query("spaceId")}
		}
		page, err := service.AgentSessionsFor(svc).List(c.Request.Context(), caller, req)
		if err != nil {
			failAgentService(c, err)
			return
		}
		ok(c, page)
	})
	group.GET("/sessions/:id", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		view, err := service.AgentSessionsFor(svc).Get(c.Request.Context(), caller, c.Param("id"))
		if err != nil {
			failAgentService(c, err)
			return
		}
		ok(c, view)
	})
	group.POST("/sessions/:id/archive", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		var body struct {
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.ExpectedRevision < 1 {
			failAgentService(c, as.NewError(as.CodeInvalidRequest, "expectedRevision 必填"))
			return
		}
		view, err := service.AgentSessionsFor(svc).Archive(c.Request.Context(), caller, as.ArchiveSessionRequest{SessionID: c.Param("id"), ExpectedRevision: body.ExpectedRevision})
		if err != nil {
			failAgentService(c, err)
			return
		}
		ok(c, view)
	})
}

func agentServiceCaller(c *gin.Context, svc *service.Service) (as.Caller, bool) {
	user, err := currentUser(c, svc)
	if err != nil {
		failService(c, err)
		return as.Caller{}, false
	}
	return as.Caller{UserID: user.ID}, true
}

// failAgentService 把 Agent 合同错误写入统一信封；未分类错误走 failService，不泄露内部原文。
func failAgentService(c *gin.Context, err error) {
	agentErr, isAgent := as.AsError(err)
	if !isAgent {
		failService(c, err)
		return
	}
	status := agentErr.HTTPStatus()
	if status >= http.StatusInternalServerError {
		logHandlerError(c, status, err)
	}
	details := gin.H{"retryable": agentErr.Retryable}
	if requestID := RequestID(c); requestID != "" {
		details["requestId"] = requestID
	}
	c.JSON(status, gin.H{"code": status, "data": nil, "msg": agentErr.Message, "reason": agentErr.Code, "details": details})
}
