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
)

const (
	dramaMaxChapters      = 3000
	dramaMaxChapterRunes  = 60000
	dramaMaxNovelRunes    = 6_000_000
	dramaInboxShotLimit   = 60
	dramaEpisodeJobsLimit = 40
)

// dramaCreation 是一次建档要写入的全部行：项目、原著章节、制作控制面与按章分集时直接建好的集。
type dramaCreation struct {
	project    model.Project
	chapters   []model.ProjectUnit
	production model.DramaProduction
	episodes   []model.DramaEpisode
}

// prepareDramaCreation 校验创建请求并组装要写入的行；新建作品与重存草稿共用这一份校验。
func (s *Service) prepareDramaCreation(userID string, projectID string, req CreateDramaRequest) (*dramaCreation, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, BadAuthRequest("作品名称不能为空")
	}
	if utf8.RuneCountInString(name) > 80 {
		return nil, BadAuthRequest("作品名称不能超过 80 字")
	}
	if len(req.Chapters) == 0 || len(req.Chapters) > dramaMaxChapters {
		return nil, BadAuthRequest("章节数量必须在 1 到 3000 之间")
	}
	if req.BudgetMicrocredits < 0 {
		return nil, BadAuthRequest("预算不能为负数")
	}
	if req.Settings.MediaMode == "" {
		// 新作品默认先只跑文本：先看剧情、分镜与资产，确认后再花钱生图。
		req.Settings.MediaMode = DramaMediaManual
	}
	format := production.NormalizeSourceFormat(req.SourceFormat)
	// 分集方式随设置保存；请求顶层的 episodeMode 只在设置里没写时使用。
	if req.Settings.EpisodeMode == "" {
		req.Settings.EpisodeMode = firstNonEmpty(req.EpisodeMode, DramaEpisodeModeAuto)
	}
	// 剧本与分镜本身就是按集写好的，总是每章一集。
	if format != production.SourceNovel {
		req.Settings.EpisodeMode, req.Settings.ChaptersPerEpisode = DramaEpisodeModeChapter, 1
	}
	settings := req.Settings.normalized()
	settings, err := s.prepareDramaCreationSettings(userID, settings, req.Start)
	if err != nil {
		return nil, err
	}
	selected, err := dramaSelectedChapters(req.SelectedChapters, len(req.Chapters))
	if err != nil {
		return nil, err
	}
	// 按章分集时建档即写好各集；交给模型分集时由调度循环分批规划。
	perChapter := settings.EpisodeMode == DramaEpisodeModeChapter
	now := time.Now()
	project := model.Project{ID: projectID, UserID: userID, Name: name, Type: "short-drama", AspectRatio: settings.AspectRatio, SourceType: format, Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
	chapters, produced, err := buildDramaCreationChapters(req, project, settings, selected, perChapter, now)
	if err != nil {
		return nil, err
	}
	status := model.DramaStatusDraft
	if req.Start {
		status = model.DramaStatusRunning
	}
	productionRow := model.DramaProduction{ProjectID: project.ID, UserID: userID, Status: status, Stage: model.DramaStageBible, SettingsJSON: encodeDramaSettings(settings), BudgetMicrocredits: req.BudgetMicrocredits, Revision: 1, CreatedAt: now, UpdatedAt: now}
	var episodes []model.DramaEpisode
	if perChapter {
		// 按章分组直接建集，分集规划视为已完成；建档后各集按窗口依次写剧本。游标只数加入制作的章节。
		productionRow.PlanCursor, productionRow.PlanDone = len(produced), true
		episodes, err = groupDramaChapterEpisodes(project.ID, 1, produced, settings.ChaptersPerEpisode, format, now)
		if err != nil {
			return nil, err
		}
	}
	draftJSON := ""
	if !req.Start && req.Draft != nil {
		form := *req.Draft
		form.SourceText = strings.TrimSpace(form.SourceText)
		if utf8.RuneCountInString(form.SourceText) > dramaMaxNovelRunes*2 {
			return nil, BadAuthRequest("草稿原文过长")
		}
		form.StyleID = truncateRunes(strings.TrimSpace(form.StyleID), 80)
		encoded, err := json.Marshal(form)
		if err != nil {
			return nil, err
		}
		draftJSON = string(encoded)
	}
	productionRow.DraftJSON = draftJSON
	return &dramaCreation{project: project, chapters: chapters, production: productionRow, episodes: episodes}, nil
}

// CreateDrama 一次性写入项目、小说章节与制作控制面；start=true 时立即进入自动制作，否则保存为草稿。
func (s *Service) CreateDrama(userID string, req CreateDramaRequest) (DramaSummary, error) {
	creation, err := s.prepareDramaCreation(userID, newID(), req)
	if err != nil {
		return DramaSummary{}, err
	}
	if err := s.repo.CreateDramaProject(&creation.project, creation.chapters, &creation.production, creation.episodes...); err != nil {
		if errors.Is(err, repository.ErrProjectNameConflict) {
			return DramaSummary{}, dramaNameConflict()
		}
		return DramaSummary{}, err
	}
	s.wakeDramaScheduler()
	return s.DramaSummary(userID, creation.project.ID)
}

// SaveDramaDraft 用创建页的完整表单整体替换一个从未开工的草稿：名称、原文章节、选章、设置与预算；
// start=true 时替换后直接开工。已开工（状态不再是草稿或已有作业）的作品拒绝，避免覆盖已产出的内容。
func (s *Service) SaveDramaDraft(userID string, projectID string, req CreateDramaRequest) (DramaSummary, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaSummary{}, err
	}
	if productionRow.Status != model.DramaStatusDraft {
		return DramaSummary{}, BadAuthRequest("作品已开始制作，不能再按草稿修改")
	}
	creation, err := s.prepareDramaCreation(userID, project.ID, req)
	if err != nil {
		return DramaSummary{}, err
	}
	creation.project.CreatedAt = project.CreatedAt
	creation.production.CreatedAt = productionRow.CreatedAt
	if err := s.repo.ReplaceDramaDraft(&creation.project, creation.chapters, &creation.production, creation.episodes); err != nil {
		switch {
		case errors.Is(err, repository.ErrProjectNameConflict):
			return DramaSummary{}, dramaNameConflict()
		case errors.Is(err, repository.ErrDramaDraftStarted):
			return DramaSummary{}, BadAuthRequest("作品已开始制作，不能再按草稿修改")
		}
		return DramaSummary{}, err
	}
	s.wakeDramaScheduler()
	return s.DramaSummary(userID, project.ID)
}

// DramaDraft 返回草稿的创建表单，供创建页回填；只有带表单的草稿可读。
func (s *Service) DramaDraft(userID string, projectID string) (DramaDraftView, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaDraftView{}, err
	}
	if productionRow.Status != model.DramaStatusDraft || productionRow.DraftJSON == "" {
		return DramaDraftView{}, BadAuthRequest("这个作品没有可编辑的草稿")
	}
	var form DramaDraftForm
	if err := json.Unmarshal([]byte(productionRow.DraftJSON), &form); err != nil {
		return DramaDraftView{}, err
	}
	outline, err := s.repo.DramaChapterOutline(project.ID)
	if err != nil {
		return DramaDraftView{}, err
	}
	selected := make([]int, 0, len(outline))
	for _, chapter := range outline {
		if chapter.Status != model.ProjectUnitStatusReserved {
			selected = append(selected, chapter.Position)
		}
	}
	return DramaDraftView{
		ProjectID: project.ID, Name: project.Name, SourceFormat: production.NormalizeSourceFormat(project.SourceType),
		Settings: parseDramaSettings(productionRow.SettingsJSON), BudgetMicrocredits: productionRow.BudgetMicrocredits,
		SelectedChapters: selected, ChapterCount: len(outline), Form: form,
	}, nil
}

func dramaNameConflict() error {
	conflict := NewAppError(http.StatusConflict, "已有同名作品，请换一个名称")
	conflict.Reason = ReasonProjectNameConflict
	return conflict
}

// newDramaChapterEpisode 把一章原文直接建成一集；梗概、钩子和悬念留空，由剧本阶段按原文补写。
func newDramaChapterEpisode(projectID string, number int, chapter model.ProjectUnit, format string, now time.Time) model.DramaEpisode {
	return model.DramaEpisode{ID: newID(), ProjectID: projectID, Number: number, Title: truncateRunes(chapter.Title, 60), ChapterIDsJSON: encodeStringList([]string{chapter.ID}), SourceFormat: format, Stage: model.DramaEpisodeQueued, Revision: 1, CreatedAt: now, UpdatedAt: now}
}

// dramaSelectedChapters 校验导入时先加入制作的章节下标；为空表示全部章节。
func dramaSelectedChapters(indexes []int, total int) (map[int]bool, error) {
	selected := make(map[int]bool, total)
	if len(indexes) == 0 {
		for index := 0; index < total; index++ {
			selected[index] = true
		}
		return selected, nil
	}
	for _, index := range indexes {
		if index < 0 || index >= total {
			return nil, BadAuthRequest("选择的章节不存在，请重新选择")
		}
		selected[index] = true
	}
	return selected, nil
}

// groupDramaChapterEpisodes 按章节顺序每 perEpisode 章合成一集，编号从 startNumber 起；不相邻的章节（中间有未加入制作的章）
// 不合进同一集。合并后单集原文超过上限时拒绝，提示减少每集章数。章节只需 ID、标题、位置与字数。
func groupDramaChapterEpisodes(projectID string, startNumber int, chapters []model.ProjectUnit, perEpisode int, format string, now time.Time) ([]model.DramaEpisode, error) {
	perEpisode = max(1, min(dramaMaxChaptersPerEpisode, perEpisode))
	episodes := make([]model.DramaEpisode, 0, (len(chapters)+perEpisode-1)/perEpisode)
	for start := 0; start < len(chapters); {
		end := start + 1
		for end < len(chapters) && end-start < perEpisode && chapters[end].Position == chapters[end-1].Position+1 {
			end++
		}
		group := chapters[start:end]
		start = end
		if len(group) == 1 {
			episodes = append(episodes, newDramaChapterEpisode(projectID, startNumber+len(episodes), group[0], format, now))
			continue
		}
		ids, runes := make([]string, 0, len(group)), 0
		for _, chapter := range group {
			ids = append(ids, chapter.ID)
			runes += chapter.WordCount
		}
		first, last := group[0].Position+1, group[len(group)-1].Position+1
		if runes > dramaEpisodeSourceRunes {
			return nil, BadAuthRequest(fmt.Sprintf("第 %d–%d 章合计 %d 字，每集原文不超过 %d 字，请减少每集章数", first, last, runes, dramaEpisodeSourceRunes))
		}
		title := truncateRunes(group[0].Title, 40) + fmt.Sprintf(" 等 %d 章", len(group))
		episodes = append(episodes, model.DramaEpisode{ID: newID(), ProjectID: projectID, Number: startNumber + len(episodes), Title: truncateRunes(title, 60), ChapterIDsJSON: encodeStringList(ids), SourceFormat: format, Stage: model.DramaEpisodeQueued, Revision: 1, CreatedAt: now, UpdatedAt: now})
	}
	return episodes, nil
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func buildDramaCreationChapters(req CreateDramaRequest, project model.Project, settings DramaSettings, selected map[int]bool, perChapter bool, now time.Time) ([]model.ProjectUnit, []model.ProjectUnit, error) {
	chapters := make([]model.ProjectUnit, 0, len(req.Chapters))
	produced := make([]model.ProjectUnit, 0, len(req.Chapters))
	totalRunes, producedRunes := 0, 0
	for index, input := range req.Chapters {
		text := strings.TrimSpace(strings.ReplaceAll(input.Text, "\r\n", "\n"))
		runes := utf8.RuneCountInString(text)
		if runes > dramaMaxChapterRunes {
			return nil, nil, BadAuthRequest("单章不能超过 6 万字，请检查章节拆分")
		}
		// 剧本提示词每章最多读取这么多字，超出会被截断而丢台词，所以无论哪种分集方式都直接拒绝。
		// 预留章节暂不改编，加入制作时再检查。
		if selected[index] && runes > production.ScriptSourceChapterRunes {
			label := "章"
			if perChapter && settings.ChaptersPerEpisode == 1 {
				label = "集"
			}
			return nil, nil, BadAuthRequest(fmt.Sprintf("第 %d %s原文 %d 字，单章不超过 %d 字，请在章节标题处拆开", index+1, label, runes, production.ScriptSourceChapterRunes))
		}
		totalRunes += runes
		title := strings.TrimSpace(input.Title)
		if title == "" {
			title = "第 " + itoa(index+1) + " 章"
		}
		status := model.ProjectUnitStatusReserved
		if selected[index] {
			status = model.ProjectUnitStatusReady
		}
		chapter := model.ProjectUnit{ID: newID(), ProjectID: project.ID, Kind: model.ProjectUnitKindChapter, Title: truncateRunes(title, 200), SourceText: text, WordCount: runes, Status: status, Position: index, CreatedAt: now, UpdatedAt: now}
		chapters = append(chapters, chapter)
		if selected[index] {
			produced = append(produced, chapter)
			producedRunes += runes
		}
	}
	if totalRunes == 0 || producedRunes == 0 {
		return nil, nil, BadAuthRequest("原文为空")
	}
	if totalRunes > dramaMaxNovelRunes {
		return nil, nil, BadAuthRequest("原文不能超过 600 万字")
	}
	return chapters, produced, nil
}

func (s *Service) prepareDramaCreationSettings(userID string, settings DramaSettings, start bool) (DramaSettings, error) {
	if err := settings.TextThinking.validate(); err != nil {
		return DramaSettings{}, err
	}
	if !production.ValidAspectRatio(settings.AspectRatio) {
		return DramaSettings{}, BadAuthRequest("不支持的画幅：" + settings.AspectRatio)
	}
	if problem := production.ValidateAssetPrompts(settings.Assets); problem != "" {
		return DramaSettings{}, BadAuthRequest(problem)
	}
	if err := s.validateDramaSkills(userID, settings); err != nil {
		return DramaSettings{}, err
	}
	if err := s.validateDramaModelOverrides(userID, settings.Models); err != nil {
		return DramaSettings{}, err
	}
	models, err := s.resolveDramaModels(userID, settings)
	if err != nil {
		return DramaSettings{}, err
	}
	settings.Models = models
	if err := s.validateDramaImageSettings(models, settings.Assets); err != nil {
		return DramaSettings{}, err
	}
	if err := s.validateDramaVideoSettings(settings); err != nil {
		return DramaSettings{}, err
	}
	if start {
		if missing := missingDramaCapabilities(models); len(missing) > 0 {
			return DramaSettings{}, BadAuthRequest("还没有可用的" + strings.Join(missing, "、") + "模型，请联系管理员开通后再开始制作")
		}
	}
	return settings, nil
}
