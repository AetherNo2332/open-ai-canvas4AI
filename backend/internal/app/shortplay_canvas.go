package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W06 集画布：每集建档时就分配了 canvasId，这里在第一次「进精修画布」时按需建出画布行。
//
// 画布行 ProjectID 必须写容器项目 ID：空着会退化成自由画布，短剧资产卡与同步全部失效。
// 同一集被同时打开两次只会建出一张画布（创建走 OnConflict DoNothing，冲突按「已存在」）。

const (
	shortplayStoryInputWidth  = 420
	shortplayStoryInputHeight = 260
	shortplayStoryInputX      = 80
	shortplayStoryInputY      = 120
)

// EnsureShortplayEpisodeCanvas 建出（或读取已有的）某集画布；created=false 表示早就存在。
func (s *Service) EnsureShortplayEpisodeCanvas(userID, idOrProject, episodeID string) (shortplay.EnsureCanvasResult, error) {
	owner, err := s.shortplayRowForUser(userID, idOrProject)
	if err != nil {
		return shortplay.EnsureCanvasResult{}, err
	}
	episode, err := s.repo.ShortplayEpisodeByID(owner.ID, strings.TrimSpace(episodeID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.EnsureCanvasResult{}, NewAppError(http.StatusNotFound, "集不存在")
	}
	if err != nil {
		return shortplay.EnsureCanvasResult{}, err
	}
	if strings.TrimSpace(episode.CanvasID) == "" {
		return shortplay.EnsureCanvasResult{}, NewAppError(http.StatusConflict, "这一集没有分配画布地址，请重新建档")
	}
	if _, err := s.repo.CanvasProjectForUser(userID, episode.CanvasID); err == nil {
		return shortplay.EnsureCanvasResult{CanvasID: episode.CanvasID, Created: false}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.EnsureCanvasResult{}, err
	}
	now := time.Now()
	canvas := model.CanvasProject{
		ID:          episode.CanvasID,
		UserID:      userID,
		ProjectID:   owner.ProjectID,
		Title:       episode.Title,
		PayloadJSON: shortplayCanvasPayloadJSON(newID(), episode.Title, episode.ScriptText, now),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.UpsertCanvasProject(&canvas); err != nil {
		// 并发建同一张画布：另一路已经建好了，按「已存在」返回而不是报错。
		if errors.Is(err, repository.ErrCanvasRevisionConflict) {
			if _, findErr := s.repo.CanvasProjectForUser(userID, episode.CanvasID); findErr == nil {
				return shortplay.EnsureCanvasResult{CanvasID: episode.CanvasID, Created: false}, nil
			}
		}
		return shortplay.EnsureCanvasResult{}, err
	}
	return shortplay.EnsureCanvasResult{CanvasID: episode.CanvasID, Created: true}, nil
}

// ShortplayByCanvas 按画布反查集归属；found=false 表示自由画布，调用方按原逻辑处理。
func (s *Service) ShortplayByCanvas(userID, canvasID string) (shortplay.CanvasBinding, bool, error) {
	canvasID = strings.TrimSpace(canvasID)
	if canvasID == "" {
		return shortplay.CanvasBinding{}, false, nil
	}
	canvas, err := s.repo.CanvasProjectForUser(userID, canvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.CanvasBinding{}, false, nil
	}
	if err != nil {
		return shortplay.CanvasBinding{}, false, err
	}
	if strings.TrimSpace(canvas.ProjectID) == "" {
		return shortplay.CanvasBinding{}, false, nil
	}
	owner, err := s.repo.ShortplayByProject(canvas.ProjectID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.CanvasBinding{}, false, nil
	}
	if err != nil {
		return shortplay.CanvasBinding{}, false, err
	}
	if owner.UserID != userID {
		return shortplay.CanvasBinding{}, false, nil
	}
	episode, err := s.repo.ShortplayEpisodeByCanvas(canvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.CanvasBinding{}, false, nil
	}
	if err != nil {
		return shortplay.CanvasBinding{}, false, err
	}
	if episode.ShortplayID != owner.ID {
		return shortplay.CanvasBinding{}, false, nil
	}
	return shortplay.CanvasBinding{
		ShortplayID: owner.ID, ProjectID: owner.ProjectID,
		EpisodeID: episode.ID, EpisodeNumber: episode.Number, EpisodeTitle: episode.Title,
	}, true, nil
}

// shortplayCanvasPayloadJSON 是集画布的初始文档：一个「故事输入」文本节点，内容是本集原文。
// 键集合对齐前端 CanvasProject 的必填字段；connections 不持久化，关系由字段投影。
func shortplayCanvasPayloadJSON(nodeID, title, scriptText string, now time.Time) string {
	stamp := now.UTC().Format("2006-01-02T15:04:05.000Z")
	node := shortplayCanvasNode{
		ID: nodeID, Type: "text", Title: title, CreatedAt: stamp, UpdatedAt: stamp,
		Position: shortplayCanvasPosition{X: shortplayStoryInputX, Y: shortplayStoryInputY},
		Width:    shortplayStoryInputWidth, Height: shortplayStoryInputHeight,
		Metadata: shortplayStoryInputMetadata{
			Content: scriptText, Status: "idle", FontSize: 14,
			WorkflowKind: "story_input", WorkflowTitle: "故事输入", WorkflowDescription: "题材、角色、冲突和结局方向",
		},
	}
	payload := shortplayCanvasPayload{
		Nodes:                  []shortplayCanvasNode{node},
		Connections:            []any{},
		ChatSessions:           []any{},
		Appearance:             shortplayDefaultAppearance(),
		BackgroundMode:         "dots",
		ShowImageInfo:          false,
		PrevisScenes:           []any{},
		ReferenceFieldsVersion: 1,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return `{"nodes":[],"connections":[],"chatSessions":[],"backgroundMode":"dots","showImageInfo":false,"previsScenes":[],"referenceFieldsVersion":1}`
	}
	return string(raw)
}

type shortplayCanvasPosition struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type shortplayStoryInputMetadata struct {
	Content             string `json:"content"`
	Status              string `json:"status"`
	FontSize            int    `json:"fontSize"`
	WorkflowKind        string `json:"workflowKind"`
	WorkflowTitle       string `json:"workflowTitle"`
	WorkflowDescription string `json:"workflowDescription"`
}

type shortplayCanvasNode struct {
	ID        string                      `json:"id"`
	Type      string                      `json:"type"`
	Title     string                      `json:"title"`
	CreatedAt string                      `json:"createdAt"`
	UpdatedAt string                      `json:"updatedAt"`
	Position  shortplayCanvasPosition     `json:"position"`
	Width     int                         `json:"width"`
	Height    int                         `json:"height"`
	Metadata  shortplayStoryInputMetadata `json:"metadata"`
}

type shortplayCanvasPayload struct {
	Nodes                  []shortplayCanvasNode `json:"nodes"`
	Connections            []any                 `json:"connections"`
	ChatSessions           []any                 `json:"chatSessions"`
	ActiveChatID           *string               `json:"activeChatId"`
	Appearance             map[string]any        `json:"appearance"`
	BackgroundMode         string                `json:"backgroundMode"`
	ShowImageInfo          bool                  `json:"showImageInfo"`
	PrevisScenes           []any                 `json:"previsScenes"`
	ReferenceFieldsVersion int                   `json:"referenceFieldsVersion"`
}

// shortplayDefaultAppearance 与自由画布新建时的默认外观一致：浅色预设 + 主题记忆。
func shortplayDefaultAppearance() map[string]any {
	preset := map[string]any{
		"id": "light",
		"values": map[string]any{
			"baseTheme": "light", "backgroundColor": "#F0F0F0", "backgroundBrightness": 0,
			"gridColor": "#000000", "gridOpacity": 23,
		},
	}
	return map[string]any{
		"mode":        "preset",
		"preset":      preset,
		"themeMemory": map[string]any{"light": map[string]any{"mode": "preset", "preset": preset}},
	}
}
