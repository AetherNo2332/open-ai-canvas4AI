package handler

import (
	"net/http"
	"time"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterCreationPlatformRoutes 挂载平台内部创作合同的 HTTP 适配。身份只来自登录会话；
// 请求体中的 BusinessRef 只是归属标签。失败时信封 reason 携带合同错误码。
func RegisterCreationPlatformRoutes(r *gin.RouterGroup, svc *service.Service) {
	group := r.Group("/platform/v1")
	group.POST("/generations", func(c *gin.Context) {
		caller, authenticated := platformCaller(c, svc)
		if !authenticated {
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "tasks:"+caller.UserID, policy.Request.TaskCreatePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var req cp.GenerationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failPlatform(c, cp.NewError(cp.CodeInvalidRequest, "请求格式错误"))
			return
		}
		if req.TraceID == "" {
			req.TraceID = TraceID(c)
		}
		receipt, err := service.CreationPlatformFor(svc).Submit(c.Request.Context(), caller, req)
		if err != nil {
			failPlatform(c, err)
			return
		}
		ok(c, receipt)
	})
	group.GET("/tasks/:id", func(c *gin.Context) {
		caller, authenticated := platformCaller(c, svc)
		if !authenticated {
			return
		}
		snapshot, err := service.CreationPlatformFor(svc).Task(c.Request.Context(), caller, c.Param("id"))
		if err != nil {
			failPlatform(c, err)
			return
		}
		ok(c, snapshot)
	})
}

func platformCaller(c *gin.Context, svc *service.Service) (cp.Caller, bool) {
	user, err := currentUser(c, svc)
	if err != nil {
		failService(c, err)
		return cp.Caller{}, false
	}
	return cp.Caller{UserID: user.ID}, true
}

// failPlatform 把合同错误写入统一信封；未分类错误走 failService，不泄露内部原文。
func failPlatform(c *gin.Context, err error) {
	platformErr, isPlatform := cp.AsError(service.CreationPlatformError(err))
	if !isPlatform {
		failService(c, err)
		return
	}
	status := platformErr.HTTPStatus()
	if status >= http.StatusInternalServerError {
		logHandlerError(c, status, err)
	}
	body := gin.H{"code": status, "data": nil, "msg": platformErr.Message, "reason": platformErr.Code}
	details := gin.H{"retryable": platformErr.Retryable}
	if platformErr.TaskID != "" {
		details["taskId"] = platformErr.TaskID
	}
	if requestID := RequestID(c); requestID != "" {
		details["requestId"] = requestID
	}
	body["details"] = details
	c.JSON(status, body)
}
