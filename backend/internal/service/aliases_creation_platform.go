package service

import (
	"yingce/backend/internal/app"
	cp "yingce/backend/internal/creationplatform"
)

// 创作平台合同由 internal/creationplatform 定义；这里只为 handler 再导出入口与错误映射。
var CreationPlatformError = app.CreationPlatformError

// CreationPlatformFor 返回 svc 的创作平台入口。
func CreationPlatformFor(svc *Service) cp.Platform { return svc.CreationPlatform() }
