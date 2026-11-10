// 管理端技能管理接口：列表、详情、基本信息、上架 / 下架、删除，以及技能包文件浏览与编辑。
//
// 所有接口在 service 层通过 RequireAdmin 校验身份；这里只做参数解析与响应封装。
package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func RegisterAdminSkillRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerAdminSkillCatalogRoutes(r, svc)
	registerAdminSkillFileRoutes(r, svc)
}
