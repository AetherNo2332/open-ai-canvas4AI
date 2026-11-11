package app

import (
	"encoding/json"
	"strings"
	"yingce/backend/internal/production"
)

// DramaModelChoice 是一条可在服务端独立执行的模型选择：逻辑模型或系统渠道模型二选一。
// 自定义渠道的密钥只存在浏览器，无法驱动无人值守的流水线，因此不在这里出现。
type DramaModelChoice struct {
	LogicalModelID string `json:"logicalModelId,omitempty"`
	ChannelID      string `json:"channelId,omitempty"`
	ModelKey       string `json:"modelKey,omitempty"`
	Label          string `json:"label,omitempty"`
	PriceLabel     string `json:"priceLabel,omitempty"`
}

func (c DramaModelChoice) Empty() bool {
	return strings.TrimSpace(c.LogicalModelID) == "" && (strings.TrimSpace(c.ChannelID) == "" || strings.TrimSpace(c.ModelKey) == "")
}

// DramaModels 是制作用到的三类模型：文本（改编、分镜、视频提示词）、图片（角色/道具/场景参考图）、
// 视频（按剧情段生成带对白与音效的视频）。文字步骤与参考图类型可以单独指定模型，为空沿用同能力的默认模型。
type DramaModels struct {
	Text  DramaModelChoice `json:"text"`
	Image DramaModelChoice `json:"image"`
	Video DramaModelChoice `json:"video"`

	BibleText      DramaModelChoice `json:"bibleText"`
	ScriptText     DramaModelChoice `json:"scriptText"`
	PromptText     DramaModelChoice `json:"promptText"`
	CharacterImage DramaModelChoice `json:"characterImage"`
	PropImage      DramaModelChoice `json:"propImage"`
	SceneImage     DramaModelChoice `json:"sceneImage"`
}

// 文字步骤：bible 资产提取（作品圣经），script 分镜拆分（每集剧本与镜头表），prompt 剧情段视频提示词。
// 分集规划沿用默认文本模型。
const (
	DramaStepBible  = "bible"
	DramaStepPlan   = "plan"
	DramaStepScript = "script"
	DramaStepPrompt = "prompt"
)

type DramaTextThinking struct {
	Bible  string `json:"bible,omitempty"`
	Plan   string `json:"plan,omitempty"`
	Script string `json:"script,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

func (t DramaTextThinking) levelFor(step string) string {
	var level string
	switch step {
	case DramaStepBible:
		level = t.Bible
	case DramaStepPlan:
		level = t.Plan
	case DramaStepScript:
		level = t.Script
	case DramaStepPrompt:
		level = t.Prompt
	}
	if strings.TrimSpace(level) == "" {
		return ThinkingIntentAuto
	}
	return strings.TrimSpace(level)
}

func (t DramaTextThinking) validate() error {
	for step, level := range map[string]string{
		DramaStepBible: t.Bible, DramaStepPlan: t.Plan, DramaStepScript: t.Script, DramaStepPrompt: t.Prompt,
	} {
		if err := ValidateThinkingLevelRequest(level); err != nil {
			return BadAuthRequest("短剧" + step + "步骤的思考等级无效")
		}
	}
	return nil
}

// textFor 是文字步骤实际使用的文本模型。
func (m DramaModels) textFor(step string) DramaModelChoice {
	var override DramaModelChoice
	switch step {
	case DramaStepBible:
		override = m.BibleText
	case DramaStepScript:
		override = m.ScriptText
	case DramaStepPrompt:
		override = m.PromptText
	}
	if !override.Empty() {
		return override
	}
	return m.Text
}

// imageFor 是某类参考图（character / prop / scene）实际使用的图片模型。
func (m DramaModels) imageFor(kind string) DramaModelChoice {
	var override DramaModelChoice
	switch kind {
	case "character":
		override = m.CharacterImage
	case "prop":
		override = m.PropImage
	case "scene":
		override = m.SceneImage
	}
	if !override.Empty() {
		return override
	}
	return m.Image
}

func (m DramaModels) choice(capability string) DramaModelChoice {
	switch capability {
	case "text":
		return m.Text
	case "image":
		return m.Image
	case "video":
		return m.Video
	}
	return DramaModelChoice{}
}

// 生成节奏：manual 先只跑文本（作品圣经、分集、剧本、分镜与视频提示词），用户确认后再生成参考图与视频；
// auto 一路跑到成片。
const (
	DramaMediaManual = "manual"
	DramaMediaAuto   = "auto"
)

// 审阅方式：sample 先完整做出第 1 集样片，确认后其余集才生成视频；every 每集生成视频前确认视频提示词；auto 不打断。
const (
	DramaReviewSample = "sample"
	DramaReviewEvery  = "every"
	DramaReviewAuto   = "auto"
)

// DramaSkillRef 指向用户技能库里的一个技能文件，用作文字步骤（资产提取、分镜拆分、视频提示词）的写作方法。
type DramaSkillRef struct {
	SkillID   string `json:"skillId"`
	SkillName string `json:"skillName"`
	Path      string `json:"path"`
}

// normalizedDramaSkillRef 截断字段长度；没有技能 ID 时视为未选择。
func normalizedDramaSkillRef(value *DramaSkillRef) *DramaSkillRef {
	if value == nil {
		return nil
	}
	ref := DramaSkillRef{SkillID: truncateRunes(strings.TrimSpace(value.SkillID), 64), SkillName: truncateRunes(strings.TrimSpace(value.SkillName), 120), Path: truncateRunes(strings.TrimSpace(value.Path), 300)}
	if ref.SkillID == "" {
		return nil
	}
	if ref.Path == "" {
		ref.Path = "SKILL.md"
	}
	return &ref
}

type DramaSettings struct {
	production.Settings
	// PromptSkill 为空时按内置格式撰写段视频提示词。
	PromptSkill *DramaSkillRef `json:"promptSkill,omitempty"`
	// BibleSkill / ScriptSkill 是资产提取与分镜拆分的写作技能，为空按内置规则；输出结构始终由系统固定。
	BibleSkill   *DramaSkillRef    `json:"bibleSkill,omitempty"`
	ScriptSkill  *DramaSkillRef    `json:"scriptSkill,omitempty"`
	ReviewMode   string            `json:"reviewMode"`
	MediaMode    string            `json:"mediaMode"`
	Concurrency  int               `json:"concurrency"`
	Models       DramaModels       `json:"models"`
	TextThinking DramaTextThinking `json:"textThinking,omitempty"`
	// ReferenceLimit 是自动取参考图时每段最多随请求发送的张数，0 表示按视频模型上限；手动指定参考图的段只受模型上限约束。
	ReferenceLimit int `json:"referenceLimit"`
	// EpisodeMode 是小说的分集方式：chapter 按章节固定分组（每 ChaptersPerEpisode 章一集），auto 由文本模型规划。
	// 空串是早于该设置创建的作品（分集方式没有记录）；分集完成后不能再修改。剧本与分镜总是每章一集。
	EpisodeMode        string `json:"episodeMode,omitempty"`
	ChaptersPerEpisode int    `json:"chaptersPerEpisode,omitempty"`
	// VideoContinuity 是剧情段视频的衔接方式，见 DramaContinuityTailFrame / DramaContinuityParallel。
	VideoContinuity string `json:"videoContinuity"`
	// VideoResolution 是剧情段视频的分辨率档位（取自视频模型声明的分辨率，如 720p、1080p）；空串按模型默认。
	VideoResolution string `json:"videoResolution,omitempty"`
}

// 剧情段视频衔接方式：tail_frame 第 2 段起用上一段视频的最后一帧做首帧，同集各段按顺序生成；
// parallel 每段只按角色/场景参考图独立生成，同集各段同时提交，适合按班次批量出片的渠道。
const (
	DramaContinuityTailFrame = "tail_frame"
	DramaContinuityParallel  = "parallel"
)

// parallelVideo 表示剧情段视频互不依赖：不等上一段、不带首帧，同集各段可同时生成。
func (s DramaSettings) parallelVideo() bool {
	return s.VideoContinuity == DramaContinuityParallel
}

// dramaMaxChaptersPerEpisode 是按章分集时每集最多合并的章数；dramaEpisodeSourceRunes 是合并后单集原文上限，
// 超过会让剧本提示词过长、改编时丢情节。
const (
	dramaMaxChaptersPerEpisode = 12
	dramaEpisodeSourceRunes    = 40000
)

// dramaMaxReferenceLimit 是每段参考图张数设置的上限。
const dramaMaxReferenceLimit = 20

func (s DramaSettings) normalized() DramaSettings {
	s.Settings = s.Settings.Normalized()
	switch s.ReviewMode {
	case DramaReviewSample, DramaReviewEvery, DramaReviewAuto:
	default:
		s.ReviewMode = DramaReviewSample
	}
	if s.MediaMode != DramaMediaManual {
		// 早于该设置创建的作品一直是全自动，保持原行为；新建作品由 CreateDrama 默认 manual。
		s.MediaMode = DramaMediaAuto
	}
	s.PromptSkill = normalizedDramaSkillRef(s.PromptSkill)
	s.BibleSkill = normalizedDramaSkillRef(s.BibleSkill)
	s.ScriptSkill = normalizedDramaSkillRef(s.ScriptSkill)
	if s.Concurrency <= 0 {
		s.Concurrency = 4
	}
	if s.Concurrency > 16 {
		s.Concurrency = 16
	}
	s.ReferenceLimit = max(0, min(dramaMaxReferenceLimit, s.ReferenceLimit))
	s.VideoResolution = truncateRunes(strings.TrimSpace(s.VideoResolution), 32)
	if isAutomaticVideoResolution(s.VideoResolution) {
		s.VideoResolution = ""
	}
	if s.VideoContinuity != DramaContinuityParallel {
		// 早于该设置创建的作品一直按尾帧续接，保持原行为。
		s.VideoContinuity = DramaContinuityTailFrame
	}
	switch s.EpisodeMode {
	case DramaEpisodeModeChapter, DramaEpisodeModeAuto, "":
	default:
		s.EpisodeMode = DramaEpisodeModeAuto
	}
	if s.EpisodeMode == DramaEpisodeModeChapter {
		s.ChaptersPerEpisode = max(1, min(dramaMaxChaptersPerEpisode, s.ChaptersPerEpisode))
	} else {
		s.ChaptersPerEpisode = 0
	}
	return s
}

func parseDramaSettings(raw string) DramaSettings {
	var settings DramaSettings
	_ = json.Unmarshal([]byte(raw), &settings)
	return settings.normalized()
}

func encodeDramaSettings(settings DramaSettings) string {
	encoded, _ := json.Marshal(settings.normalized())
	return string(encoded)
}

type dramaBibleMeta struct {
	Logline     string `json:"logline"`
	Tone        string `json:"tone"`
	VisualRules string `json:"visualRules"`
}

func parseDramaBibleMeta(raw string) dramaBibleMeta {
	var meta dramaBibleMeta
	_ = json.Unmarshal([]byte(raw), &meta)
	return meta
}
