package legacymigrate

import (
	"fmt"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/references"
)

// ShotMigrationResult is the applied document plus the row/output counts S13
// records in the migration receipt. The source document is never mutated.
type ShotMigrationResult struct {
	Document        map[string]any
	Submaps         int
	Rows            int
	ExistingImages  int
	ExistingVideos  int
	PreservedAssets int
}

type shotBinding struct {
	nodeID    string
	role      string
	mediaType string
}

// ApplyShotSubmaps converts every storyboard row into one flat submap scope.
// Existing image/video outputs stay where they are and are referenced as the
// shot's first frame/video result, so result resources keep their ownership and
// task bindings. New prompt/image/video children receive production IDs from
// the caller's allocator; the caller also persists the allocator's idCounters.
func ApplyShotSubmaps(doc map[string]any, allocate func(prefix string) string) (ShotMigrationResult, error) {
	next, err := references.Clone(doc)
	if err != nil {
		return ShotMigrationResult{}, err
	}
	if allocate == nil {
		return ShotMigrationResult{}, fmt.Errorf("分镜迁移缺少节点发号器")
	}
	nodes := list(next["nodes"])
	byID := make(map[string]map[string]any, len(nodes))
	for _, node := range nodes {
		byID[text(node["id"])] = node
	}
	registry := capability.BuiltinRegistry()
	result := ShotMigrationResult{Document: next}
	created := []map[string]any{}
	for _, node := range nodes {
		if text(node["type"]) != "script" || text(node["scopeId"]) != "" {
			continue
		}
		metadata := object(node["metadata"])
		board := object(metadata["storyboard"])
		rows := list(board["rows"])
		if len(rows) == 0 {
			continue
		}
		migratedIDs := []string{}
		for index, row := range rows {
			container, children, stats, err := buildShotSubmap(node, row, byID, nodes, registry, allocate, index)
			if err != nil {
				return ShotMigrationResult{}, err
			}
			created = append(created, container)
			created = append(created, children...)
			byID[text(container["id"])] = container
			for _, child := range children {
				byID[text(child["id"])] = child
			}
			migratedIDs = append(migratedIDs, text(container["id"]))
			result.Submaps++
			result.Rows++
			result.ExistingImages += stats.ExistingImages
			result.ExistingVideos += stats.ExistingVideos
			result.PreservedAssets += stats.PreservedAssets
		}
		board["rows"] = []any{}
		board["shotMigration"] = map[string]any{
			"version": 1, "migratedRows": len(rows), "submapIds": stringListAsAny(migratedIDs),
		}
		metadata["storyboard"] = board
		node["metadata"] = metadata
	}
	if result.Rows == 0 {
		return result, nil
	}
	next["nodes"] = append(anyList(nodes), anyList(created)...)
	next["referenceFieldsVersion"] = 1
	return result, nil
}

type shotRowStats struct {
	ExistingImages  int
	ExistingVideos  int
	PreservedAssets int
}

func buildShotSubmap(script, row map[string]any, byID map[string]map[string]any, nodes []map[string]any, registry *capability.Registry, allocate func(string) string, index int) (map[string]any, []map[string]any, shotRowStats, error) {
	submapID := allocate("submap")
	rowID := text(row["id"])
	shotNumber := int(number(row["shotNumber"]))
	if shotNumber <= 0 {
		shotNumber = index + 1
	}
	x, y := shotSubmapPosition(script, index)
	stats := shotRowStats{PreservedAssets: len(list(row["assetBindings"]))}
	imageID := text(row["imageNodeId"])
	existingImage := imageID != "" && byID[imageID] != nil
	if existingImage {
		stats.ExistingImages = 1
	}
	videoID := text(row["videoNodeId"])
	existingVideo := videoID != "" && byID[videoID] != nil
	if existingVideo {
		stats.ExistingVideos = 1
	}
	container := map[string]any{
		"id": submapID, "type": "submap", "title": fmt.Sprintf("镜头 %d", shotNumber),
		"position": map[string]any{"x": x, "y": y}, "width": 920.0, "height": 760.0,
		"metadata": map[string]any{
			"status": "idle", "collapsed": false, "workflowId": "storyboard", "workflowVersion": 1,
			"shotMigration": map[string]any{
				"version": 1, "sourceScriptId": text(script["id"]), "sourceRowId": rowID,
				"shotNumber": shotNumber, "preserved": shotPreserved(row),
			},
		},
	}
	textIDs := make([]string, 4)
	for i := range textIDs {
		textIDs[i] = allocate(submapID + "__text")
	}
	imageText := text(row["imageGenerationPrompt"])
	videoText := firstText(row["videoMotionPrompt"], row["motion"])
	children := []map[string]any{
		shotTextNode(textIDs[0], submapID, 0, "图片提示词", imageText),
		shotTextNode(textIDs[1], submapID, 1, "视频提示词", videoText),
		shotTextNode(textIDs[2], submapID, 2, "台词/旁白", text(row["dialogue"])),
		shotTextNode(textIDs[3], submapID, 3, "备注", text(row["plotDescription"])),
	}
	assets, assetIDs := shotAssets(row, byID)
	characters := shotCharacters(row, nodes)
	assetIDs = appendUnique(assetIDs, characters...)
	if len(assets) > 0 || len(characters) > 0 {
		stats.PreservedAssets = len(assets) + len(characters)
	}
	if !existingImage {
		imageID = allocate(submapID + "__image")
		children = append(children, shotMediaNode(imageID, submapID, "image", "首帧", 4, []string{textIDs[0]}, []shotBinding{
			{nodeID: textIDs[0], role: "source-text", mediaType: "text"},
		}, imageText))
	}
	if !existingVideo {
		videoID = allocate(submapID + "__video")
		videoPrompt := "@文本1\n@文本2\n台词/旁白：@文本3"
		bindings := []shotBinding{
			{nodeID: textIDs[0], role: "source-text", mediaType: "text"},
			{nodeID: textIDs[1], role: "source-text", mediaType: "text"},
			{nodeID: textIDs[2], role: "source-text", mediaType: "text"},
		}
		for _, assetID := range assetIDs {
			mediaType := shotMediaType(registry, byID[assetID])
			role := "reference"
			if mediaType == "text" {
				role = "source-text"
			}
			if mediaType == "" {
				continue
			}
			bindings = append(bindings, shotBinding{nodeID: assetID, role: role, mediaType: mediaType})
		}
		if imageID != "" {
			bindings = append(bindings, shotBinding{nodeID: imageID, role: "first-frame", mediaType: "image"})
		}
		video := shotMediaNode(videoID, submapID, "video", "视频", 5, []string{textIDs[0], textIDs[1], textIDs[2]}, bindings, videoPrompt)
		metadata := object(video["metadata"])
		metadata["referenceNodeIds"] = stringListAsAny(assetIDs)
		metadata["seconds"] = secondsString(row["durationSeconds"])
		metadata["videoStartFrameNodeId"] = imageID
		spec := object(metadata["generationSpec"])
		if seconds := int(number(row["durationSeconds"])); seconds > 0 {
			options := object(spec["options"])
			options["durationSeconds"] = seconds
			spec["options"] = options
		}
		video["metadata"] = metadata
		children = append(children, video)
	}
	return container, children, stats, nil
}

func shotSubmapPosition(script map[string]any, index int) (float64, float64) {
	position := object(script["position"])
	x := number(position["x"]) + number(script["width"]) + 80
	y := number(position["y"]) + float64(index)*820
	return x, y
}

func shotTextNode(id, scopeID string, index int, title, content string) map[string]any {
	column, row := index%3, index/3
	x := 24.0 + float64(column)*296
	y := 60.0 + float64(row)*256
	return map[string]any{
		"id": id, "type": "text", "title": title, "scopeId": scopeID,
		"position": map[string]any{"x": x, "y": y}, "width": 340.0, "height": 240.0,
		"metadata": map[string]any{"content": content, "fontSize": 14.0, "status": "idle"},
	}
}

func shotMediaNode(id, scopeID, nodeType, title string, index int, textIDs []string, bindings []shotBinding, specPrompt string) map[string]any {
	column, row := index%3, index/3
	x := 24.0 + float64(column)*296
	y := 60.0 + float64(row)*256
	mode, options := "image", map[string]any{"size": "16:9", "imageResolution": "1k"}
	if nodeType == "video" {
		mode, options = "video", map[string]any{"size": "16:9", "resolution": "720"}
	}
	return map[string]any{
		"id": id, "type": nodeType, "title": title, "scopeId": scopeID,
		"position": map[string]any{"x": x, "y": y}, "width": 720.0, "height": 405.0,
		"metadata": map[string]any{
			"status": "idle", "prompt": "", "referenceTextNodeIds": stringListAsAny(textIDs),
			"generationSpec": map[string]any{
				"version": 1, "mode": mode, "prompt": specPrompt, "options": options,
				"referenceBindings": shotReferenceBindings(bindings), "textInputMode": "append-sources",
			},
		},
	}
}

func shotReferenceBindings(bindings []shotBinding) []any {
	out := []any{}
	seen := map[string]bool{}
	order := 0
	for _, binding := range bindings {
		if binding.nodeID == "" || binding.mediaType == "" {
			continue
		}
		id := "node:" + binding.nodeID + ":" + binding.role
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, map[string]any{
			"id": id, "nodeId": binding.nodeID, "mediaType": binding.mediaType,
			"role": binding.role, "order": order, "resolution": "latest",
		})
		order++
	}
	return out
}

func shotMediaType(registry *capability.Registry, node map[string]any) string {
	if node == nil {
		return ""
	}
	metadata := object(node["metadata"])
	descriptor, ok := registry.ResolveNode(text(node["type"]), text(metadata["workflowKind"]))
	if !ok {
		return ""
	}
	if descriptor.InputKind == "character" {
		return "image"
	}
	return descriptor.InputKind
}

func shotPreserved(row map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		"shotNumber", "locked", "mediaReview", "status", "errorDetails",
		"sourceStartMs", "sourceEndMs", "keyframeTimeMs", "previsSceneId",
		"previsSnapshotUrl", "imageNodeId", "videoNodeId", "assetBindings",
		"characters", "durationSeconds", "dialogue", "plotDescription",
		"imageGenerationPrompt", "videoMotionPrompt", "motion",
	} {
		if value, ok := row[key]; ok {
			out[key] = value
		}
	}
	return out
}

func anyList(items []map[string]any) []any {
	out := make([]any, len(items))
	for index, item := range items {
		out[index] = item
	}
	return out
}

func stringListAsAny(items []string) []any {
	out := make([]any, len(items))
	for index, item := range items {
		out[index] = item
	}
	return out
}
