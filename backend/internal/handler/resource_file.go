package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func resourceFileHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		options := resourceAccessOptions(c)
		if tryServeResourceTranscoded(c, svc, user.ID, c.Param("id")) {
			return
		}
		delivery, err := svc.PrepareResourceDelivery(user.ID, c.Param("id"), options, c.GetHeader("Range"))
		if err != nil {
			failService(c, err)
			return
		}
		serveResourceDelivery(c, delivery, "private, no-cache", "")
	}
}
