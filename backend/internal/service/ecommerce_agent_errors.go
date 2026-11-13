package service

import "yingce/backend/internal/app"

// EcommerceAgentNotFound 判断电商 Agent 的错误是否来自底层记录不存在。
// handler 据此映射 404，不直接依赖 ORM 错误类型；业务错误语义在此归属。
var EcommerceAgentNotFound = app.EcommerceAgentNotFound
