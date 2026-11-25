package model

import "time"

// 独立 shortplay 工作台的数据模型。与 drama 制作台是两条产品线，共用 projects 容器
// （type = "shortplay"）但各有自己的集、资产与任务表。
//
// 三条不变式：
//   - 一集一画布：shortplay_episodes.canvas_id 唯一且非空，画布行按需创建。
//   - 资产身份是 (shortplay_id, id) 复合主键，id 为项目内从 1 起的序号；
//     跨项目可以各有资产 1，任何读写都必须带 shortplay_id。
//   - 参考图的媒体文件在 resources 表，本域只持 resourceId；当前图/音的唯一真源是
//     current_image_id / current_audio_id 指针。
//
// 供体固定提交 13731aae6 的 shortplay/model.go；目标端只改包归属与命名空间。

// Shortplay 是项目的短剧域配置，项目内唯一；原文只作整篇留档，工作副本在集上。
type Shortplay struct {
	ID           string    `json:"id" gorm:"primaryKey;size:36"`
	ProjectID    string    `json:"projectId" gorm:"index;size:36"`
	SourceRaw    string    `json:"-" gorm:"type:text"`
	SettingsJSON string    `json:"settingsJson" gorm:"type:text"`
	UserID       string    `json:"userId" gorm:"index;size:36"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ShortplayEpisode 是一集：集行同时就是画布归属的权威（canvasId 唯一）。
//
// CanvasID 是预分配的画布地址，不是画布行的外键：建档时只分配 ID 写进集行，
// 画布行在用户进入精修画布时才创建。所有读画布路径都以 canvas_projects 行是否存在为准，
// 因此「集已建号、画布未建」这个中间态没有观测面。
type ShortplayEpisode struct {
	ID          string `json:"id" gorm:"primaryKey;size:36"`
	ShortplayID string `json:"shortplayId" gorm:"size:36;uniqueIndex:idx_shortplay_episodes_shortplay_number,priority:1"`
	Number      int    `json:"number" gorm:"uniqueIndex:idx_shortplay_episodes_shortplay_number,priority:2"`
	Title       string `json:"title" gorm:"size:240"`
	ScriptText  string `json:"scriptText" gorm:"type:text"`
	// CanvasID 是一集一画布的唯一地址，建集时分配，永远不为空。
	CanvasID     string    `json:"canvasId" gorm:"size:80;not null;uniqueIndex:idx_shortplay_episodes_canvas;check:shortplay_episode_canvas_id,canvas_id <> ''"`
	AssetIDsJSON string    `json:"assetIdsJson" gorm:"type:text"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ShortplayAsset 是资产身份行 + 一张生效参考图。
//
// 身份是复合主键 (shortplay_id, id)：id 是项目内从 1 开始的全局自增序号（角色/道具/
// 场景混编），名称只是展示标签，可重复，不承载身份。
type ShortplayAsset struct {
	ShortplayID string `json:"shortplayId" gorm:"primaryKey;size:36"`
	ID          int    `json:"id" gorm:"primaryKey;autoIncrement:false"`
	Kind        string `json:"kind" gorm:"size:16"`
	Name        string `json:"name" gorm:"size:120"`
	AliasesJSON string `json:"aliasesJson" gorm:"type:text"`
	// Description 是叙事设定，Appearance 是可直接生图的外貌描述（生图模板只用后者）。
	Description string `json:"description" gorm:"type:text"`
	Appearance  string `json:"appearance" gorm:"type:text"`
	// Voice 是角色专属的音色描述提示词，道具与场景留空。
	Voice string `json:"voice" gorm:"type:text"`
	// Locked 是参考图锁：为 true 时自动生图跳过本行，用户手动换图仍可以。
	Locked bool `json:"locked"`
	// Revision 是身份/设定的行级 CAS 版本，每次写入自增。
	Revision int64 `json:"revision"`
	// ImageResourceID 是读时投影，不落列：当前图的唯一事实源是 CurrentImageID。
	ImageResourceID string `json:"-" gorm:"-"`
	// ImageSourceID 是当前图片行的 source_image_id，用于衍生过期判定。
	ImageSourceID int `json:"-" gorm:"-"`
	// ResolvedDescription / ResolvedRenderRule 是衍生行的展示值（本行为空时回落父资产）。
	ResolvedDescription string `json:"-" gorm:"-"`
	ResolvedRenderRule  string `json:"-" gorm:"-"`
	// BaseImageStale 表示衍生形象的当前图不是按父资产当前图生成的。
	BaseImageStale bool   `json:"-" gorm:"-"`
	ImageStatus    string `json:"imageStatus" gorm:"size:16"`
	// ImageVersion 每次参考图变化 +1，是「图变了」的单调信号，不是并发锁。
	ImageVersion       int    `json:"imageVersion"`
	EpisodeNumbersJSON string `json:"episodeNumbersJson" gorm:"type:text"`
	Position           int    `json:"position"`
	// DerivedFrom 非空表示本行是 DerivedFrom 指向角色的衍生形象（造型）。
	DerivedFrom int    `json:"derivedFrom,omitempty" gorm:"index"`
	VariantName string `json:"variantName,omitempty" gorm:"size:32"`
	RenderRule  string `json:"renderRule,omitempty" gorm:"type:text"`
	// CurrentImageID / CurrentAudioID 指向当前生效的产物行；空表示从未生成。
	CurrentImageID int    `json:"currentImageId,omitempty" gorm:"index"`
	CurrentAudioID int    `json:"currentAudioId,omitempty" gorm:"index"`
	AudioStatus    string `json:"audioStatus" gorm:"size:16"`
	AudioVersion   int    `json:"audioVersion"`
	// AudioResourceID / AudioSourceID 是当前音频行的读时投影，与图片同形。
	AudioResourceID string `json:"-" gorm:"-"`
	AudioSourceID   int    `json:"-" gorm:"-"`
	// BaseAudioStale 表示衍生形象自己的音色样本不是按父资产当前音频生成的。
	BaseAudioStale bool      `json:"-" gorm:"-"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// ShortplayAssetImage 是一次出图的产物行；设定版本在 ShortplayAsset.Revision。
type ShortplayAssetImage struct {
	ShortplayID string `json:"shortplayId" gorm:"primaryKey;size:36;uniqueIndex:idx_shortplay_asset_images_version,priority:1"`
	ID          int    `json:"id" gorm:"primaryKey;autoIncrement:false"`
	AssetID     int    `json:"assetId" gorm:"index;uniqueIndex:idx_shortplay_asset_images_version,priority:2"`
	Version     int    `json:"version" gorm:"uniqueIndex:idx_shortplay_asset_images_version,priority:3"`
	// Prompt / Appearance 是本版实际使用的提示词与外貌原文，便于复现。
	Prompt     string `json:"prompt" gorm:"type:text"`
	Appearance string `json:"appearance" gorm:"type:text"`
	// SourceImageID 是衍生形象专用：本版以父资产的哪一版为底图。
	SourceImageID int       `json:"sourceImageId,omitempty"`
	ResourceID    string    `json:"resourceId" gorm:"size:36;index"`
	Status        string    `json:"status" gorm:"size:16"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ShortplayAssetAudio 是一次音频生成的产物行，与图片一一对应。
type ShortplayAssetAudio struct {
	ShortplayID   string    `json:"shortplayId" gorm:"primaryKey;size:36;uniqueIndex:idx_shortplay_asset_audios_version,priority:1"`
	ID            int       `json:"id" gorm:"primaryKey;autoIncrement:false"`
	AssetID       int       `json:"assetId" gorm:"index;uniqueIndex:idx_shortplay_asset_audios_version,priority:2"`
	Version       int       `json:"version" gorm:"uniqueIndex:idx_shortplay_asset_audios_version,priority:3"`
	Prompt        string    `json:"prompt" gorm:"type:text"`
	SourceAudioID int       `json:"sourceAudioId,omitempty"`
	ResourceID    string    `json:"resourceId" gorm:"size:36;index"`
	Status        string    `json:"status" gorm:"size:16"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ShortplayJob 是调度队列中的一项工作；真实任务在派发时创建，保证用最新的资产数据。
type ShortplayJob struct {
	ID          string `json:"id" gorm:"primaryKey;size:36"`
	ShortplayID string `json:"shortplayId" gorm:"size:36;index:idx_shortplay_jobs_shortplay_status,priority:1"`
	CanvasID    string `json:"canvasId,omitempty" gorm:"size:80;index"`
	BatchID     string `json:"batchId,omitempty" gorm:"size:36"`
	Kind        string `json:"kind" gorm:"size:24"`
	Status      string `json:"status" gorm:"size:16;index:idx_shortplay_jobs_shortplay_status,priority:2"`
	// DedupeKey 见 shortplay.JobDedupeKey；唯一索引只覆盖未结束的行。
	DedupeKey        string     `json:"-" gorm:"size:160;uniqueIndex:idx_shortplay_jobs_dedupe_active,where:dedupe_key <> '' AND (status = 'queued' OR status = 'running')"`
	Priority         int        `json:"priority"`
	Attempt          int        `json:"attempt"`
	TaskID           string     `json:"taskId,omitempty" gorm:"index;size:36"`
	PayloadJSON      string     `json:"-" gorm:"type:text"`
	AttemptsJSON     string     `json:"-" gorm:"type:text"`
	LastError        string     `json:"lastError,omitempty" gorm:"size:500"`
	CostMicrocredits int64      `json:"costMicrocredits"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	FinishedAt       *time.Time `json:"finishedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// DisplayDescription / DisplayRenderRule 是展示与生图读取用的值。
func (a *ShortplayAsset) DisplayDescription() string {
	if a == nil {
		return ""
	}
	if a.ResolvedDescription != "" {
		return a.ResolvedDescription
	}
	return a.Description
}

func (a *ShortplayAsset) DisplayRenderRule() string {
	if a == nil {
		return ""
	}
	if a.ResolvedRenderRule != "" {
		return a.ResolvedRenderRule
	}
	return a.RenderRule
}
