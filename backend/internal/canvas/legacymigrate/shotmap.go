package legacymigrate

import (
	"fmt"
	"sort"
	"strings"
)

// ShotSubmap is the read-only mapping rule from a 4AI storyboard row to the
// donor shot workbench: one row becomes one submap container plus flattened
// image/video/prompt children. S13 applies the rule and allocates production
// identities; this package only records the shape and the preserved fields.
type ShotSubmap struct {
	SourceNodeID     string            `json:"sourceNodeId"`
	RowID            string            `json:"rowId"`
	SubmapID         string            `json:"submapId"`
	Title            string            `json:"title"`
	ShotNumber       int               `json:"shotNumber"`
	SceneID          string            `json:"sceneId,omitempty"`
	SceneSnapshotURL string            `json:"sceneSnapshotUrl,omitempty"`
	DurationSeconds  float64           `json:"durationSeconds,omitempty"`
	ImageNodeID      string            `json:"imageNodeId,omitempty"`
	VideoNodeID      string            `json:"videoNodeId,omitempty"`
	ExistingImage    bool              `json:"existingImage"`
	ExistingVideo    bool              `json:"existingVideo"`
	FirstFrameNodeID string            `json:"firstFrameNodeId,omitempty"`
	ImageReferences  []string          `json:"imageReferences"`
	VideoReferences  []string          `json:"videoReferences"`
	AssetNodeIDs     []string          `json:"assetNodeIds"`
	Assets           []ShotAsset       `json:"assets"`
	CharacterNodeIDs []string          `json:"characterNodeIds"`
	TextNodeIDs      []string          `json:"textNodeIds"`
	Children         []ShotSubmapChild `json:"children"`
	Preserved        map[string]any    `json:"preserved"`
}

type ShotAsset struct {
	NodeID        string  `json:"nodeId"`
	Role          string  `json:"role,omitempty"`
	Priority      float64 `json:"priority,omitempty"`
	StorageKey    string  `json:"storageKey,omitempty"`
	AssetID       string  `json:"assetId,omitempty"`
	MediaVersions int     `json:"mediaVersions,omitempty"`
}

type ShotSubmapChild struct {
	LocalID      string   `json:"localId"`
	NodeID       string   `json:"nodeId"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	Content      string   `json:"content,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	SourceField  string   `json:"sourceField"`
	References   []string `json:"references,omitempty"`
	FirstFrameID string   `json:"firstFrameNodeId,omitempty"`
	Seconds      string   `json:"seconds,omitempty"`
}

func mapStoryboardShotSubmaps(doc map[string]any) []ShotSubmap {
	byID := map[string]map[string]any{}
	nodes := list(doc["nodes"])
	for _, node := range nodes {
		byID[text(node["id"])] = node
	}
	out := []ShotSubmap{}
	ordinal := 0
	for _, node := range nodes {
		if text(node["type"]) != "script" {
			continue
		}
		board := object(object(node["metadata"])["storyboard"])
		for _, row := range list(board["rows"]) {
			ordinal++
			out = append(out, mapStoryboardRow(text(node["id"]), row, nodes, byID, ordinal))
		}
	}
	return out
}

func mapStoryboardRow(scriptID string, row map[string]any, nodes []map[string]any, byID map[string]map[string]any, ordinal int) ShotSubmap {
	submapID := fmt.Sprintf("preview-submap-%d", ordinal)
	rowID := text(row["id"])
	imageID := text(row["imageNodeId"])
	videoID := text(row["videoNodeId"])
	existingImage := imageID != "" && byID[imageID] != nil
	existingVideo := videoID != "" && byID[videoID] != nil
	if imageID == "" {
		imageID = submapID + "__image-1"
	}
	if videoID == "" {
		videoID = submapID + "__video-1"
	}
	textIDs := []string{submapID + "__text-1", submapID + "__text-2", submapID + "__text-3", submapID + "__text-4"}
	children := []ShotSubmapChild{
		{LocalID: "text-1", NodeID: textIDs[0], Type: "text", Title: "图片提示词", Content: text(row["imageGenerationPrompt"]), SourceField: "imageGenerationPrompt"},
		{LocalID: "text-2", NodeID: textIDs[1], Type: "text", Title: "视频提示词", Content: firstText(row["videoMotionPrompt"], row["motion"]), SourceField: "videoMotionPrompt"},
		{LocalID: "text-3", NodeID: textIDs[2], Type: "text", Title: "台词/旁白", Content: text(row["dialogue"]), SourceField: "dialogue"},
		{LocalID: "text-4", NodeID: textIDs[3], Type: "text", Title: "备注", Content: text(row["plotDescription"]), SourceField: "plotDescription"},
	}
	if !existingImage {
		children = append(children, ShotSubmapChild{
			LocalID: "image-1", NodeID: imageID, Type: "image", Title: "首帧",
			Prompt: "@文本1", SourceField: "imageGenerationPrompt", References: []string{textIDs[0]},
		})
	}
	assets, assetIDs := shotAssets(row, byID)
	characters := shotCharacters(row, nodes)
	assetIDs = appendUnique(assetIDs, characters...)
	videoReferences := append([]string{textIDs[0], textIDs[1], textIDs[2]}, assetIDs...)
	if !existingVideo {
		children = append(children, ShotSubmapChild{
			LocalID: "video-1", NodeID: videoID, Type: "video", Title: "视频",
			Prompt: "@文本1\n@文本2\n台词/旁白：@文本3", SourceField: "videoMotionPrompt",
			References: videoReferences, FirstFrameID: imageID, Seconds: secondsString(row["durationSeconds"]),
		})
	}
	shotNumber := int(number(row["shotNumber"]))
	if shotNumber <= 0 {
		shotNumber = ordinal
	}
	title := fmt.Sprintf("镜头 %d", shotNumber)
	preserved := map[string]any{}
	for _, key := range []string{
		"shotNumber", "locked", "mediaReview", "status", "errorDetails",
		"sourceStartMs", "sourceEndMs", "keyframeTimeMs", "previsSceneId",
		"previsSnapshotUrl", "imageNodeId", "videoNodeId", "assetBindings", "characters",
	} {
		if value, ok := row[key]; ok {
			preserved[key] = value
		}
	}
	return ShotSubmap{
		SourceNodeID: scriptID, RowID: rowID, SubmapID: submapID, Title: title,
		ShotNumber: shotNumber, SceneID: text(row["previsSceneId"]),
		SceneSnapshotURL: text(row["previsSnapshotUrl"]), DurationSeconds: number(row["durationSeconds"]),
		ImageNodeID: imageID, VideoNodeID: videoID, ExistingImage: existingImage, ExistingVideo: existingVideo,
		FirstFrameNodeID: imageID, ImageReferences: append([]string{textIDs[0]}, assetIDs...), VideoReferences: videoReferences,
		AssetNodeIDs: assetIDs, Assets: assets, CharacterNodeIDs: characters,
		TextNodeIDs: textIDs, Children: children, Preserved: preserved,
	}
}

func shotAssets(row map[string]any, byID map[string]map[string]any) ([]ShotAsset, []string) {
	bindings := list(row["assetBindings"])
	sort.SliceStable(bindings, func(i, j int) bool { return number(bindings[i]["priority"]) > number(bindings[j]["priority"]) })
	assets := []ShotAsset{}
	ids := []string{}
	for _, binding := range bindings {
		nodeID := text(binding["nodeId"])
		if nodeID == "" {
			continue
		}
		asset := ShotAsset{NodeID: nodeID, Role: text(binding["role"]), Priority: number(binding["priority"])}
		if node := byID[nodeID]; node != nil {
			metadata := object(node["metadata"])
			asset.StorageKey, asset.AssetID = text(metadata["storageKey"]), text(metadata["assetId"])
			asset.MediaVersions = len(list(metadata["mediaVersions"]))
		}
		assets = append(assets, asset)
		ids = appendUnique(ids, nodeID)
	}
	return assets, ids
}

func shotCharacters(row map[string]any, nodes []map[string]any) []string {
	ids := []string{}
	for _, character := range list(row["characters"]) {
		if nodeID := text(character["characterImageNodeId"]); nodeID != "" {
			ids = appendUnique(ids, nodeID)
			continue
		}
		assetID := text(character["characterAssetId"])
		if assetID == "" {
			continue
		}
		for _, node := range nodes {
			if text(object(node["metadata"])["characterAssetId"]) == assetID {
				ids = appendUnique(ids, text(node["id"]))
				break
			}
		}
	}
	return ids
}

func appendUnique(ids []string, values ...string) []string {
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range values {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func firstText(values ...any) string {
	for _, value := range values {
		if result := strings.TrimSpace(text(value)); result != "" {
			return result
		}
	}
	return ""
}

func secondsString(value any) string {
	seconds := number(value)
	if seconds <= 0 {
		return ""
	}
	return fmt.Sprintf("%g", seconds)
}
