package shortplay

// W06 集画布资产卡与收录的请求/回执形状。
//
// 节点本身的字段名与几何留在 app 层：那是画布文档合同，不是短剧域规则；
// 这里只定义 HTTP 入参与回执，handler 与页面共用。

// AdoptAssetRequest 是收录一次局部卡的入参：源画布、源节点与目标集。
// 只接受这三个身份：卡片内容一律从节点快照读，不接受请求体覆盖。
type AdoptAssetRequest struct {
	CanvasID  string `json:"canvasId"`
	NodeID    string `json:"nodeId"`
	EpisodeID string `json:"episodeId"`
}

// AdoptAssetResult 是收录回执：新资产行、目标集上新卡的节点 ID、是否新建行与是否带入了图。
type AdoptAssetResult struct {
	AssetID        int    `json:"assetId"`
	NodeID         string `json:"nodeId"`
	Created        bool   `json:"created"`
	ImageAdopted   bool   `json:"imageAdopted"`
	Revision       int64  `json:"revision"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	EpisodeNumbers []int  `json:"episodeNumbers"`
	Note           string `json:"note,omitempty"`
}

// PlaceAssetRequest 是「投送资产卡进集画布」的入参：一次投送若干张，已在该集的卡只刷新。
type PlaceAssetRequest struct {
	AssetIDs []int `json:"assetIds"`
}

// PlaceAssetResult 是投送回执：实际新建的卡数与目标节点 ID（顺序同请求）。
type PlaceAssetResult struct {
	Placed         int      `json:"placed"`
	NodeIDs        []string `json:"nodeIds"`
	EpisodeNumbers []int    `json:"episodeNumbers"`
}
