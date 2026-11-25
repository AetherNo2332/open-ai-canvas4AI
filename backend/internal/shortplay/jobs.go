package shortplay

// 后台 job 的四种唯一执行单位与状态机。任务在派发时创建真实模型任务，保证用最新资产数据。
//
// 供体固定提交 13731aae6 的 shortplay/model.go；本文件只保留纯规则，不 import 任何上游包。

const (
	// JobExtract 读全部 scriptText，产资产行并回写每集 assetIdsJSON。
	JobExtract = "extract"
	// JobAssetImage 按资产当前设定出图，产物写回 asset.current_image_id。
	JobAssetImage = "asset_image"
	// JobAssetAudio 按资产的 voice 文本生成音色样本，产物写回 asset.current_audio_id。
	JobAssetAudio = "asset_audio"
	// JobAssetSync 是画布节点字段回写表的失败补偿，只在同步失败时入队。
	JobAssetSync = "asset_sync"

	// AssetCharacter / AssetProp / AssetScene 是短剧域的三类资产，不映射到素材库分类。
	AssetCharacter = "character"
	AssetProp      = "prop"
	AssetScene     = "scene"

	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCanceled  = "canceled"

	// 参考图生命周期，取值与 drama 的媒体状态一一对应；空串表示从未生成。
	ImageQueued  = "queued"
	ImageRunning = "running"
	ImageReady   = "ready"
	ImageFailed  = "failed"
)

// JobDedupeKey 拼 job 去重键，只有四种合法拼法：
//
//	extract     → kind|shortplayId   整部作品同时只跑一次提取
//	asset_image → kind|shortplayId|assetId   资产 id 只在项目内唯一，必须带作品 ID
//	asset_audio → kind|shortplayId|assetId
//	asset_sync  → kind|canvasId      按画布补偿，一次落库对应一条
func JobDedupeKey(kind, target string) string {
	return kind + "|" + target
}

// ImageInFlight 是「这张图正在生成」的状态集合：入队去重与回写 CAS 共用。
func ImageInFlight() []string {
	return []string{ImageQueued, ImageRunning}
}

// ImageAnyState 是「用户当场要的图」的前置状态集合：画布显式生图的产物回写用它。
// 与 ImageInFlight 的差别不是宽松，而是写者不同：job 出图只从在途收口（旧任务撞到
// ready 就认输），画布点生成是显式操作，任何状态都该写得进去。
func ImageAnyState() []string {
	return []string{ImageQueued, ImageRunning, ImageReady, ImageFailed, ""}
}

// AudioInFlight 是音色样本「正在生成」的状态集合，语义同 ImageInFlight。
func AudioInFlight() []string {
	return []string{ImageQueued, ImageRunning}
}

// UnfinishedJobStatuses 是仍占用去重键的状态。
func UnfinishedJobStatuses() []string {
	return []string{JobQueued, JobRunning}
}

// JobKindValid 校验四种 job kind。
func JobKindValid(kind string) bool {
	switch kind {
	case JobExtract, JobAssetImage, JobAssetAudio, JobAssetSync:
		return true
	}
	return false
}
