package shortplay

import (
	"encoding/json"
	"time"

	"yingce/backend/internal/model"
)

// HTTP 合同形状。字段名、默认值与错误语义对齐供体固定提交 13731aae6 的
// backend/internal/shortplay/model.go 与 handler/shortplay_routes.go；目标端只做
// 包归属调整，不增删业务字段。

// EpisodeInput 是建档请求里的一集原文；标题留空时由服务端按序号补齐。
type EpisodeInput struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// CreateShortplayRequest 是一次性建档：容器行、域配置与 N 条集行。
// 不建画布、不入队提取（画布与提取都由详情页显式触发）。
type CreateShortplayRequest struct {
	Name              string         `json:"name"`
	AspectRatio       string         `json:"aspectRatio"`
	SettingsJSON      string         `json:"settingsJson"`
	DefaultImageModel string         `json:"defaultImageModel"`
	DefaultVideoModel string         `json:"defaultVideoModel"`
	Chapters          []EpisodeInput `json:"chapters"`
}

// EpisodeSummary 是集行的读模型；ScriptText 只在详情返回，列表不带。
type EpisodeSummary struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	CanvasID  string `json:"canvasId"`
	WordCount int    `json:"wordCount"`
	// ScriptText 是集原文的工作副本；详情页回填编辑框用。
	ScriptText string `json:"scriptText,omitempty"`
	AssetIDs   []int  `json:"assetIds"`
}

// ShortplaySummary 是建档返回：只回身份与新集清单，不回整部原文。
type ShortplaySummary struct {
	ProjectID   string           `json:"projectId"`
	ShortplayID string           `json:"shortplayId"`
	Episodes    []EpisodeSummary `json:"episodes"`
}

// ShortplayListItem 是列表行：作品名与画幅来自 projects 容器行，集数来自 shortplay_episodes。
type ShortplayListItem struct {
	ProjectID    string    `json:"projectId"`
	ShortplayID  string    `json:"shortplayId"`
	Name         string    `json:"name"`
	AspectRatio  string    `json:"aspectRatio"`
	EpisodeCount int64     `json:"episodeCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ShortplayDetail 是详情读模型：容器、设置、全部集与最近 job。
type ShortplayDetail struct {
	ProjectID         string               `json:"projectId"`
	ShortplayID       string               `json:"shortplayId"`
	Name              string               `json:"name"`
	AspectRatio       string               `json:"aspectRatio"`
	DefaultImageModel string               `json:"defaultImageModel"`
	DefaultVideoModel string               `json:"defaultVideoModel"`
	SettingsJSON      string               `json:"settingsJson"`
	CreatedAt         time.Time            `json:"createdAt"`
	UpdatedAt         time.Time            `json:"updatedAt"`
	Episodes          []EpisodeSummary     `json:"episodes"`
	Jobs              []model.ShortplayJob `json:"jobs"`
}

// UpdateSettingsRequest 是设置整份提交；settings 是短剧会读的那几项（见 app.shortplaySettings.encode）。
// 整份写而不是逐字段 patch：设置之间互相牵制，逐字段写会落库「各自合法、组合非法」的中间态。
type UpdateSettingsRequest struct {
	Settings json.RawMessage `json:"settings"`
}

// SettingsUpdateResult 回传服务端归一后的 settingsJson，页面据此回填而不是自己猜默认值。
type SettingsUpdateResult struct {
	SettingsJSON string `json:"settingsJson"`
}

// EnsureCanvasResult 是「进精修画布」的回执：created=false 表示这一集早就建过画布。
type EnsureCanvasResult struct {
	CanvasID string `json:"canvasId"`
	Created  bool   `json:"created"`
}

// CanvasBinding 是按画布反查到的集归属；只在画布确实属于当前用户的作品时返回。
type CanvasBinding struct {
	ShortplayID   string `json:"shortplayId"`
	ProjectID     string `json:"projectId"`
	EpisodeID     string `json:"episodeId"`
	EpisodeNumber int    `json:"episodeNumber"`
	EpisodeTitle  string `json:"episodeTitle"`
}
