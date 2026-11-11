package app

import (
	"yingce/backend/internal/model"
)

// DramaSegmentView 是一个剧情段：视频提示词、参考图、生成方式与视频结果。
type DramaSegmentView struct {
	ID string `json:"id"`
	// Revision 用于画布采用时的乐观并发校验；UpdatedAt 是段记录最后更新时间（RFC3339），段还没有记录时为空。
	Revision   int64    `json:"revision"`
	UpdatedAt  string   `json:"updatedAt,omitempty"`
	Index      int      `json:"index"`
	Title      string   `json:"title"`
	ShotIDs    []string `json:"shotIds"`
	DurationMs int64    `json:"durationMs"`
	// VideoPrompt 为生效内容：手动改写优先，其次是模型撰写的 Generated。
	VideoPrompt string `json:"videoPrompt"`
	Generated   string `json:"generated"`
	Custom      bool   `json:"custom"`
	// PromptStatus：none 无记录、queued 待撰写、running 撰写中、ready 完成、stale 镜头已改动、failed 失败。
	PromptStatus string `json:"promptStatus"`
	PromptError  string `json:"promptError,omitempty"`
	// VideoStatus：空串待生成、running、ready、stale（提示词或上一段已变）、failed。
	VideoStatus         string `json:"videoStatus"`
	VideoResourceID     string `json:"videoResourceId,omitempty"`
	TailFrameResourceID string `json:"tailFrameResourceId,omitempty"`
	// VideoSeconds 是当前视频进入成片的时长（秒），与成片时间线一致。
	VideoSeconds int    `json:"videoSeconds,omitempty"`
	VideoError   string `json:"videoError,omitempty"`
	// VideoReprocessable：视频已生成并入库，只是结果回写（如抽尾帧）失败；可重新处理已有视频，不重新生成。
	VideoReprocessable bool             `json:"videoReprocessable,omitempty"`
	VideoModel         DramaModelChoice `json:"videoModel"`
	// ReferenceMode：auto 按本段镜头自动取参考图；custom 为用户指定的参考图。
	ReferenceMode      string `json:"referenceMode"`
	AudioReferenceMode string `json:"audioReferenceMode"`
	// Request 是生成本段视频时实际会发送的内容：生成方式、首帧、参考图、秒数与追加在提示词前的参考图说明。
	Request   DramaSegmentRequest `json:"request"`
	PromptJob *DramaJobView       `json:"promptJob,omitempty"`
	VideoJob  *DramaJobView       `json:"videoJob,omitempty"`
	// VideoWait 只在视频已请求（queued）但尚未提交时出现：调度实际卡住的原因，例如等待上一段或参考图。
	VideoWait string `json:"videoWait,omitempty"`
}

// DramaSegmentRequest 是一段视频请求的预演，与提交时同一函数计算。
type DramaSegmentRequest struct {
	// Operation：image_to_video（上一段最后一帧作首帧）、reference_to_video（参考图）、text_to_video。
	Operation       string `json:"operation"`
	Seconds         int    `json:"seconds"`
	MaxVideoSeconds int    `json:"maxVideoSeconds"`
	// AspectRatio 是作品画幅；Size / Resolution 是实际发送的画面参数：声明比例参数的模型发 Size，
	// 用完整分辨率标签表达横竖的模型发 Resolution（例如 768p竖）。
	AspectRatio string `json:"aspectRatio"`
	Size        string `json:"size,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	// PromptMaxChars 是视频模型的提示词字符上限（含开头的参考图说明）；0 表示未声明。
	PromptMaxChars int `json:"promptMaxChars,omitempty"`
	// GenerateAudio 表示视频模型同步生成对白与音效；为 false 时本段视频没有声音。
	GenerateAudio bool `json:"generateAudio"`
	// FirstFrame 是上一段视频的最后一帧；第一段为空。FirstFramePending 表示上一段还没生成，生成后才能提交本段。
	FirstFrameResourceID string                  `json:"firstFrameResourceId,omitempty"`
	FirstFramePending    bool                    `json:"firstFramePending"`
	References           []DramaSegmentReference `json:"references"`
	// ReferenceCapacity 是本段最多随请求发送的参考图张数（已扣除首帧，并受每段上限约束）；ModelMaxImages 是视频模型声明的图片上限。
	ReferenceCapacity int `json:"referenceCapacity"`
	ModelMaxImages    int `json:"modelMaxImages"`
	// ReferenceLimited 表示有参考图因“每段最多参考图”设置而未发送（而不是模型上限）。
	ReferenceLimited bool `json:"referenceLimited,omitempty"`
	// Header 是提交时放在提示词最前面的参考图对应说明，让模型知道每张图是谁。
	Header string `json:"header"`
	// Warning 说明请求的限制，例如模型不支持参考图、参考图超出模型上限。
	Warning string `json:"warning,omitempty"`
	// UnknownReferences 是提示词正文里不属于本段的“参考图N”（编号超出本段参考图数量），存在时不能提交视频。
	UnknownReferences []string `json:"unknownReferences,omitempty"`
	// AudioReferences 为按对白自动选择或用户手动指定的角色；Sent 的作为“参考音频N”随请求发送。
	// ModelMaxAudios 是视频模型声明的参考音频上限，0 表示不支持参考音频。
	AudioReferences []DramaSegmentAudioReference `json:"audioReferences"`
	ModelMaxAudios  int                          `json:"modelMaxAudios,omitempty"`
}

// DramaSegmentAudioReference 是一个说话角色的声音样本；没有随请求发送时 Note 说明原因，本段按声线描述生成该角色的对白。
type DramaSegmentAudioReference struct {
	CastID     string `json:"castId"`
	Name       string `json:"name"`
	Label      string `json:"label,omitempty"`
	ResourceID string `json:"resourceId"`
	Sent       bool   `json:"sent"`
	Note       string `json:"note,omitempty"`
}

type DramaSegmentReference struct {
	ResourceID string `json:"resourceId,omitempty"`
	CastID     string `json:"castId"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	// Sent 表示这张参考图会随请求发送；未生成或超过模型参考图上限时为 false。
	Sent bool `json:"sent"`
}

type UpdateDramaSegmentRequest struct {
	// VideoPromptOverride 传空串表示恢复模型撰写的版本。
	VideoPromptOverride *string `json:"videoPromptOverride"`
	// ReferenceCastIDs 指定本段参考图（顺序即编号，可为空数组表示不发参考图）；AutoReferences=true 恢复按镜头自动取。
	ReferenceCastIDs    *[]string                    `json:"referenceCastIds"`
	AutoReferences      bool                         `json:"autoReferences"`
	ReferenceImages     *[]model.DramaReferenceImage `json:"referenceImages"`
	AudioCastIDs        *[]string                    `json:"audioCastIds"`
	AutoAudioReferences bool                         `json:"autoAudioReferences"`
}

// UpdateDramaShotRequest 修改镜头的剧本内容；镜头只是撰写视频提示词的素材，修改后本段提示词标记过期。
type UpdateDramaShotRequest struct {
	Speaker     *string `json:"speaker"`
	Dialogue    *string `json:"dialogue"`
	Action      *string `json:"action"`
	ShotSize    *string `json:"shotSize"`
	Camera      *string `json:"camera"`
	ImagePrompt *string `json:"imagePrompt"`
	VideoPrompt *string `json:"videoPrompt"`
	// Transition 是进入本镜头的转场；DurationSeconds 手动设置镜头时长。
	Transition      *string  `json:"transition"`
	DurationSeconds *float64 `json:"durationSeconds"`
}

// SetDramaCastVoiceRequest 为角色指定声音：VoiceProfileID 从声音库选，SampleResourceID 是刚上传或录制的样本音频
// （按 VoiceName 登记到声音库，同一样本重复使用同一条）。RestyleSegments 让引用该角色的剧情段视频重新生成。
type SetDramaCastVoiceRequest struct {
	VoiceProfileID   string `json:"voiceProfileId"`
	SampleResourceID string `json:"sampleResourceId"`
	VoiceName        string `json:"voiceName"`
	RestyleSegments  bool   `json:"restyleSegments"`
}

// DramaCastLookRequest 新增或修改一套角色造型：LookName 造型名，Appearance 这套造型的外观，
// FromChapter / ToChapter 默认适用的原著章节号（从 1 起，0 或 nil 表示该端不限）。
type DramaCastLookRequest struct {
	LookName        string  `json:"lookName"`
	Appearance      string  `json:"appearance"`
	ImagePrompt     *string `json:"imagePrompt"`
	FromChapter     *int    `json:"fromChapter"`
	ToChapter       *int    `json:"toChapter"`
	RestyleSegments bool    `json:"restyleSegments"`
}

type UpdateDramaCastRequest struct {
	Name              *string   `json:"name"`
	Aliases           *[]string `json:"aliases"`
	Appearance        *string   `json:"appearance"`
	Description       *string   `json:"description"`
	VoiceInstructions *string   `json:"voiceInstructions"`
	Locked            *bool     `json:"locked"`
}

type DramaListItem struct {
	Project         model.Project `json:"project"`
	Status          string        `json:"status"`
	Stage           string        `json:"stage"`
	PauseReason     string        `json:"pauseReason"`
	EpisodeCount    int64         `json:"episodeCount"`
	DoneCount       int64         `json:"doneCount"`
	CoverResourceID string        `json:"coverResourceId,omitempty"`
	// CoverResourceIDs 是前几张角色参考图，卡片拼成封面；CoverResourceID 保留第一张。
	CoverResourceIDs  []string `json:"coverResourceIds,omitempty"`
	AspectRatio       string   `json:"aspectRatio"`
	StyleName         string   `json:"styleName,omitempty"`
	SpentMicrocredits int64    `json:"spentMicrocredits"`
	// HeldMicrocredits 是尚未结算订单的冻结额，口径同 DramaProductionView。
	HeldMicrocredits int64  `json:"heldMicrocredits"`
	UpdatedAt        string `json:"updatedAt"`
	// HasDraftForm 表示草稿保存了创建页表单，打开时回到创建页继续编辑。
	HasDraftForm bool `json:"hasDraftForm,omitempty"`
}

type DramaModelOption struct {
	Choice     DramaModelChoice `json:"choice"`
	Capability string           `json:"capability"`
	Label      string           `json:"label"`
	PriceLabel string           `json:"priceLabel"`
	// MaxVideoSeconds 仅视频模型：单次最长生成秒数，0 表示未声明。
	MaxVideoSeconds int `json:"maxVideoSeconds,omitempty"`
	MaxAudios       int `json:"maxAudios"`
	// MinReferenceImages 仅视频模型：模型要求的最少参考图数（含首帧），0 表示不强制参考图。
	MinReferenceImages int `json:"minReferenceImages,omitempty"`
	// TailFrameChain 仅视频模型：是否支持图生视频，可用于“尾帧续接”；不支持的模型只能并行出片。
	TailFrameChain bool `json:"tailFrameChain,omitempty"`
	// VideoRatios 仅视频模型：能准确给出的作品画幅；不含作品画幅的模型不能用于该作品。
	VideoRatios []string `json:"videoRatios,omitempty"`
	// GenerateAudio 仅视频模型：是否同步生成对白与音效；false 时段视频没有声音。
	GenerateAudio *bool `json:"generateAudio,omitempty"`
	// VideoResolutions 仅视频模型：作品可指定的分辨率档位（如 720p、1080p）与模型默认档位；为空表示模型不提供可选分辨率。
	VideoResolutions       []string `json:"videoResolutions,omitempty"`
	DefaultVideoResolution string   `json:"defaultVideoResolution,omitempty"`
	// 以下仅图片模型：可指定的参考图分辨率档位、默认档位，以及能按默认档位准确出图的参考图画幅。
	ImageResolutions       []string `json:"imageResolutions,omitempty"`
	DefaultImageResolution string   `json:"defaultImageResolution,omitempty"`
	ImageRatios            []string `json:"imageRatios,omitempty"`
}

// DramaModelUnavailable 是平台目录里有、但短剧流水线无法使用的模型及原因，前端置灰展示而不是隐藏。
type DramaModelUnavailable struct {
	Choice DramaModelChoice `json:"choice"`
	Reason string           `json:"reason"`
}

type DramaModelCatalog struct {
	Defaults    DramaModels                        `json:"defaults"`
	Options     map[string][]DramaModelOption      `json:"options"`
	Unavailable map[string][]DramaModelUnavailable `json:"unavailable"`
}

type DramaEstimateRequest struct {
	ChapterCount int           `json:"chapterCount"`
	WordCount    int           `json:"wordCount"`
	Settings     DramaSettings `json:"settings"`
	// EpisodeMode 为 chapter 时每章一集，集数等于章节数。
	EpisodeMode string `json:"episodeMode"`
}

// DramaEstimate 的 Videos 是剧情段数（一段一次视频调用），Images 是参考图数。
type DramaEstimate struct {
	Episodes          int    `json:"episodes"`
	Shots             int    `json:"shots"`
	Images            int    `json:"images"`
	Videos            int    `json:"videos"`
	TextCalls         int    `json:"textCalls"`
	MinMicrocredits   int64  `json:"minMicrocredits"`
	MaxMicrocredits   int64  `json:"maxMicrocredits"`
	SampleMicrocredit int64  `json:"sampleMicrocredits"`
	Priced            bool   `json:"priced"`
	Note              string `json:"note"`
	DurationMinutes   int    `json:"durationMinutes"`
}

// ReleaseDramaMediaRequest 在手动模式下放行生成：assets 只放行参考图，episodes 放行指定集，all 放行全部。
type ReleaseDramaMediaRequest struct {
	Target     string   `json:"target"`
	EpisodeIDs []string `json:"episodeIds"`
}
