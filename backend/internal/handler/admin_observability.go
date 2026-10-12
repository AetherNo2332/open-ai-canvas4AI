package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

func RegisterAdminObservabilityRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/observability/overview", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireAdmin(user); err != nil {
			failService(c, err)
			return
		}
		window := 15 * time.Minute
		if raw := c.Query("window"); raw != "" {
			seconds, parseErr := strconv.Atoi(raw)
			if parseErr != nil || seconds < 60 || seconds > 24*60*60 {
				fail(c, http.StatusBadRequest, service.BadAuthRequest("观测窗口必须为 60 到 86400 秒"))
				return
			}
			window = time.Duration(seconds) * time.Second
		}
		result, err := svc.AdminObservability(user, window, analyticsQuery(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}
