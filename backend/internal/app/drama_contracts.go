package app

import (
	"yingce/backend/internal/model"
)

type DramaChapterInput struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// 分集方式：auto 由文本模型把小说章节规划成集；chapter 每章就是一集，不经过分集规划，适合逐集制作。
const (
	DramaEpisodeModeAuto    = "auto"
	DramaEpisodeModeChapter = "chapter"
)

type CreateDramaRequest struct {
	Name               string              `json:"name"`
	Chapters           []DramaChapterInput `json:"chapters"`
	Settings           DramaSettings       `json:"settings"`
	BudgetMicrocredits int64               `json:"budgetMicrocredits"`
	Start              bool                `json:"start"`
	// SourceFormat 是素材类型：novel 小说、script 剧本、storyboard 分镜；剧本与分镜总是每章一集。
	SourceFormat string `json:"sourceFormat"`
	EpisodeMode  string `json:"episodeMode"`
	// SelectedChapters 是先加入制作的章节下标（从 0 起，对应 Chapters）；为空表示全部章节。
	// 没选中的章节原文照常保存为预留章节，不参与资产提取、分集与改编，之后可在制作台加入。
	SelectedChapters []int `json:"selectedChapters"`
	// Draft 是 start=false 时随草稿保存的创建页表单，回到创建页时原样回填；开工时不保存。
	Draft *DramaDraftForm `json:"draft,omitempty"`
}

// DramaDraftForm 是创建页里设置之外、回填所需的表单内容：导入的原始正文（按当前分集方式重新拆章）和画风 ID。
type DramaDraftForm struct {
	SourceText string `json:"sourceText"`
	StyleID    string `json:"styleId,omitempty"`
}

// DramaDraftView 是草稿回填创建页所需的全部内容；SelectedChapters 是加入制作的章节下标（从 0 起）。
type DramaDraftView struct {
	ProjectID          string         `json:"projectId"`
	Name               string         `json:"name"`
	SourceFormat       string         `json:"sourceFormat"`
	Settings           DramaSettings  `json:"settings"`
	BudgetMicrocredits int64          `json:"budgetMicrocredits"`
	SelectedChapters   []int          `json:"selectedChapters"`
	ChapterCount       int            `json:"chapterCount"`
	Form               DramaDraftForm `json:"form"`
}

// ProduceDramaChaptersRequest 把预留的原著章节加入制作：按章节顺序每 ChaptersPerEpisode 章建成一集（不连续的章节不合并），
// 追加在现有分集之后。ChaptersPerEpisode 为 0 时沿用作品的按章分集设置，没有设置按每章一集。
type ProduceDramaChaptersRequest struct {
	ChapterIDs         []string `json:"chapterIds"`
	ChaptersPerEpisode int      `json:"chaptersPerEpisode"`
}

// ProduceDramaChaptersResult 是加入制作后新建的集，按集号排列。
type ProduceDramaChaptersResult struct {
	EpisodeIDs []string `json:"episodeIds"`
}

// AddDramaEpisodeRequest 新增一集：原文直接成为这一集，不经过分集规划。AnchorEpisodeID 为空时追加到末尾；
// 否则按 Position（before / after）插到这一集之前或之后，后续集号顺延。
type AddDramaEpisodeRequest struct {
	Title           string `json:"title"`
	Text            string `json:"text"`
	SourceFormat    string `json:"sourceFormat"`
	AnchorEpisodeID string `json:"anchorEpisodeId"`
	Position        string `json:"position"`
}

// UpdateDramaChapterRequest 修改原文；已写好的剧本不会自动重写，需要用户点重新改编。
type UpdateDramaChapterRequest struct {
	Title *string `json:"title"`
	Text  *string `json:"text"`
}

type UpdateDramaEpisodeRequest struct {
	Title        *string `json:"title"`
	SourceFormat *string `json:"sourceFormat"`
}

// InsertDramaShotRequest 在指定镜头前或后插入一个空镜头，归入同一剧情段。
type InsertDramaShotRequest struct {
	Position string `json:"position"`
}

// SetDramaCastImageRequest 用用户上传或画布采用的图片替换参考图并锁定，之后不会被重新生成覆盖。
type SetDramaCastImageRequest struct {
	ResourceID      string `json:"resourceId"`
	RestyleSegments bool   `json:"restyleSegments"`
	// Source 为 canvas 表示从精修画布采用，版本记录标为“来自画布”；空串或 upload 为直接上传。
	Source string `json:"source"`
	// TaskID 是产出这张图的画布生成任务，只用于版本来源与提示词回看；与资源对不上时忽略。
	TaskID string `json:"taskId"`
}

// AdoptDramaSegmentVideoRequest 把画布上的视频采用为剧情段当前视频。
type AdoptDramaSegmentVideoRequest struct {
	ResourceID string `json:"resourceId"`
	TaskID     string `json:"taskId"`
	// Cascade=true 时后续已生成过视频的段重新排队生成（首帧换成新视频的尾帧，保证衔接）；默认只换本段。
	// 并行出片的作品段与段互不依赖，忽略该选项。
	Cascade bool `json:"cascade"`
	// ExpectedRevision 是客户端看到的剧情段版本；版本已变时拒绝，避免覆盖刚发生的修改。
	ExpectedRevision *int64 `json:"expectedRevision"`
}

// DramaCanvasAssetsRequest 为投送到画布的短剧媒体登记素材记录：画布媒体节点必须由当前用户的素材引用资源。
type DramaCanvasAssetsRequest struct {
	Resources []DramaCanvasAssetInput `json:"resources"`
}

type DramaCanvasAssetInput struct {
	ResourceID string `json:"resourceId"`
	Title      string `json:"title"`
	Category   string `json:"category"`
}

// DramaTakeView 是一个产物版本；Source 区分流水线生成（pipeline）、直接上传（upload）与画布采用（canvas）。
type DramaTakeView struct {
	ID         string `json:"id"`
	OwnerID    string `json:"ownerId"`
	Kind       string `json:"kind"`
	Version    int    `json:"version"`
	ResourceID string `json:"resourceId"`
	TaskID     string `json:"taskId,omitempty"`
	Prompt     string `json:"prompt"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Source     string `json:"source"`
	Current    bool   `json:"current"`
	CreatedAt  string `json:"createdAt"`
}

// DramaDialogueCheck 是原文台词与镜头台词的逐句核对结果；Missing 是被删掉或改写的原文台词（最多展示 30 句）。
type DramaDialogueCheck struct {
	SourceLines  int      `json:"sourceLines"`
	MissingCount int      `json:"missingCount"`
	Missing      []string `json:"missing"`
}

type UpdateDramaSettingsRequest struct {
	Settings           *DramaSettings `json:"settings"`
	BudgetMicrocredits *int64         `json:"budgetMicrocredits"`
}

type DramaProductionView struct {
	Status             string        `json:"status"`
	Stage              string        `json:"stage"`
	PauseReason        string        `json:"pauseReason"`
	Settings           DramaSettings `json:"settings"`
	BudgetMicrocredits int64         `json:"budgetMicrocredits"`
	// SpentMicrocredits 是作品已实际扣费的总额：流水线任务加关联画布上的精修任务，按订单结算实收计（退回不计）。
	// CanvasSpentMicrocredits 是其中画布精修的部分；HeldMicrocredits 是尚未结算订单的冻结额（冻结中）。
	// 预算按 SpentMicrocredits + HeldMicrocredits 保守判断。
	SpentMicrocredits       int64 `json:"spentMicrocredits"`
	CanvasSpentMicrocredits int64 `json:"canvasSpentMicrocredits"`
	HeldMicrocredits        int64 `json:"heldMicrocredits"`
	PlanDone                bool  `json:"planDone"`
	PlanCursor              int   `json:"planCursor"`
	// HasDraftForm 同 DramaListItem.HasDraftForm。
	HasDraftForm   bool   `json:"hasDraftForm,omitempty"`
	SampleApproved bool   `json:"sampleApproved"`
	AssetsReleased bool   `json:"assetsReleased"`
	Logline        string `json:"logline"`
	Tone           string `json:"tone"`
	// ChapterCount / WordCount 只统计已加入制作的章节；Reserved* 是导入时未选中、尚未加入制作的原著章节。
	ChapterCount         int `json:"chapterCount"`
	WordCount            int `json:"wordCount"`
	ReservedChapterCount int `json:"reservedChapterCount"`
	ReservedWordCount    int `json:"reservedWordCount"`
}

// DramaEpisodeStats 按剧情段统计进度：视频提示词写好几段、视频生成几段、失败几段。
// VideoGenerated 是当前持有视频的段数（含已过期的旧视频），删除本集前据此要求输入确认。
type DramaEpisodeStats struct {
	Segments       int64 `json:"segments"`
	PromptReady    int64 `json:"promptReady"`
	VideoReady     int64 `json:"videoReady"`
	VideoGenerated int64 `json:"videoGenerated"`
	Failed         int64 `json:"failed"`
}

type DramaEpisodeRow struct {
	ID              string            `json:"id"`
	Number          int               `json:"number"`
	Title           string            `json:"title"`
	Synopsis        string            `json:"synopsis"`
	Stage           string            `json:"stage"`
	SourceFormat    string            `json:"sourceFormat"`
	Error           string            `json:"error"`
	ShotCount       int               `json:"shotCount"`
	DurationMs      int64             `json:"durationMs"`
	Approved        bool              `json:"approved"`
	MediaReleased   bool              `json:"mediaReleased"`
	AwaitingMedia   bool              `json:"awaitingMedia"`
	FinalResourceID string            `json:"finalResourceId,omitempty"`
	FinalStale      bool              `json:"finalStale"`
	Stats           DramaEpisodeStats `json:"stats"`
	// Revision / UpdatedAt 是集记录的版本与最后更新时间（RFC3339），用于判断读到的状态是否最新。
	Revision  int64  `json:"revision"`
	UpdatedAt string `json:"updatedAt"`
}

type DramaInboxItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	EpisodeID string `json:"episodeId,omitempty"`
	CastID    string `json:"castId,omitempty"`
	// 以下字段供前端把同类问题折叠成组：角色/道具/场景、所在集、段号与失败环节（prompt 提示词 / video 视频）。
	CastKind      string `json:"castKind,omitempty"`
	EpisodeNumber int    `json:"episodeNumber,omitempty"`
	SegmentIndex  int    `json:"segmentIndex"`
	Media         string `json:"media,omitempty"`
}

type DramaJobView struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	Label            string `json:"label"`
	Error            string `json:"error,omitempty"`
	EpisodeID        string `json:"episodeId,omitempty"`
	ShotID           string `json:"shotId,omitempty"`
	TaskID           string `json:"taskId,omitempty"`
	CostMicrocredits int64  `json:"costMicrocredits"`
	Attempt          int    `json:"attempt"`
	CreatedAt        string `json:"createdAt"`
	StartedAt        string `json:"startedAt,omitempty"`
	FinishedAt       string `json:"finishedAt,omitempty"`
	UpdatedAt        string `json:"updatedAt"`
	// ProviderStage 是生成中任务的上游进度文案（如“候车 · 前面 3 条”），只在上游已接收任务时给出。
	ProviderStage string `json:"providerStage,omitempty"`
	// Wait 是排队作业还没提交的原因（作品暂停、账号同时任务已满、本池同时生成数已满）；下一轮就会提交时为空。
	Wait string `json:"wait,omitempty"`
}

// DramaJobAttempt 是工作的一次提交记录；status 为任务状态，或 rejected（提交时被准入拒绝）。
type DramaJobAttempt struct {
	TaskID           string `json:"taskId,omitempty"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
	CostMicrocredits int64  `json:"costMicrocredits"`
	At               string `json:"at"`
}

// DramaJobTaskView 是队列详情里展示的任务快照：只暴露请求参数白名单，不回显渠道配置。
type DramaJobTaskView struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	Stage       string            `json:"stage,omitempty"`
	Operation   string            `json:"operation,omitempty"`
	Model       string            `json:"model"`
	ModelLabel  string            `json:"modelLabel"`
	Prompt      string            `json:"prompt"`
	Params      map[string]string `json:"params"`
	Error       string            `json:"error,omitempty"`
	MediaStage  string            `json:"mediaStage,omitempty"`
	CreatedAt   string            `json:"createdAt"`
	StartedAt   string            `json:"startedAt,omitempty"`
	CompletedAt string            `json:"completedAt,omitempty"`
	ResultMode  string            `json:"resultMode,omitempty"`
	ResultText  string            `json:"resultText,omitempty"`
	ResourceIDs []string          `json:"resourceIds,omitempty"`
}

type DramaJobAttemptView struct {
	DramaJobAttempt
	Task *DramaJobTaskView `json:"task,omitempty"`
}

type DramaJobDetail struct {
	Job      DramaJobView          `json:"job"`
	Target   string                `json:"target"`
	Attempts []DramaJobAttemptView `json:"attempts"`
}

type DramaSummary struct {
	Project    model.Project       `json:"project"`
	Production DramaProductionView `json:"production"`
	Episodes   []DramaEpisodeRow   `json:"episodes"`
	JobCounts  map[string]int64    `json:"jobCounts"`
	Inbox      []DramaInboxItem    `json:"inbox"`
	CastCount  int                 `json:"castCount"`
}

type DramaChapterRef struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Position  int    `json:"position"`
	WordCount int    `json:"wordCount"`
	// Reserved 表示原著章节尚未加入制作；EpisodeNumber 是使用这一章的第一集（未分到集时为 0）。只在章节目录里给出。
	Reserved      bool `json:"reserved,omitempty"`
	EpisodeNumber int  `json:"episodeNumber,omitempty"`
}

type DramaEpisodeDetail struct {
	Episode  model.DramaEpisode `json:"episode"`
	Shots    []model.DramaShot  `json:"shots"`
	Segments []DramaSegmentView `json:"segments"`
	Chapters []DramaChapterRef  `json:"chapters"`
	Jobs     []DramaJobView     `json:"jobs"`
	// ScriptJob 是本集最近一次改编剧本的调度工作，可查看改编提示词与模型返回。
	ScriptJob *DramaJobView `json:"scriptJob,omitempty"`
	// DialogueCheck 只在本集有镜头、且原文章节不与其他集共用时给出（共用时无法判断哪些台词属于本集）。
	DialogueCheck *DramaDialogueCheck `json:"dialogueCheck,omitempty"`
}
