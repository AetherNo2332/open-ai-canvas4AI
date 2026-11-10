package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerEcommerceRoundRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/ecommerce/projects/:id/rounds", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "10"))
		before, beforeErr := strconv.Atoi(c.DefaultQuery("before", "0"))
		if limitErr != nil || beforeErr != nil || before < 0 {
			fail(c, http.StatusBadRequest, errors.New("历史分页参数无效"))
			return
		}
		page, err := svc.EcommerceRoundPage(user.ID, c.Param("id"), limit, before)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, page)
	})
	r.POST("/ecommerce/projects/:id/rounds-convert", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
		var req struct {
			Revision int64 `json:"revision"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Revision < 1 {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("缺少有效的项目版本"))
			return
		}
		project, conversion, err := svc.ConvertEcommerceRounds(user.ID, c.Param("id"), req.Revision)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"project": project, "conversion": conversion})
	})
	r.PATCH("/ecommerce/projects/:id/rounds/:roundId/images/:imageId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		var req service.EcommerceRoundImageUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("图片更新参数无效"))
			return
		}
		round, err := svc.UpdateEcommerceRoundImage(user.ID, c.Param("id"), c.Param("roundId"), c.Param("imageId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, round)
	})
}
