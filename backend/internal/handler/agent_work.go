package handler

import (
	"errors"
	"net/http"
	"strconv"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterAgentWorkRoutes 挂载会话消息、工作、报告、审批与预算只读接口。所有者只来自登录会话；
// 消息发送者身份由服务端写入，请求体里的 actor 等字段一律忽略。
func RegisterAgentWorkRoutes(r *gin.RouterGroup, svc *service.Service) {
	group := r.Group("/agent/v2")
	group.GET("/sessions/:id/messages", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		after, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
		if err != nil || after < 0 {
			failAgentService(c, as.NewError(as.CodeInvalidRequest, "after 必须是非负整数"))
			return
		}
		page, err := svc.SessionMessages(caller.UserID, c.Param("id"), after)
		respondAgent(c, page, err)
	})
	group.POST("/sessions/:id/messages", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var body struct {
			MessageID string `json:"messageId"`
			Text      string `json:"text"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			failAgentService(c, as.NewError(as.CodeInvalidRequest, "请求格式错误"))
			return
		}
		receipt, err := svc.SendSessionMessage(caller.UserID, service.SendSessionMessageRequest{SessionID: c.Param("id"), MessageID: body.MessageID, Text: body.Text})
		respondAgent(c, receipt, err)
	})
	group.GET("/works", func(c *gin.Context) { handleListAgentWorks(c, svc) })
	group.POST("/work", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		var req service.StartWorkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failAgentService(c, as.NewError(as.CodeInvalidRequest, "请求格式错误"))
			return
		}
		work, err := svc.StartWork(caller.UserID, req)
		respondAgent(c, work, err)
	})
	group.GET("/work/:id", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		work, err := svc.GetWork(caller.UserID, c.Param("id"))
		respondAgent(c, work, err)
	})
	group.GET("/work/:id/reports", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		reports, err := svc.ListReports(caller.UserID, c.Param("id"))
		respondAgent(c, gin.H{"reports": reports}, err)
	})
	group.POST("/work/:id/stop", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		var body struct {
			ExpectedVersion int64  `json:"expectedVersion"`
			Reason          string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			failAgentService(c, as.NewError(as.CodeInvalidRequest, "请求格式错误"))
			return
		}
		work, err := svc.StopWork(caller.UserID, service.WorkControlRequest{WorkItemID: c.Param("id"), ExpectedVersion: body.ExpectedVersion, Reason: body.Reason})
		respondAgent(c, work, err)
	})
	group.GET("/work/:id/approvals", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		approvals, err := svc.WorkApprovals(caller.UserID, c.Param("id"))
		respondAgent(c, gin.H{"approvals": approvals}, err)
	})
	group.POST("/approvals/:id/decision", func(c *gin.Context) {
		caller, authenticated := agentServiceCaller(c, svc)
		if !authenticated {
			return
		}
		var body struct {
			Decision                     string `json:"decision"`
			ExpectedAuthorizationVersion int64  `json:"expectedAuthorizationVersion"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			failAgentService(c, as.NewError(as.CodeInvalidRequest, "请求格式错误"))
			return
		}
		approval, err := svc.DecideApproval(caller.UserID, service.DecideApprovalRequest{ApprovalID: c.Param("id"), Decision: body.Decision, ExpectedAuthorizationVersion: body.ExpectedAuthorizationVersion})
		respondAgent(c, approval, err)
	})
	r.GET("/platform/v1/budget-grants/:id", func(c *gin.Context) {
		caller, authenticated := platformCaller(c, svc)
		if !authenticated {
			return
		}
		grant, err := svc.BudgetGrants().Get(c.Request.Context(), caller, c.Param("id"))
		if err != nil {
			failPlatform(c, err)
			return
		}
		ok(c, grant)
	})
}

func respondAgent(c *gin.Context, data any, err error) {
	if err != nil {
		failAgentWork(c, err)
		return
	}
	ok(c, data)
}

var workErrorStatus = map[ao.ErrorCode]int{
	ao.CodeInvalidRequest: http.StatusBadRequest, ao.CodeNotFound: http.StatusNotFound, ao.CodeForbidden: http.StatusForbidden,
	ao.CodeWorkStopped: http.StatusConflict, ao.CodeStaleAssignment: http.StatusConflict, ao.CodeVersionConflict: http.StatusConflict,
}

// handleListAgentWorks 是总控工作台目录的 HTTP 适配。
func handleListAgentWorks(c *gin.Context, svc *service.Service) {
	caller, authenticated := agentServiceCaller(c, svc)
	if !authenticated {
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 0 {
		failAgentService(c, as.NewError(as.CodeInvalidRequest, "limit 必须是非负整数"))
		return
	}
	works, err := svc.ListAgentWorks(caller.UserID, limit)
	respondAgent(c, gin.H{"works": works}, err)
}

// failAgentWork maps work, session and platform contract errors to the envelope.
func failAgentWork(c *gin.Context, err error) {
	var workErr *ao.Error
	if errors.As(err, &workErr) {
		status := workErrorStatus[workErr.Code]
		if status == 0 {
			status = http.StatusInternalServerError
		}
		c.JSON(status, gin.H{"code": status, "data": nil, "msg": workErr.Message, "reason": workErr.Code, "details": gin.H{"retryable": false}})
		return
	}
	if _, isPlatform := cp.AsError(err); isPlatform {
		failPlatform(c, err)
		return
	}
	failAgentService(c, err)
}
