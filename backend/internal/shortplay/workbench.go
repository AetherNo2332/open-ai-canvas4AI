package shortplay

// 独立 shortplay 工作台的 HTTP 合同常量。请求 / 响应结构见 types.go。
//
// 供体固定提交 13731aae6 的 shortplay/model.go 与 app/shortplay_service.go；
// 目标端只调整包归属，不增删业务字段。

const (
	// ProjectType 是 projects 容器的域判别值，与 drama 的 short-drama 并存。
	ProjectType = "shortplay"
	// NameRunes 与 drama 的作品名上限对齐。
	NameRunes = 80
	// MaxEpisodes 只挡异常超大请求；集边界由前端切好，后端不切分。
	MaxEpisodes = 3000
	// JobSummaryLimit 是详情页返回的最近 job 条数。
	JobSummaryLimit = 40
)
