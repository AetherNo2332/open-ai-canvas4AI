package app

import (
	"errors"
	"fmt"
	"gorm.io/gorm"
	"math"
	"slices"
	"strconv"
	"strings"
	"yingce/backend/internal/production"
)

var dramaCapabilities = []string{"text", "image", "video"}

// dramaModelIntent 是短剧流水线对各能力的最低要求：参考图走文生图；视频只要求能生成视频，
// 生成方式（尾帧续接需要图生视频，并行出片只需参考图生视频或文生视频）由 dramaVideoProfile 按能力合同判断。
func dramaModelIntent(capability string) *ModelRequestIntent {
	switch capability {
	case "image":
		return &ModelRequestIntent{Capability: "image", Operation: "text_to_image"}
	case "video":
		return &ModelRequestIntent{Capability: "video"}
	}
	return &ModelRequestIntent{Capability: "text"}
}

// capabilityMatchNeedsImage 判断能力不匹配里是否有图片输入数量不足（模型声明了最少参考图数）。
func capabilityMatchNeedsImage(match CapabilityMatch) bool {
	for _, issue := range match.Issues {
		if issue.Kind == "input" && issue.Name == "image" {
			return true
		}
	}
	return false
}

// 目录尚未组装剧情段素材；同时接受纯文本和单张参考图入口，实际素材数量在提交时校验。
func (s *Service) resolveDramaLogicalModel(access *userGroupAccess, id, capability string) (*RoutedModel, error) {
	intent := *dramaModelIntent(capability)
	routed, err := s.ResolveLogicalModel(access, id, intent)
	if err != nil && capability == "video" {
		intent.Inputs = map[string]int{"image": 1}
		return s.ResolveLogicalModel(access, id, intent)
	}
	return routed, err
}

// dramaSegmentOperation 是剧情段视频在该衔接方式下的主要生成方式，用于报价与能力校验。
func dramaSegmentOperation(settings DramaSettings, profile dramaVideoProfile) (string, map[string]int) {
	if !settings.parallelVideo() {
		return "image_to_video", map[string]int{"image": 1}
	}
	if profile.supports("reference_to_video") {
		return "reference_to_video", map[string]int{"image": 1}
	}
	return "text_to_video", nil
}

// validateDramaVideoSettings 在保存设置时确认视频模型能按作品画幅出片，并支持所选的段落衔接方式。
func (s *Service) validateDramaVideoSettings(settings DramaSettings) error {
	if settings.Models.Video.Empty() {
		return nil
	}
	profile, err := s.dramaVideoProfile(settings.Models.Video)
	if err != nil {
		return BadAuthRequest("视频模型不可用于短剧：" + err.Error())
	}
	if issue := profile.screen(settings.AspectRatio, settings.VideoResolution).Issue; issue != "" {
		return BadAuthRequest(issue)
	}
	if issue := profile.continuityIssue(settings); issue != "" {
		return BadAuthRequest(issue)
	}
	return nil
}

// dramaModelOptions 列出某能力下的平台模型：可由服务端无人值守执行的进入 options，
// 其余带原因进入 unavailable。候选与创作端目录同源（不带意图过滤），避免“别处能选、这里看不到”。
func (s *Service) dramaModelOptions(userID string, capability string) ([]DramaModelOption, []DramaModelUnavailable, error) {
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, nil, err
	}
	options := []DramaModelOption{}
	unavailable := []DramaModelUnavailable{}
	seen := map[string]bool{}
	add := func(choice DramaModelChoice, key, label, priceLabel string, available bool) {
		if seen[key] {
			return
		}
		seen[key] = true
		reason := ""
		if !available {
			reason = "模型暂不可用或未配置有效价格"
		} else {
			reason = s.dramaChoiceIssue(access, choice, capability)
		}
		if reason != "" {
			unavailable = append(unavailable, DramaModelUnavailable{Choice: choice, Reason: reason})
			return
		}
		options = append(options, s.dramaModelOption(choice, capability, label, priceLabel))
	}
	frontendEnabled, err := s.FeatureEnabled(FeatureFrontendModels)
	if err != nil {
		return nil, nil, err
	}
	if frontendEnabled {
		models, err := s.PublicLogicalModels(access, nil)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range models {
			if normalizeCapability(item.Capability) != capability {
				continue
			}
			add(DramaModelChoice{LogicalModelID: item.ID, Label: item.Name, PriceLabel: item.PriceLabel}, "l:"+item.ID, item.Name, item.PriceLabel, item.Available)
		}
	}
	catalog, err := s.ModelCatalog(access, nil)
	if err != nil {
		return nil, nil, err
	}
	for _, channel := range catalog.Channels {
		for _, item := range channel.Models {
			if normalizeCapability(item.Capability) != capability {
				continue
			}
			label := firstNonEmpty(item.DisplayName, item.ModelKey)
			add(DramaModelChoice{ChannelID: channel.ID, ModelKey: item.ModelKey, Label: label, PriceLabel: item.PriceLabel}, "c:"+channel.ID+":"+item.ModelKey, label, item.PriceLabel, item.Available)
		}
	}
	return options, unavailable, nil
}

// dramaChoiceIssue 返回模型不能用于短剧的原因；空串表示可用。
func (s *Service) dramaChoiceIssue(access *userGroupAccess, choice DramaModelChoice, capability string) string {
	intent := dramaModelIntent(capability)
	if choice.LogicalModelID != "" {
		if _, err := s.resolveDramaLogicalModel(access, choice.LogicalModelID, capability); err != nil {
			return dramaIntentIssue(capability, err.Error())
		}
	} else {
		row, err := s.repo.ChannelModelByKey(choice.ChannelID, choice.ModelKey)
		if err != nil {
			return "模型不存在或已下架"
		}
		if normalizeCapability(row.Capability) != "audio" {
			config, err := normalizedChannelModelCapability(row, s.protocolRegistry())
			if err != nil {
				return "模型能力配置无效：" + err.Error()
			}
			spec, err := CapabilitySpecFromModelCapabilityConfig(config, row.Capability)
			if err != nil {
				return "模型能力配置无效：" + err.Error()
			}
			match := MatchCapability(spec, *intent)
			// 只在缺参考图时改按一张参考图再匹配；其他原因不重试，避免给不收图片的模型多报一条“不支持图片输入”。
			if !match.Matched && capability == "video" && capabilityMatchNeedsImage(match) {
				intent.Inputs = map[string]int{"image": 1}
				match = MatchCapability(spec, *intent)
			}
			if !match.Matched {
				return dramaIntentIssue(capability, strings.Join(match.Reasons, "；"))
			}
		}
	}
	if capability == "video" {
		profile, err := s.dramaVideoProfile(choice)
		if err != nil {
			return err.Error()
		}
		// 一个作品画幅都给不出的模型对短剧没有用；能给出部分画幅的由前端按作品画幅置灰。
		if len(profile.ratios()) == 0 {
			return profile.screen(production.AspectRatios[0], "").Issue
		}
	}
	if capability == "image" {
		// 场景与道具参考图固定横版 16:9：给不出接近尺寸的模型会把它们画成方图。
		if request := s.dramaImageRequestFor(choice, production.AssetImageRatio, ""); request.Issue != "" {
			return request.Issue
		}
	}
	return ""
}

// validateDramaImageSettings 在保存设置时确认角色、道具、场景各自的图片模型能按该类参考图的画幅与分辨率出图，
// 避免开工后每张参考图都失败。模型能力读不到时交给提交时校验。
func (s *Service) validateDramaImageSettings(models DramaModels, assets production.AssetSettings) error {
	labels := map[string]string{"character": "角色", "prop": "道具", "scene": "场景"}
	for _, kind := range []string{"character", "prop", "scene"} {
		choice := models.imageFor(kind)
		if choice.Empty() {
			continue
		}
		ratio := production.AssetImageRatio
		if kind == "character" {
			ratio = assets.CharacterRatio
		}
		if request := s.dramaImageRequestFor(choice, ratio, assets.ResolutionFor(kind)); request.Issue != "" {
			return BadAuthRequest(labels[kind] + "参考图设置与图片模型不匹配：" + request.Issue)
		}
	}
	return nil
}

// validateDramaModelOverrides 确认单独指定的步骤模型与参考图模型都在当前用户可用的短剧模型目录里，防止提交任意渠道标识。
func (s *Service) validateDramaModelOverrides(userID string, models DramaModels) error {
	for _, item := range []struct {
		capability string
		choice     DramaModelChoice
	}{{"text", models.BibleText}, {"text", models.ScriptText}, {"text", models.PromptText}, {"image", models.CharacterImage}, {"image", models.PropImage}, {"image", models.SceneImage}} {
		if item.choice.Empty() {
			continue
		}
		if err := s.requireDramaModelOption(userID, item.capability, item.choice); err != nil {
			return err
		}
	}
	return nil
}

// validateDramaSkills 确认三个文字步骤选定的技能都可读。
func (s *Service) validateDramaSkills(userID string, settings DramaSettings) error {
	for _, item := range []struct {
		label string
		ref   *DramaSkillRef
	}{{"资产提取", settings.BibleSkill}, {"分镜拆分", settings.ScriptSkill}, {"视频提示词", settings.PromptSkill}} {
		if _, err := s.dramaWritingSkill(userID, item.ref, item.label); err != nil {
			return err
		}
	}
	return nil
}

func dramaIntentIssue(capability, reason string) string {
	switch capability {
	case "image":
		return "短剧参考图需要文生图：" + reason
	case "video":
		return "短剧需要可生成视频的模型：" + reason
	}
	return reason
}

func (s *Service) DramaModelCatalog(userID string) (DramaModelCatalog, error) {
	result := DramaModelCatalog{Options: map[string][]DramaModelOption{}}
	result.Unavailable = map[string][]DramaModelUnavailable{}
	for _, capability := range dramaCapabilities {
		options, unavailable, err := s.dramaModelOptions(userID, capability)
		if err != nil {
			return result, err
		}
		result.Options[capability] = options
		result.Unavailable[capability] = unavailable
		// 新作品默认竖屏、尾帧续接；新建页按用户实际选的画幅与衔接方式重新挑选。
		defaults := DramaSettings{Settings: production.Settings{AspectRatio: production.AspectRatios[0]}, VideoContinuity: DramaContinuityTailFrame}
		if option, ok := preferredDramaOption(options, defaults); ok {
			setDramaModelChoice(&result.Defaults, capability, option.Choice)
		}
	}
	return result, nil
}

// preferredDramaOption 返回第一个可用的候选；视频模型还要能按作品画幅出片并支持作品的段落衔接方式。
// 视频模型优先选不强制参考图的：没有参考设定的剧情段（如空镜）发不出参考图，强制参考图的模型会让这些段提交失败。
// 都不满足时不替用户挑一个开工后才失败的模型，由调用方提示没有可用模型。
func preferredDramaOption(options []DramaModelOption, settings DramaSettings) (DramaModelOption, bool) {
	for _, allowRequiredReference := range []bool{false, true} {
		for _, option := range options {
			if option.Capability != "video" {
				return option, true
			}
			if (option.MinReferenceImages > 0) == allowRequiredReference && dramaVideoOptionFits(option, settings) {
				return option, true
			}
		}
	}
	return DramaModelOption{}, false
}

func dramaVideoOptionFits(option DramaModelOption, settings DramaSettings) bool {
	return (settings.parallelVideo() || option.TailFrameChain) && slices.Contains(option.VideoRatios, settings.AspectRatio) && dramaVideoResolutionOffered(option.VideoResolutions, settings.VideoResolution)
}

// dramaVideoResolutionOffered 判断作品指定的分辨率在模型可选档位里；未指定（按模型默认）总是可以。
func dramaVideoResolutionOffered(options []string, value string) bool {
	if value == "" {
		return true
	}
	key := dramaVideoResolutionKey(value)
	for _, option := range options {
		if dramaVideoResolutionKey(option) == key {
			return true
		}
	}
	return false
}

// dramaVideoResolutionKey 让 720、720p、4k、2160p 这类写法按同一档位比较。
func dramaVideoResolutionKey(value string) string {
	return strings.ToLower(normalizeVideoResolution(strings.TrimSpace(value)))
}

func setDramaModelChoice(models *DramaModels, capability string, choice DramaModelChoice) {
	switch capability {
	case "text":
		models.Text = choice
	case "image":
		models.Image = choice
	case "video":
		models.Video = choice
	}
}

// resolveDramaModels 为未选择的能力补齐默认模型；新用户无需进入任何模型配置即可启动。
// 视频模型按作品的段落衔接方式挑选（尾帧续接需要图生视频）。
func (s *Service) resolveDramaModels(userID string, settings DramaSettings) (DramaModels, error) {
	models := settings.Models
	for _, capability := range dramaCapabilities {
		if !models.choice(capability).Empty() {
			continue
		}
		options, _, err := s.dramaModelOptions(userID, capability)
		if err != nil {
			return models, err
		}
		if option, ok := preferredDramaOption(options, settings); ok {
			setDramaModelChoice(&models, capability, option.Choice)
		}
	}
	return models, nil
}

func missingDramaCapabilities(models DramaModels) []string {
	labels := map[string]string{"text": "文本", "image": "图片", "video": "视频"}
	missing := []string{}
	for _, capability := range dramaCapabilities {
		if models.choice(capability).Empty() {
			missing = append(missing, labels[capability])
		}
	}
	return missing
}

// dramaVideoProfile 是视频模型能力合同中短剧需要的部分：时长档位、支持的生成方式、参考图上限、
// 是否同步生成声音，以及换算作品画幅所需的比例与分辨率声明。
type dramaVideoProfile struct {
	Durations  production.DurationOptions
	Operations []string
	MaxImages  int
	// MinImages 是模型要求的最少参考图数（含首帧）；剧情段发不出这么多张时预演就报出，不提交。
	MinImages int
	// MaxAudios / MaxAudioSeconds 是参考音频的张数与单段时长上限（秒，0 表示未声明），用于随段发送角色声音样本。
	MaxAudios       int
	MaxAudioSeconds int
	GenerateAudio   bool
	// PromptMaxChars 是提示词字符上限（含系统追加的参考图说明），与任务准入同一份能力合同；0 表示不限。
	PromptMaxChars int
	video          *VideoCapabilityConfig
}

// screen 把作品画幅与分辨率换算成本模型的画面参数，与任务准入使用同一份能力合同；resolution 为空按模型默认。
func (p dramaVideoProfile) screen(ratio string, resolution string) dramaVideoScreen {
	return dramaVideoScreenFor(p.video, ratio, resolution)
}

// ratios 是本模型能准确给出的作品画幅（按 production.AspectRatios 的顺序）。
func (p dramaVideoProfile) ratios() []string {
	ratios := []string{}
	for _, ratio := range production.AspectRatios {
		if p.screen(ratio, "").Issue == "" {
			ratios = append(ratios, ratio)
		}
	}
	return ratios
}

// supports 与 MatchCapability 一致：未声明生成方式的模型视为不限制。
func (p dramaVideoProfile) supports(operation string) bool {
	if len(p.Operations) == 0 {
		return true
	}
	for _, item := range p.Operations {
		if item == operation {
			return true
		}
	}
	return false
}

func (p dramaVideoProfile) maxSeconds() int {
	return dramaMaxVideoSeconds(p.Durations)
}

// continuityIssue 返回模型不支持作品段落衔接方式的原因；空串表示可用。
func (p dramaVideoProfile) continuityIssue(settings DramaSettings) string {
	if !settings.parallelVideo() && !p.supports("image_to_video") {
		return "所选视频模型不支持图生视频，无法用上一段尾帧续接；请在制作设置里改用“并行出片”或换用支持图生视频的模型"
	}
	return ""
}

// dramaModelUnavailable 说明作品保存的模型为什么不能用、去哪里改。渠道模型被删除或停用后，
// 流水线和制作台不能只显示数据库的 record not found。
func dramaModelUnavailable(capability string, choice DramaModelChoice, reason string) error {
	label := map[string]string{"text": "文本", "image": "图片", "video": "视频"}[capability]
	message := fmt.Sprintf("%s模型「%s」%s，请在制作设置里改选", label, firstNonEmpty(choice.ModelKey, choice.LogicalModelID, choice.Label), reason)
	if capability == "video" {
		message += "；单独指定过视频模型的剧情段需重新选择"
	}
	return InvalidModelSelection(message)
}

func (s *Service) dramaVideoProfile(choice DramaModelChoice) (dramaVideoProfile, error) {
	var capability *ModelCapabilityConfig
	if choice.LogicalModelID != "" {
		// 只读取能力画像；用户授权在任务准入时按分组校验。
		routed, err := s.resolveDramaLogicalModel(nil, choice.LogicalModelID, "video")
		if err != nil {
			return dramaVideoProfile{}, err
		}
		if routed == nil {
			return dramaVideoProfile{}, errors.New("视频模型路由不存在")
		}
		var parseErr error
		capability, parseErr = normalizedChannelModelCapability(&routed.ChannelModel, s.protocolRegistry())
		if parseErr != nil {
			return dramaVideoProfile{}, parseErr
		}
	} else if choice.ChannelID != "" && choice.ModelKey != "" {
		row, err := s.repo.ChannelModelByKey(choice.ChannelID, choice.ModelKey)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dramaVideoProfile{}, dramaModelUnavailable("video", choice, "已下线或停用")
		}
		if err != nil {
			return dramaVideoProfile{}, err
		}
		capability, err = normalizedChannelModelCapability(row, s.protocolRegistry())
		if err != nil {
			return dramaVideoProfile{}, err
		}
	} else {
		return dramaVideoProfile{}, errors.New("请选择视频模型")
	}
	if capability == nil || capability.Video == nil {
		return dramaVideoProfile{}, errors.New("模型未声明视频能力")
	}
	video := capability.Video
	options := dramaDurationOptions(video.Duration)
	// 不声明同步生成声音的模型也可以用：段视频没有对白与音效，由用户在选模型时自行取舍。
	profile := dramaVideoProfile{Durations: options, Operations: video.Operations, MaxImages: video.References.MaxImages, MinImages: max(video.References.MinImages, 0), MaxAudios: max(video.References.MaxAudios, 0), MaxAudioSeconds: max(video.References.MaxAudioDuration, 0), GenerateAudio: video.GenerateAudio.Supported, PromptMaxChars: max(video.References.PromptMaxChars, 0), video: video}
	if profile.maxSeconds() <= 0 {
		return profile, errors.New("未声明可生成时长，无法按剧情段切分")
	}
	if !profile.supports("reference_to_video") && !profile.supports("text_to_video") {
		return profile, errors.New("短剧需要支持参考图生视频或文生视频（开场段与并行出片）")
	}
	return profile, nil
}

func (s *Service) quoteDramaChoice(userID string, choice DramaModelChoice, intent ModelRequestIntent) (int64, bool) {
	if choice.Empty() {
		return 0, false
	}
	var quote *LogicalModelQuote
	var err error
	if choice.LogicalModelID != "" {
		quote, err = s.QuoteLogicalModel(userID, choice.LogicalModelID, intent)
	} else {
		quote, err = s.QuoteChannelModel(userID, ChannelModelQuoteRequest{ChannelID: choice.ChannelID, ModelKey: choice.ModelKey, Intent: intent})
	}
	if err != nil || quote == nil {
		return 0, false
	}
	return quote.AmountMicrocredits, true
}

// EstimateDrama 依据真实报价估算整部剧的费用区间；数量按经验比例推算，结果只用于预算决策。
func (s *Service) EstimateDrama(userID string, req DramaEstimateRequest) (DramaEstimate, error) {
	settings := req.Settings.normalized()
	models, err := s.resolveDramaModels(userID, settings)
	if err != nil {
		return DramaEstimate{}, err
	}
	chapters := max(req.ChapterCount, 1)
	words := max(req.WordCount, chapters*1000)
	// 竖屏短剧每集约 90 秒，对应原文 2000-3000 字；章节过短时按字数折算。
	episodes := int(math.Round(float64(words) / 2500 * 90 / float64(settings.EpisodeSeconds)))
	if mode := firstNonEmpty(settings.EpisodeMode, req.EpisodeMode); mode == DramaEpisodeModeChapter {
		perEpisode := max(1, settings.ChaptersPerEpisode)
		episodes = (chapters + perEpisode - 1) / perEpisode
	}
	episodes = max(episodes, 1)
	shotsPerEpisode := settings.Storyboard.ShotTarget(settings.EpisodeSeconds)
	profile, err := s.dramaVideoProfile(models.Video)
	if err != nil {
		return DramaEstimate{}, err
	}
	// 一个剧情段一次视频调用；智能模式按模型上限估算，是调用次数的下限。
	segmentSeconds := max(settings.Storyboard.SegmentLimits(profile.Durations).TargetSeconds(), 1)
	segmentsPerEpisode := max(1, int(math.Ceil(float64(settings.EpisodeSeconds)/float64(segmentSeconds))))
	casts := min(60, 12+episodes/3)
	estimate := DramaEstimate{
		Episodes:        episodes,
		Shots:           episodes * shotsPerEpisode,
		Images:          casts,
		Videos:          episodes * segmentsPerEpisode,
		TextCalls:       1 + (chapters+9)/10 + episodes*(1+segmentsPerEpisode),
		DurationMinutes: episodes * settings.EpisodeSeconds / 60,
	}
	textCost, okText := s.quoteDramaChoice(userID, models.textFor(DramaStepScript), ModelRequestIntent{Capability: "text"})
	// 参考图按每类实际提交的模型、尺寸与分辨率报价，避免按模型默认档位低估；角色、道具、场景按等量估算。
	var imageTotal int64
	imagePriced, okImage := 0, false
	for _, kind := range []string{"character", "prop", "scene"} {
		choice := models.imageFor(kind)
		ratio := production.AssetImageRatio
		if kind == "character" {
			ratio = settings.Assets.CharacterRatio
		}
		imageIntent := *dramaModelIntent("image")
		if request := s.dramaImageRequestFor(choice, ratio, settings.Assets.ResolutionFor(kind)); request.Issue == "" {
			imageIntent.Options = map[string]any{}
			if request.Size != "" {
				imageIntent.Options["size"] = request.Size
			}
			if request.Resolution != "" {
				imageIntent.Options["imageResolution"] = request.Resolution
			}
		}
		if cost, ok := s.quoteDramaChoice(userID, choice, imageIntent); ok {
			imageTotal += cost
			imagePriced++
			okImage = true
		}
	}
	var imageCost int64
	if imagePriced > 0 {
		imageCost = imageTotal / int64(imagePriced)
	}
	operation, inputs := dramaSegmentOperation(settings, profile)
	// 与剧情段提交时的画面参数和声音开关一致，价格档可能按分辨率与是否出声区分。
	videoOptions := map[string]any{"videoSeconds": itoa(production.SnapVideoSeconds(int64(segmentSeconds)*1000, profile.Durations)), "videoGenerateAudio": strconv.FormatBool(profile.GenerateAudio)}
	if screen := profile.screen(settings.AspectRatio, settings.VideoResolution); screen.Issue == "" {
		screen.apply(videoOptions)
	}
	videoCost, okVideo := s.quoteDramaChoice(userID, models.Video, ModelRequestIntent{Capability: "video", Operation: operation, Inputs: inputs, Options: videoOptions})
	total := int64(estimate.TextCalls)*textCost + int64(estimate.Images)*imageCost + int64(estimate.Videos)*videoCost
	sample := textCost*int64(2+segmentsPerEpisode) + int64(casts)*imageCost + int64(segmentsPerEpisode)*videoCost
	estimate.Priced = okText || okImage || okVideo
	estimate.MinMicrocredits = total * 9 / 10
	// 上限包含失败重试与返工的余量。
	estimate.MaxMicrocredits = total * 14 / 10
	estimate.SampleMicrocredit = sample
	notes := []string{}
	if !(okText && okImage && okVideo) {
		notes = append(notes, "部分模型暂无报价，费用按已知部分估算")
	}
	if missing := missingDramaCapabilities(models); len(missing) > 0 {
		notes = append(notes, "尚无可用的"+strings.Join(missing, "、")+"模型")
	}
	estimate.Note = strings.Join(notes, "；")
	return estimate, nil
}
