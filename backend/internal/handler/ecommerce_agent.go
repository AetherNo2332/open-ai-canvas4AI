package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/service"
)

func ecommerceAgentFailure(c *gin.Context, err error) {
	var ae *service.EcommerceAgentError
	if errors.As(err, &ae) {
		status := http.StatusBadRequest
		if ae.Code == "INSUFFICIENT_CREDITS" {
			status = http.StatusPaymentRequired
		}
		if ae.Code == "UPSTREAM_ERROR" {
			status = http.StatusBadGateway
		}
		if ae.Code == "UNAVAILABLE" {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"code": status, "data": nil, "msg": ae.Message, "reason": ae.Code})
		return
	}
	if service.EcommerceAgentNotFound(err) {
		fail(c, http.StatusNotFound, errors.New("项目或任务不存在"))
		return
	}
	failService(c, err)
}

func RegisterEcommerceAgentRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerEcommerceAgentPlanningRoutes(r, svc)
	registerEcommerceAgentStreamRoutes(r, svc)
	registerEcommerceAgentCanvasRoutes(r, svc)
	registerEcommerceAgentTaskRoutes(r, svc)
}
