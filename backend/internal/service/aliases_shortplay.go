package service

import "yingce/backend/internal/app"

// shortplay 工作台的稳定再导出面：handler 只依赖 service，类型定义仍在
// app（指向 internal/shortplay 的合同类型）一处。
type (
	CreateShortplayRequest         = app.CreateShortplayRequest
	CreateShortplayChapter         = app.CreateShortplayEpisode
	UpdateShortplaySettingsRequest = app.UpdateShortplaySettingsRequest
	ShortplaySummary               = app.ShortplaySummary
	ShortplayEpisodeSummary        = app.ShortplayEpisodeSummary
	ShortplayListItem              = app.ShortplayListItem
	ShortplayDetail                = app.ShortplayDetail
	ShortplaySettingsUpdateResult  = app.ShortplaySettingsUpdateResult
)
