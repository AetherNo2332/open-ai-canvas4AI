package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W03 分集工作台：建档（容器 + 域配置 + N 条集行）、列表与详情。
// 建档不建画布、不入队提取；这两步由详情页显式触发（W04/W06 接入）。
// 设置保存见 shortplay_settings.go；业务校验与归属检查全部在本层完成。

// CreateShortplay 校验建档请求并一次性写入容器行、域配置与全部集行。
// 同名冲突（projects 的唯一索引）返回 409，整个事务回滚，不留半成品。
func (s *Service) CreateShortplay(userID string, req shortplay.CreateShortplayRequest) (shortplay.ShortplaySummary, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return shortplay.ShortplaySummary{}, BadAuthRequest("短剧名称不能为空")
	}
	if utf8.RuneCountInString(name) > shortplay.NameRunes {
		return shortplay.ShortplaySummary{}, BadAuthRequest("短剧名称不能超过 80 字")
	}
	if len(req.Chapters) == 0 || len(req.Chapters) > shortplay.MaxEpisodes {
		return shortplay.ShortplaySummary{}, BadAuthRequest(fmt.Sprintf("集数必须在 1 到 %d 之间", shortplay.MaxEpisodes))
	}
	aspectRatio := strings.TrimSpace(req.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = production.AspectRatios[0]
	}
	if !production.ValidAspectRatio(aspectRatio) {
		return shortplay.ShortplaySummary{}, BadAuthRequest("不支持的画幅：" + aspectRatio)
	}
	settingsJSON := strings.TrimSpace(req.SettingsJSON)
	if settingsJSON == "" {
		settingsJSON = "{}"
	}
	// 容器行的默认图片模型是设置里的投影；请求显式带的只在设置没选时兜底。
	imageModel := shortplayDefaultImageModelText(parseShortplaySettings(settingsJSON).Models.normalized())
	if imageModel == "" {
		imageModel = strings.TrimSpace(req.DefaultImageModel)
	}

	now := time.Now()
	project := model.Project{
		ID:                newID(),
		UserID:            userID,
		Name:              name,
		Type:              shortplay.ProjectType,
		AspectRatio:       aspectRatio,
		SourceType:        production.SourceScript,
		DefaultImageModel: imageModel,
		DefaultVideoModel: strings.TrimSpace(req.DefaultVideoModel),
		Status:            model.ProjectStatusActive,
		Revision:          1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	shortplayID := newID()

	texts := make([]string, 0, len(req.Chapters))
	episodes := make([]model.ShortplayEpisode, 0, len(req.Chapters))
	summaries := make([]shortplay.EpisodeSummary, 0, len(req.Chapters))
	totalRunes := 0
	for index, input := range req.Chapters {
		// 先去 \r\n 再算字数，最后才判断总量非空；超上限直接拒绝，不截断丢台词。
		text := strings.TrimSpace(strings.ReplaceAll(input.Text, "\r\n", "\n"))
		runes := utf8.RuneCountInString(text)
		if runes > production.ScriptSourceChapterRunes {
			return shortplay.ShortplaySummary{}, BadAuthRequest(fmt.Sprintf(
				"第 %d 集原文 %d 字，单集不超过 %d 字，请在集标题处拆开",
				index+1, runes, production.ScriptSourceChapterRunes,
			))
		}
		totalRunes += runes
		title := strings.TrimSpace(input.Title)
		if title == "" {
			title = fmt.Sprintf("第 %d 集", index+1)
		}
		title = truncateRunes(title, 200)

		// 画布地址先分配、画布行后建：建档 N 集不落 N 份 payload。
		episodeID, canvasID := newID(), newID()
		texts = append(texts, text)
		episodes = append(episodes, model.ShortplayEpisode{
			ID:           episodeID,
			ShortplayID:  shortplayID,
			Number:       index + 1,
			Title:        title,
			ScriptText:   text,
			CanvasID:     canvasID,
			AssetIDsJSON: "[]",
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		summaries = append(summaries, shortplay.EpisodeSummary{
			ID:        episodeID,
			Number:    index + 1,
			Title:     title,
			CanvasID:  canvasID,
			WordCount: runes,
			AssetIDs:  []int{},
		})
	}
	if totalRunes == 0 {
		return shortplay.ShortplaySummary{}, BadAuthRequest("原文为空")
	}

	// sourceRaw 只作整篇留档，工作副本在集上；按集拼接保留分集位置。
	domainRow := model.Shortplay{
		ID:           shortplayID,
		ProjectID:    project.ID,
		SourceRaw:    strings.Join(texts, "\n\n"),
		SettingsJSON: settingsJSON,
		UserID:       userID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.CreateShortplayProject(&project, &domainRow, episodes); err != nil {
		if errors.Is(err, repository.ErrShortplayNameTaken) || errors.Is(err, repository.ErrProjectNameConflict) {
			conflict := NewAppError(http.StatusConflict, "已有同名作品，请换一个名称")
			conflict.Reason = ReasonProjectNameConflict
			return shortplay.ShortplaySummary{}, conflict
		}
		return shortplay.ShortplaySummary{}, err
	}
	return shortplay.ShortplaySummary{ProjectID: project.ID, ShortplayID: shortplayID, Episodes: summaries}, nil
}

// ListShortplays 返回账号下的短剧列表：容器行 + 集数聚合，按更新时间倒序。
func (s *Service) ListShortplays(userID string) ([]shortplay.ShortplayListItem, error) {
	rows, err := s.repo.ListShortplayItems(userID)
	if err != nil {
		return nil, err
	}
	items := make([]shortplay.ShortplayListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, shortplay.ShortplayListItem{
			ProjectID:    row.ProjectID,
			ShortplayID:  row.ShortplayID,
			Name:         row.Name,
			AspectRatio:  row.AspectRatio,
			EpisodeCount: row.EpisodeCount,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
		})
	}
	return items, nil
}

// ShortplayDetail 返回容器、设置、全部集与最近 job。参数是项目 ID（与 /dramas/:id 同口径）。
func (s *Service) ShortplayDetail(userID, projectID string) (shortplay.ShortplayDetail, error) {
	project, err := s.repo.ProjectForUser(userID, strings.TrimSpace(projectID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return shortplay.ShortplayDetail{}, NewAppError(http.StatusNotFound, "短剧不存在")
		}
		return shortplay.ShortplayDetail{}, err
	}
	if project.Type != shortplay.ProjectType {
		return shortplay.ShortplayDetail{}, NewAppError(http.StatusNotFound, "该项目不是短剧作品")
	}
	row, err := s.repo.ShortplayByProject(project.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return shortplay.ShortplayDetail{}, NewAppError(http.StatusNotFound, "该项目不是短剧作品")
		}
		return shortplay.ShortplayDetail{}, err
	}
	episodes, err := s.repo.ShortplayEpisodes(row.ID)
	if err != nil {
		return shortplay.ShortplayDetail{}, err
	}
	jobs, err := s.repo.ShortplayJobs(row.ID, shortplay.JobSummaryLimit)
	if err != nil {
		return shortplay.ShortplayDetail{}, err
	}
	if jobs == nil {
		jobs = []model.ShortplayJob{}
	}
	items := make([]shortplay.EpisodeSummary, 0, len(episodes))
	for _, episode := range episodes {
		items = append(items, shortplay.EpisodeSummary{
			ID:         episode.ID,
			Number:     episode.Number,
			Title:      episode.Title,
			CanvasID:   episode.CanvasID,
			WordCount:  utf8.RuneCountInString(episode.ScriptText),
			ScriptText: episode.ScriptText,
			AssetIDs:   shortplayIntList(episode.AssetIDsJSON),
		})
	}
	return shortplay.ShortplayDetail{
		ProjectID:         project.ID,
		ShortplayID:       row.ID,
		Name:              project.Name,
		AspectRatio:       project.AspectRatio,
		DefaultImageModel: project.DefaultImageModel,
		DefaultVideoModel: project.DefaultVideoModel,
		SettingsJSON:      row.SettingsJSON,
		CreatedAt:         project.CreatedAt,
		UpdatedAt:         project.UpdatedAt,
		Episodes:          items,
		Jobs:              jobs,
	}, nil
}

// shortplayRowForUser 按 shortplayId 或 projectId 反查域配置并校验归属。
// 页面拿项目 ID、域内调用拿短剧 ID，两种口径都接受；跨账号一律 404。
func (s *Service) shortplayRowForUser(userID, idOrProject string) (*model.Shortplay, error) {
	id := strings.TrimSpace(idOrProject)
	if id == "" {
		return nil, NewAppError(http.StatusNotFound, "短剧不存在")
	}
	row, err := s.repo.ShortplayByID(id)
	if err == nil {
		if row.UserID != userID {
			return nil, NewAppError(http.StatusNotFound, "短剧不存在")
		}
		return row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	row, err = s.repo.ShortplayByProject(id)
	if err == nil {
		if row.UserID != userID {
			return nil, NewAppError(http.StatusNotFound, "短剧不存在")
		}
		return row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return nil, NewAppError(http.StatusNotFound, "短剧不存在")
}

// shortplayIntList 读集行的 assetIdsJSON；非法 JSON 返回空切片。
func shortplayIntList(raw string) []int {
	if strings.TrimSpace(raw) == "" {
		return []int{}
	}
	var values []int
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil {
		return []int{}
	}
	return values
}
