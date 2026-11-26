package shortplay

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/production"
)

// W05 参考图与音色的纯规则：任务载荷、入队筛选与画幅选择。
// 真实任务提交与回写留在 app；本文件不 import 上游执行入口。

// DefaultRenderRule 是衍生形象的生图纪律默认值。只在域内写一次：
// 模板里不再重复否定句，衍生/基础都按「本行 → 父资产 → 本常量」的回落链取值。
const DefaultRenderRule = "参考图1是角色既有形象，保持五官、脸型与体态完全一致，只改服装、发型与配饰。"

// QueueAssetImagesRequest 是页面「生成参考图」的筛选条件。
//
// 三种组合：单个资产（AssetIDs）、某类的「生成全部」（Kind + OnlyMissing）、
// 某类的「重试失败」（Kind + OnlyFailed）。都留空表示全部资产。
type QueueAssetImagesRequest struct {
	Kind        string `json:"kind"`
	AssetIDs    []int  `json:"assetIds"`
	OnlyMissing bool   `json:"onlyMissing"`
	OnlyFailed  bool   `json:"onlyFailed"`
}

// ImageQueueResult 是三档计数：新排队、已在途复用、被跳过（锁定 / 不在筛选范围）。
// Reused 不是失败：同一资产已有一条在途出图，重复点按钮不该排第二条。
type ImageQueueResult struct {
	Queued  int      `json:"queued"`
	Reused  int      `json:"reused"`
	Skipped int      `json:"skipped"`
	JobIDs  []string `json:"jobIds"`
}

// AssetImageJobPayload 是出图 job 的载荷。sourceImageId / prompt / appearance 在提交那一刻
// 固定：提交与回写之间父资产可能换图，回写时重算会把 source_image_id 记成一张没参与生成的图。
type AssetImageJobPayload struct {
	AssetID       int    `json:"assetId"`
	SourceImageID int    `json:"sourceImageId,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	Appearance    string `json:"appearance,omitempty"`
}

// Encode 序列化载荷；失败时返回带 assetId 的最小载荷（AssetID 不能丢）。
func (p AssetImageJobPayload) Encode() string {
	raw, err := json.Marshal(p)
	if err != nil {
		return `{"assetId":` + itoa(p.AssetID) + `}`
	}
	return string(raw)
}

// ParseAssetImageJob 解析载荷；assetId 也兼容旧的纯数字写法。
func ParseAssetImageJob(raw string) AssetImageJobPayload {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AssetImageJobPayload{}
	}
	var payload AssetImageJobPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		var id int
		if json.Unmarshal([]byte(raw), &id) == nil {
			return AssetImageJobPayload{AssetID: id}
		}
		return AssetImageJobPayload{}
	}
	return payload
}

// QueueAssetAudioRequest 是页面「生成音色」的筛选条件，与图片同构。
// Audio 只对角色有意义，非角色一律计入 Skipped。
type QueueAssetAudioRequest struct {
	AssetIDs    []int `json:"assetIds"`
	OnlyMissing bool  `json:"onlyMissing"`
	OnlyFailed  bool  `json:"onlyFailed"`
}

// AudioQueueResult 与 ImageQueueResult 同形：新排队、在途复用、跳过。
type AudioQueueResult struct {
	Queued  int      `json:"queued"`
	Reused  int      `json:"reused"`
	Skipped int      `json:"skipped"`
	JobIDs  []string `json:"jobIds"`
}

// AssetAudioJobPayload 是音色 job 的载荷。sourceAudioId / prompt 在提交那一刻固定，
// 理由与图片相同：提交与回写之间父资产可能换样本。
type AssetAudioJobPayload struct {
	AssetID       int    `json:"assetId"`
	SourceAudioID int    `json:"sourceAudioId,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
}

// Encode 序列化载荷；失败时返回带 assetId 的最小载荷。
func (p AssetAudioJobPayload) Encode() string {
	raw, err := json.Marshal(p)
	if err != nil {
		return `{"assetId":` + itoa(p.AssetID) + `}`
	}
	return string(raw)
}

// ParseAssetAudioJob 解析载荷；assetId 也兼容纯数字写法。
func ParseAssetAudioJob(raw string) AssetAudioJobPayload {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AssetAudioJobPayload{}
	}
	var payload AssetAudioJobPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		var id int
		if json.Unmarshal([]byte(raw), &id) == nil {
			return AssetAudioJobPayload{AssetID: id}
		}
		return AssetAudioJobPayload{}
	}
	return payload
}

// AudioOperation 是音频任务的标准 operation：仓里唯一在用的音频 token。
// 音频能力规格目前没有可编辑渠道 JSON，所以不按规格挑 operation。
const AudioOperation = "text_to_speech"

// AssetImageRatio 是某类资产的参考图画幅：场景与道具统一横版 16:9（要容纳完整空间、
// 留足展示余量），角色跟参考图方案。
func AssetImageRatio(assets production.AssetSettings, kind string) string {
	if kind == AssetCharacter {
		if ratio := strings.TrimSpace(assets.CharacterRatio); ratio != "" {
			return ratio
		}
	}
	return production.AssetImageRatio
}

// itoa 避免为一个整数再引 strconv 到调用点，保持载荷降级路径简单。
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
