package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"net/http"
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
	if errors.Is(err, gorm.ErrRecordNotFound) {
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
