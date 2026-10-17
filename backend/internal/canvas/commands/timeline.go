package commands

import (
	"infinite-canvas/backend/internal/assets"
	"math"
)

var timelineTrackFields = map[string]string{"kind": "kind", "label": "string", "order": "number", "locked": "bool", "visible": "bool", "muted": "bool"}
var timelineClipFields = map[string]string{"kind": "kind", "nodeId": "string", "trackId": "string", "startMs": "number", "durationMs": "positive", "title": "string", "sourceStartMs": "number", "sourceDurationMs": "number", "volume": "volume", "fadeInMs": "number", "fadeOutMs": "number", "subtitleEntryIndex": "index", "text": "string", "directMedia": "media"}
var timelineMediaFields = map[string]string{"id": "string", "assetId": "string", "kind": "mediaKind", "title": "string", "storageKey": "locator", "url": "locator", "dataUrl": "locator", "content": "string", "durationMs": "number", "width": "positive", "height": "positive", "bytes": "number", "mimeType": "string"}

func timelineKind(value string) bool {
	return value == "video" || value == "audio" || value == "subtitle" || value == "text" || value == "image"
}
func validateTimelinePatch(patch map[string]any, fields map[string]string) error {
	for key, value := range patch {
		rule, ok := fields[key]
		if !ok {
			return BadAuthRequest("时间线字段不可编辑：" + key)
		}
		if value == nil {
			continue
		}
		valid := false
		switch rule {
		case "string":
			s, ok := value.(string)
			valid = ok && len(s) <= 1<<20
		case "kind":
			valid = timelineKind(text(value))
		case "mediaKind":
			valid = timelineKind(text(value)) && text(value) != "subtitle"
		case "bool":
			_, valid = value.(bool)
		case "locator":
			s, ok := value.(string)
			valid = ok && (s == "" || assets.ResourceID(s) != "")
		case "media":
			if m, ok := value.(map[string]any); ok {
				if err := validateTimelinePatch(m, timelineMediaFields); err != nil {
					return err
				}
				valid = true
			}
		default:
			n, ok := value.(float64)
			valid = ok && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 1e12
			if rule == "positive" {
				valid = valid && n > 0
			}
			if rule == "volume" {
				valid = valid && n <= 10
			}
			if rule == "index" {
				valid = ok && math.Trunc(n) == n && n >= -1 && n <= 1e6
			}
		}
		if !valid {
			return BadAuthRequest("时间线字段无效：" + key)
		}
	}
	return nil
}
func mergeTimelinePatch(object, patch map[string]any) {
	for key, value := range patch {
		if value == nil {
			delete(object, key)
		} else if key == "directMedia" {
			next, _ := object[key].(map[string]any)
			if next == nil {
				next = map[string]any{}
			}
			mergeTimelinePatch(next, value.(map[string]any))
			object[key] = next
		} else {
			object[key] = value
		}
	}
}
func (p *planner) editTimeline(doc Document, op Operation, ctx ValidationContext) error {
	if len(op.TimelineOperations) < 1 || len(op.TimelineOperations) > 10000 {
		return BadAuthRequest("时间线操作数量无效")
	}
	timeline, _ := doc["timeline"].(map[string]any)
	if timeline == nil {
		timeline = map[string]any{"version": 2.0, "tracks": []any{}, "clips": []any{}}
		doc["timeline"] = timeline
	}
	for _, change := range op.TimelineOperations {
		if change.Type == "clear" {
			if len(op.TimelineOperations) != 1 {
				return BadAuthRequest("清空时间线不能与其他操作组合")
			}
			delete(doc, "timeline")
			return nil
		}
		key, fields := "clips", timelineClipFields
		if change.Type == "upsert_track" || change.Type == "delete_track" || change.Type == "reorder_tracks" {
			key, fields = "tracks", timelineTrackFields
		}
		items := maps(timeline[key])
		if change.Type == "reorder_tracks" || change.Type == "reorder_clips" {
			order := change.TrackIDs
			if change.Type == "reorder_clips" {
				order = change.ClipIDs
			}
			next, err := p.canvasReorderItems(items, order)
			if err != nil {
				return err
			}
			if change.Type == "reorder_tracks" {
				for i, item := range next {
					item["order"] = float64(i)
				}
			}
			timeline[key] = p.mapsAsAny(next)
			continue
		}
		if err := validateCanvasID(change.ID, "时间线实体 ID", 128); err != nil {
			return err
		}
		index := p.canvasNodeIndex(items, change.ID)
		switch change.Type {
		case "upsert_track", "upsert_clip":
			if len(change.Patch) == 0 {
				return BadAuthRequest("时间线更新需要字段")
			}
			if err := validateTimelinePatch(change.Patch, fields); err != nil {
				return err
			}
			if index < 0 {
				items = append(items, map[string]any{"id": change.ID})
				index = len(items) - 1
			}
			mergeTimelinePatch(items[index], change.Patch)
		case "delete_track", "delete_clip":
			if index < 0 {
				return BadAuthRequest("时间线实体不存在")
			}
			items = append(items[:index:index], items[index+1:]...)
			if change.Type == "delete_track" {
				clips := []map[string]any{}
				for _, clip := range maps(timeline["clips"]) {
					if text(clip["trackId"]) != change.ID {
						clips = append(clips, clip)
					}
				}
				timeline["clips"] = p.mapsAsAny(clips)
			}
		default:
			return BadAuthRequest("不支持的时间线操作")
		}
		timeline[key] = p.mapsAsAny(items)
	}
	if err := validateTimeline(doc); err != nil {
		return err
	}
	for _, clip := range maps(timeline["clips"]) {
		media, _ := clip["directMedia"].(map[string]any)
		if media == nil || text(media["assetId"]) == "" {
			continue
		}
		if ctx.Asset == nil {
			return failure("resource_not_owned", "时间线素材未授权")
		}
		asset, err := ctx.Asset(text(media["assetId"]))
		if err != nil {
			return err
		}
		if asset.ID != text(media["assetId"]) || asset.Kind != text(media["kind"]) || asset.Status == "archived" {
			return failure("resource_not_owned", "时间线素材不可用")
		}
		if asset.Kind != "text" {
			for _, field := range []string{"storageKey", "url", "dataUrl"} {
				if id := assets.ResourceID(text(media[field])); id != "" && id != asset.ResourceID {
					return BadAuthRequest("时间线素材与资源记录不一致")
				}
			}
		}
	}
	timeline["version"] = 2.0
	timeline["updatedAt"] = p.now().UTC().Format("2006-01-02T15:04:05.999Z07:00")
	return nil
}
func validateTimeline(doc Document) error {
	timeline, _ := doc["timeline"].(map[string]any)
	if timeline == nil {
		return nil
	}
	tracks, clips := maps(timeline["tracks"]), maps(timeline["clips"])
	if len(tracks) > 1000 || len(clips) > 10000 {
		return BadAuthRequest("时间线轨道或片段过多")
	}
	ids := map[string]bool{}
	for _, track := range tracks {
		id := text(track["id"])
		if id == "" || ids[id] || !timelineKind(text(track["kind"])) {
			return BadAuthRequest("时间线轨道无效或重复")
		}
		ids[id] = true
	}
	clipIDs := map[string]bool{}
	end := 0.0
	for _, clip := range clips {
		id := text(clip["id"])
		start, okStart := clip["startMs"].(float64)
		duration, okDuration := clip["durationMs"].(float64)
		if id == "" || clipIDs[id] || !ids[text(clip["trackId"])] || !timelineKind(text(clip["kind"])) || !okStart || !okDuration || start < 0 || duration <= 0 || math.IsNaN(start+duration) || math.IsInf(start+duration, 0) || start+duration > 1e12 {
			return BadAuthRequest("时间线片段或轨道引用无效")
		}
		clipIDs[id] = true
		media, _ := clip["directMedia"].(map[string]any)
		if media == nil {
			if entity(doc, "node:"+text(clip["nodeId"])) == nil {
				return BadAuthRequest("时间线片段引用不存在的节点")
			}
		} else {
			if text(media["id"]) == "" || !timelineKind(text(media["kind"])) || text(media["kind"]) == "subtitle" {
				return BadAuthRequest("时间线直连素材无效")
			}
			if text(media["kind"]) != "text" && assets.ResourceID(text(media["storageKey"])) == "" && assets.ResourceID(text(media["url"])) == "" && assets.ResourceID(text(media["dataUrl"])) == "" {
				return BadAuthRequest("时间线直连媒体必须先保存为本地素材")
			}
		}
		end = math.Max(end, start+duration)
	}
	timeline["durationMs"] = end
	return nil
}

// Node deletion removes only canvas-backed clips; direct timeline media remains.
func cleanTimelineNodeReferences(doc Document) {
	timeline, _ := doc["timeline"].(map[string]any)
	if timeline == nil {
		return
	}
	clips := []map[string]any{}
	for _, clip := range maps(timeline["clips"]) {
		if clip["directMedia"] != nil || entity(doc, "node:"+text(clip["nodeId"])) != nil {
			clips = append(clips, clip)
		}
	}
	timeline["clips"] = pMapsAsAny(clips)
	_ = validateTimeline(doc)
}
func pMapsAsAny(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}
