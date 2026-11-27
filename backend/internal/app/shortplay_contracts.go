package app

import "yingce/backend/internal/shortplay"

// shortplay 域的稳定再导出面：handler 经 service 别名引用，页面合同不直接依赖
// 内部包的包路径。类型定义只在 shortplay 包一处。
type (
	CreateShortplayRequest         = shortplay.CreateShortplayRequest
	CreateShortplayEpisode         = shortplay.EpisodeInput
	ShortplaySummary               = shortplay.ShortplaySummary
	ShortplayEpisodeSummary        = shortplay.EpisodeSummary
	ShortplayListItem              = shortplay.ShortplayListItem
	ShortplayDetail                = shortplay.ShortplayDetail
	UpdateShortplaySettingsRequest = shortplay.UpdateSettingsRequest
	ShortplaySettingsUpdateResult  = shortplay.SettingsUpdateResult
	UpsertShortplayAssetRequest    = shortplay.UpsertAssetRequest
	ShortplayAssetSummary          = shortplay.AssetSummary
	ShortplayExtractQueueResult    = shortplay.ExtractQueueResult
)
