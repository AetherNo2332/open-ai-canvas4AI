package shortplay

import "yingce/backend/internal/model"

// UpsertAssetRequest 是资产身份行 + 设定的一次提交：assetId 为 0 表示新建，
// 非 0 表示更新该行（必须属于同一作品且 kind 不变）。
type UpsertAssetRequest struct {
	AssetID     int      `json:"assetId"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
	Appearance  string   `json:"appearance"`
	Voice       string   `json:"voice"`
	Locked      bool     `json:"locked"`
	DerivedFrom int      `json:"derivedFrom,omitempty"`
	VariantName string   `json:"variantName,omitempty"`
	RenderRule  *string  `json:"renderRule,omitempty"`
}

// AssetSummary 是资产读模型：身份 + 设定 + 当前图/音投影 + 分集绑定 + 行版本。
type AssetSummary struct {
	ID              int      `json:"id"`
	Kind            string   `json:"kind"`
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Description     string   `json:"description"`
	Appearance      string   `json:"appearance"`
	Voice           string   `json:"voice"`
	Locked          bool     `json:"locked"`
	ImageResourceID string   `json:"imageResourceId,omitempty"`
	ImageStatus     string   `json:"imageStatus"`
	ImageVersion    int      `json:"imageVersion"`
	AudioResourceID string   `json:"audioResourceId,omitempty"`
	AudioStatus     string   `json:"audioStatus,omitempty"`
	AudioVersion    int      `json:"audioVersion,omitempty"`
	EpisodeNumbers  []int    `json:"episodeNumbers"`
	Revision        int64    `json:"revision"`
	DerivedFrom     int      `json:"derivedFrom,omitempty"`
	VariantName     string   `json:"variantName,omitempty"`
	RenderRule      string   `json:"renderRule,omitempty"`
}

// ExtractQueueResult 是提取入队结果：Queued=false 表示已有在途提取，返回的是那一条。
type ExtractQueueResult struct {
	JobID  string `json:"jobId"`
	Queued bool   `json:"queued"`
	Cursor int    `json:"cursor"`
}

// BuildAssetSummary 把落库行转成读模型；别名与分集绑定从 JSON 列还原。
func BuildAssetSummary(row model.ShortplayAsset) AssetSummary {
	return AssetSummary{
		ID:              row.ID,
		Kind:            row.Kind,
		Name:            row.Name,
		Aliases:         SplitAliasesJSON(row.AliasesJSON),
		Description:     row.DisplayDescription(),
		Appearance:      row.Appearance,
		Voice:           row.Voice,
		Locked:          row.Locked,
		ImageResourceID: row.ImageResourceID,
		ImageStatus:     row.ImageStatus,
		ImageVersion:    row.ImageVersion,
		AudioResourceID: row.AudioResourceID,
		AudioStatus:     row.AudioStatus,
		AudioVersion:    row.AudioVersion,
		EpisodeNumbers:  SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON),
		Revision:        row.Revision,
		DerivedFrom:     row.DerivedFrom,
		VariantName:     row.VariantName,
		RenderRule:      row.RenderRule,
	}
}
