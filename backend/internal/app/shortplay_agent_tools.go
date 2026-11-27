package app

import (
	"encoding/json"
	"errors"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W07 短剧资产读面：集画布上的 Agent 按画布反查作品，读取资产身份、设定与媒体状态。
//
// 只读、零凭证：不返回资源下载地址，只返回 resourceId 与状态；资产行 ID 是项目内整数序号。
// 不是集画布时返回 linked=false，让模型改走普通画布工具，而不是报错。
func cloudAgentListShortplayAssets(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	if state == nil || strings.TrimSpace(state.Request.CanvasID) == "" {
		return nil, BadAuthRequest("短剧资产工具需要当前画布")
	}
	var args struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
		Page  int    `json:"page"`
	}
	if strings.TrimSpace(call.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return nil, BadAuthRequest("短剧资产参数不是合法 JSON")
		}
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, BadAuthRequest("当前画布不存在")
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(canvas.ProjectID) == "" {
		return map[string]any{"linked": false, "assets": []any{}, "note": "当前画布没有作品容器，不是短剧分集画布"}, nil
	}
	owner, err := repo.ShortplayByProject(canvas.ProjectID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return map[string]any{"linked": false, "assets": []any{}, "note": "当前画布不属于短剧作品"}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := repo.ShortplayAssets(owner.ID)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(args.Query))
	kind := strings.TrimSpace(args.Kind)
	matched := make([]shortplayAgentAsset, 0, len(rows))
	for _, row := range rows {
		if kind != "" && row.Kind != kind {
			continue
		}
		if query != "" && !shortplayAgentAssetMatches(row, query) {
			continue
		}
		matched = append(matched, shortplayAgentAssetView(row))
	}
	page := args.Page
	if page < 1 {
		page = 1
	}
	const perPage = 50
	start := (page - 1) * perPage
	if start > len(matched) {
		start = len(matched)
	}
	end := start + perPage
	if end > len(matched) {
		end = len(matched)
	}
	return map[string]any{
		"linked": true, "shortplayId": owner.ID, "total": len(matched), "page": page,
		"hasMore": end < len(matched), "assets": matched[start:end],
	}, nil
}

// shortplayAgentAsset 是给模型看的资产投影：身份、设定与媒体状态，不含凭证。
type shortplayAgentAsset struct {
	ID             int      `json:"id"`
	Kind           string   `json:"kind"`
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases"`
	Description    string   `json:"description,omitempty"`
	Appearance     string   `json:"appearance,omitempty"`
	Voice          string   `json:"voice,omitempty"`
	Locked         bool     `json:"locked"`
	Revision       int64    `json:"revision"`
	DerivedFrom    int      `json:"derivedFrom,omitempty"`
	VariantName    string   `json:"variantName,omitempty"`
	ImageStatus    string   `json:"imageStatus"`
	ImageVersion   int      `json:"imageVersion"`
	ImageResource  string   `json:"imageResourceId,omitempty"`
	AudioStatus    string   `json:"audioStatus,omitempty"`
	AudioVersion   int      `json:"audioVersion"`
	AudioResource  string   `json:"audioResourceId,omitempty"`
	EpisodeNumbers []int    `json:"episodeNumbers"`
}

func shortplayAgentAssetView(row model.ShortplayAsset) shortplayAgentAsset {
	return shortplayAgentAsset{
		ID: row.ID, Kind: row.Kind, Name: row.Name, Aliases: shortplay.SplitAliasesJSON(row.AliasesJSON),
		Description: row.Description, Appearance: row.Appearance, Voice: row.Voice, Locked: row.Locked,
		Revision: row.Revision, DerivedFrom: row.DerivedFrom, VariantName: row.VariantName,
		ImageStatus: row.ImageStatus, ImageVersion: row.ImageVersion, ImageResource: row.ImageResourceID,
		AudioStatus: row.AudioStatus, AudioVersion: row.AudioVersion, AudioResource: row.AudioResourceID,
		EpisodeNumbers: shortplay.SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON),
	}
}

func shortplayAgentAssetMatches(row model.ShortplayAsset, query string) bool {
	if strings.Contains(strings.ToLower(row.Name), query) {
		return true
	}
	for _, alias := range shortplay.SplitAliasesJSON(row.AliasesJSON) {
		if strings.Contains(strings.ToLower(alias), query) {
			return true
		}
	}
	return false
}
