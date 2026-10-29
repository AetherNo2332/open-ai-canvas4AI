package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// mediaVersionsMigrationVersion 把 fork 的 canvas_media_versions 就绪行搬进节点
// metadata.mediaVersions（上游版本历史格式）。旧表保留，迁移后只读。
const mediaVersionsMigrationVersion int64 = 77

const mediaVersionsChecksum = "sha256:media-versions-to-metadata-v77-20261029"

// mediaVersionLimit 与前端 MEDIA_VERSION_LIMIT 一致。
const mediaVersionLimit = 30

// 与 web/src/lib/canvas/canvas-media-versions.ts 的 MEDIA_KEYS 一致。
var mediaVersionMediaKeys = []string{"content", "storageKey", "previewContent", "mimeType", "bytes", "durationMs", "hasAudio", "waveform", "waveformVersion", "naturalWidth", "naturalHeight", "videoPreview", "imageRotation", "assetId"}

// CanvasWriteProjector 在迁移事务里把一次画布写入投影为 Agent 命令事件与实体版本。
// database 不能 import repository（repository 依赖本包），由 repository 在 init 中注册。
type CanvasWriteProjector func(tx *gorm.DB, before, after *model.CanvasProject) error

var (
	canvasWriteProjectorMu sync.RWMutex
	canvasWriteProjector   CanvasWriteProjector
)

// RegisterCanvasWriteProjector 设置投影函数并返回之前的值；传 nil 用于测试取消注册。
func RegisterCanvasWriteProjector(fn CanvasWriteProjector) CanvasWriteProjector {
	canvasWriteProjectorMu.Lock()
	defer canvasWriteProjectorMu.Unlock()
	previous := canvasWriteProjector
	canvasWriteProjector = fn
	return previous
}

func registeredCanvasWriteProjector() CanvasWriteProjector {
	canvasWriteProjectorMu.RLock()
	defer canvasWriteProjectorMu.RUnlock()
	return canvasWriteProjector
}

func migrateMediaVersionsToMetadata(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&model.CanvasMediaVersion{}) {
		return nil
	}
	var canvasIDs []string
	if err := tx.Model(&model.CanvasMediaVersion{}).Where("status = ?", "ready").Distinct("canvas_id").Order("canvas_id").Pluck("canvas_id", &canvasIDs).Error; err != nil {
		return fmt.Errorf("读取媒体版本画布：%w", err)
	}
	for _, canvasID := range canvasIDs {
		if err := migrateCanvasMediaVersions(tx, canvasID); err != nil {
			return fmt.Errorf("迁移画布 %s 的媒体版本：%w", canvasID, err)
		}
	}
	return nil
}

func migrateCanvasMediaVersions(tx *gorm.DB, canvasID string) error {
	var project model.CanvasProject
	if err := tx.First(&project, "id = ?", canvasID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	// 与 mediaversions.ScopeForNode 同一规则：行只属于当前纪元里未删除、generation 相同的节点化身。
	var epoch model.CanvasDocumentEpoch
	if err := tx.First(&epoch, "canvas_id = ? AND owner_id = ?", canvasID, project.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if epoch.Deleted {
		return nil
	}
	var entities []model.CanvasEntityVersion
	if err := tx.Where("canvas_id = ? AND epoch = ? AND entity_key LIKE ?", canvasID, epoch.Epoch, "node:%").Find(&entities).Error; err != nil {
		return err
	}
	generations := map[string]int64{}
	for _, entity := range entities {
		if !entity.Deleted {
			generations[strings.TrimPrefix(entity.EntityKey, "node:")] = entity.Generation
		}
	}
	var rows []model.CanvasMediaVersion
	if err := tx.Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND status = ?", project.UserID, canvasID, epoch.Epoch, "ready").Order("node_id, sequence").Find(&rows).Error; err != nil {
		return err
	}
	byNode := map[string][]model.CanvasMediaVersion{}
	for _, row := range rows {
		if generation, ok := generations[row.NodeID]; ok && generation == row.Generation {
			byNode[row.NodeID] = append(byNode[row.NodeID], row)
		}
	}
	if len(byNode) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(project.PayloadJSON)))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return fmt.Errorf("解析画布内容：%w", err)
	}
	nodes, _ := doc["nodes"].([]any)
	changed := false
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := node["id"].(string)
		mode, _ := node["type"].(string)
		if len(byNode[id]) == 0 || (mode != "image" && mode != "video" && mode != "audio") {
			continue
		}
		next, err := mergeMediaVersions(node, byNode[id])
		if err != nil {
			return err
		}
		changed = changed || next
	}
	if !changed {
		return nil
	}
	projector := registeredCanvasWriteProjector()
	if projector == nil {
		return errors.New("未注册画布写入投影，拒绝绕过 Agent 命令投影改写画布")
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	before := project
	after := project
	after.PayloadJSON = string(encoded)
	after.Revision = project.Revision + 1
	after.UpdatedAt = time.Now()
	result := tx.Model(&model.CanvasProject{}).Where("id = ? AND user_id = ? AND revision = ?", project.ID, project.UserID, project.Revision).
		Updates(map[string]any{"payload_json": after.PayloadJSON, "revision": after.Revision, "updated_at": after.UpdatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("画布 revision 已变化")
	}
	return projector(tx, &before, &after)
}

// mergeMediaVersions 把就绪行追加到节点已有版本历史，按资源 ID 去重，保留最新 30 条且当前版本必留。
func mergeMediaVersions(node map[string]any, rows []model.CanvasMediaVersion) (bool, error) {
	meta, _ := node["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		node["metadata"] = meta
	}
	original, _ := json.Marshal(meta)
	existing, _ := meta["mediaVersions"].([]any)
	versions := append([]any(nil), existing...)
	rowIDs := map[string]bool{}
	for _, row := range rows {
		rowIDs[row.ID] = true
	}
	ids := map[string]bool{}
	byResource := map[string]string{}
	// 编号基数只看不是由这些行迁出的版本，重复执行时迁移版本拿到相同编号，裁剪结果也相同。
	base := 0.0
	for _, raw := range versions {
		version, _ := raw.(map[string]any)
		id, _ := version["id"].(string)
		ids[id] = true
		media, _ := version["media"].(map[string]any)
		if resource := resourceOf(media); resource != "" && byResource[resource] == "" {
			byResource[resource] = id
		}
		if !rowIDs[id] {
			base = max(base, numberOf(version["number"]))
		}
	}
	alias := map[string]string{}
	rank := 0
	for _, row := range rows {
		if ids[row.ID] {
			rank++
			continue
		}
		version, err := migratedMediaVersion(row)
		if err != nil {
			return false, fmt.Errorf("媒体版本 %s：%w", row.ID, err)
		}
		resource := resourceOf(version["media"].(map[string]any))
		if resource != "" && byResource[resource] != "" {
			alias[row.ID] = byResource[resource]
			continue
		}
		rank++
		version["number"] = base + float64(rank)
		versions = append(versions, version)
		ids[row.ID] = true
		if resource != "" {
			byResource[resource] = row.ID
		}
	}
	current, _ := meta["currentMediaVersionId"].(string)
	if current == "" {
		state, _ := meta["mediaVersion"].(map[string]any)
		pointer, _ := state["currentVersionId"].(string)
		if target := alias[pointer]; target != "" {
			pointer = target
		}
		if ids[pointer] {
			current = pointer
			meta["currentMediaVersionId"] = current
		}
	}
	sort.SliceStable(versions, func(i, j int) bool {
		return numberOf(versions[i].(map[string]any)["number"]) < numberOf(versions[j].(map[string]any)["number"])
	})
	for len(versions) > mediaVersionLimit {
		for index, raw := range versions {
			if id, _ := raw.(map[string]any)["id"].(string); id != current {
				versions = append(versions[:index], versions[index+1:]...)
				break
			}
		}
	}
	if len(versions) > 0 {
		meta["mediaVersions"] = versions
	}
	next, _ := json.Marshal(meta)
	return !bytes.Equal(original, next), nil
}

func migratedMediaVersion(row model.CanvasMediaVersion) (map[string]any, error) {
	var stored map[string]any
	if strings.TrimSpace(row.MediaJSON) != "" {
		if err := json.Unmarshal([]byte(row.MediaJSON), &stored); err != nil {
			return nil, err
		}
	}
	media := map[string]any{}
	for _, key := range mediaVersionMediaKeys {
		if value, ok := stored[key]; ok && value != nil && value != "" {
			media[key] = value
		}
	}
	if media["storageKey"] == nil && row.ResourceID != "" {
		media["storageKey"] = "resource:" + row.ResourceID
	}
	var request struct {
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
		Input  struct {
			Config map[string]any `json:"config"`
		} `json:"input"`
	}
	if strings.TrimSpace(row.RequestJSON) != "" {
		if err := json.Unmarshal([]byte(row.RequestJSON), &request); err != nil {
			return nil, err
		}
	}
	generation := map[string]any{}
	put := func(key string, value any) {
		if value != nil && value != "" {
			generation[key] = value
		}
	}
	put("prompt", request.Prompt)
	put("model", request.Model)
	put("producedModel", stored["producedModel"])
	put("size", stringValue(request.Input.Config["size"]))
	put("quality", stringValue(request.Input.Config["quality"]))
	seconds := request.Input.Config["videoSeconds"]
	if seconds == nil {
		seconds = request.Input.Config["seconds"]
	}
	put("seconds", stringValue(seconds))
	if row.TaskID != nil {
		put("taskId", *row.TaskID)
	} else {
		put("taskId", row.SourceTaskID)
	}
	put("taskDurationMs", stored["taskDurationMs"])
	version := map[string]any{"id": row.ID, "createdAt": row.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"), "source": "generate", "media": media}
	if len(generation) > 0 {
		version["generation"] = generation
	}
	return version, nil
}

func resourceOf(media map[string]any) string {
	key, _ := media["storageKey"].(string)
	if strings.HasPrefix(key, "resource:") {
		return strings.TrimPrefix(key, "resource:")
	}
	return ""
}

func numberOf(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

// stringValue 把配置里的字符串或数字转成上游 generation 使用的字符串；其他类型丢弃。
func stringValue(value any) any {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return nil
}
