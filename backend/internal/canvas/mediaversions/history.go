package mediaversions

import (
	"crypto/rand"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 本文件是 web/src/lib/canvas/canvas-media-versions.ts 的 Go 等价实现：节点 metadata.mediaVersions
// 是唯一的版本历史。testdata/parity.json 由 Go 与 TS 两侧测试共同读取，行为差异会让两边之一失败。

// Limit 与前端 MEDIA_VERSION_LIMIT 一致。
const Limit = 30

// MediaKeys 与前端 MEDIA_KEYS 一致：版本快照与采用时替换的媒体字段。
var MediaKeys = []string{"content", "storageKey", "previewContent", "mimeType", "bytes", "durationMs", "hasAudio", "waveform", "waveformVersion", "naturalWidth", "naturalHeight", "videoPreview", "imageRotation", "assetId"}

// GenerationKeys 与前端 GENERATION_KEYS 一致。
var GenerationKeys = []string{"prompt", "model", "producedModel", "size", "quality", "seconds", "references", "taskId", "taskDurationMs"}

const inlineDataURLLimit = 2048

// newVersionID 与 clock 只在测试中替换；版本 ID 是不透明字符串，迁移来的版本保留旧表行 ID。
var (
	newVersionID = func() string { return "mv" + rand.Text() }
	clock        = time.Now
)

var trailingVersionLabel = regexp.MustCompile(` · V\d+$`)

func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func cloneMap(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0 && !math.IsNaN(v)
	case int:
		return v != 0
	case int64:
		return v != 0
	case json.Number:
		f, _ := v.Float64()
		return f != 0
	}
	return true
}

func number(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func present(value any) bool { return value != nil && value != "" }

func meta(node map[string]any) map[string]any {
	m, _ := node["metadata"].(map[string]any)
	return m
}

// IsVersionNode 对应 isMediaVersionNode：批次、图层节点不维护版本历史。
func IsVersionNode(node map[string]any) bool {
	if node == nil || !Supported(str(node["type"])) {
		return false
	}
	m := meta(node)
	return !truthy(m["isBatchRoot"]) && !truthy(m["batchRootId"]) && !truthy(m["imageLayerGroup"]) && !truthy(m["imageLayer"]) && !truthy(m["layerExtraction"])
}

func hasMediaFields(m map[string]any) bool { return truthy(m["storageKey"]) || truthy(m["content"]) }

// HasMedia 报告节点当前是否显示媒体。
func HasMedia(node map[string]any) bool { return hasMediaFields(meta(node)) }

func mediaIdentity(m map[string]any) string {
	if key := str(m["storageKey"]); key != "" {
		return key
	}
	return str(m["content"])
}

func snapshotMedia(m map[string]any) map[string]any {
	media := map[string]any{}
	for _, key := range MediaKeys {
		if value := m[key]; present(value) {
			media[key] = value
		}
	}
	if present(media["storageKey"]) {
		for _, key := range []string{"content", "previewContent"} {
			if value := str(media[key]); strings.HasPrefix(value, "data:") && len([]rune(value)) > inlineDataURLLimit {
				delete(media, key)
			}
		}
	}
	return media
}

func snapshotGeneration(m map[string]any) map[string]any {
	generation := map[string]any{}
	for _, key := range GenerationKeys {
		if value := m[key]; present(value) {
			generation[key] = value
		}
	}
	if len(generation) == 0 {
		return nil
	}
	return generation
}

func inferSource(m map[string]any) string {
	if truthy(m["taskId"]) || truthy(m["generationType"]) || truthy(m["producedModel"]) || truthy(m["taskDurationMs"]) {
		return "generate"
	}
	return "upload"
}

// List 返回节点的版本条目（原始顺序）。非对象条目被忽略。
func List(node map[string]any) []map[string]any {
	out := []map[string]any{}
	values, _ := meta(node)["mediaVersions"].([]any)
	for _, value := range values {
		if version, ok := value.(map[string]any); ok {
			out = append(out, version)
		}
	}
	return out
}

func versionMedia(version map[string]any) map[string]any {
	media, _ := version["media"].(map[string]any)
	return media
}

func versionGeneration(version map[string]any) map[string]any {
	generation, _ := version["generation"].(map[string]any)
	return generation
}

// Current 对应 currentMediaVersion：当前显示的媒体对应的版本；媒体被其他路径改写后不再认定旧指针。
func Current(node map[string]any) map[string]any {
	identity := mediaIdentity(meta(node))
	if identity == "" {
		return nil
	}
	versions := List(node)
	pointer := str(meta(node)["currentMediaVersionId"])
	for _, version := range versions {
		if str(version["id"]) == pointer && mediaIdentity(versionMedia(version)) == identity {
			return version
		}
	}
	for _, version := range versions {
		if mediaIdentity(versionMedia(version)) == identity {
			return version
		}
	}
	return nil
}

// Find 返回指定 ID 的版本。
func Find(node map[string]any, id string) map[string]any {
	for _, version := range List(node) {
		if str(version["id"]) == id {
			return version
		}
	}
	return nil
}

// ForTask 返回 generation.taskId 指向该任务的最新版本。
func ForTask(node map[string]any, taskID string) map[string]any {
	var found map[string]any
	for _, version := range List(node) {
		if taskID != "" && str(versionGeneration(version)["taskId"]) == taskID && (found == nil || number(version["number"]) > number(found["number"])) {
			found = version
		}
	}
	return found
}

func toAny(versions []map[string]any) []any {
	out := make([]any, len(versions))
	for i, version := range versions {
		out[i] = version
	}
	return out
}

func trimVersions(versions []map[string]any, keep ...string) []map[string]any {
	if len(versions) <= Limit {
		return versions
	}
	kept := map[string]bool{}
	for _, id := range keep {
		kept[id] = true
	}
	sorted := append([]map[string]any(nil), versions...)
	sort.SliceStable(sorted, func(i, j int) bool { return number(sorted[i]["number"]) < number(sorted[j]["number"]) })
	removed := map[string]bool{}
	for _, version := range sorted {
		if len(removed) == len(versions)-Limit {
			break
		}
		if !kept[str(version["id"])] {
			removed[str(version["id"])] = true
		}
	}
	out := []map[string]any{}
	for _, version := range versions {
		if !removed[str(version["id"])] {
			out = append(out, version)
		}
	}
	return out
}

func nextNumber(versions []map[string]any) float64 {
	highest := 0.0
	for _, version := range versions {
		highest = math.Max(highest, number(version["number"]))
	}
	return highest + 1
}

func newVersion(m map[string]any, number float64, source string, fallback time.Time) map[string]any {
	createdAt := str(m["taskCompletedAt"])
	if createdAt == "" {
		createdAt = isoTime(fallback)
	}
	if source == "" {
		source = inferSource(m)
	}
	version := map[string]any{"id": newVersionID(), "number": number, "createdAt": createdAt, "source": source, "media": snapshotMedia(m)}
	if generation := snapshotGeneration(m); generation != nil {
		version["generation"] = generation
	}
	return version
}

// ensure 对应 ensureCurrentMediaVersion，直接修改传入的（已复制的）节点。
func ensure(node map[string]any, source string, fallback time.Time) {
	m := meta(node)
	if !IsVersionNode(node) || !hasMediaFields(m) {
		return
	}
	if existing := Current(node); existing != nil {
		m["currentMediaVersionId"] = existing["id"]
		return
	}
	versions := List(node)
	version := newVersion(m, nextNumber(versions), source, fallback)
	m["mediaVersions"] = toAny(trimVersions(append(versions, version), str(version["id"])))
	m["currentMediaVersionId"] = version["id"]
}

func setOrDelete(m map[string]any, key string, value any) {
	if value == nil {
		delete(m, key)
		return
	}
	m[key] = value
}

// RecordReplaced 对应 recordReplacedMediaVersion：旧媒体先入历史，新媒体作为当前版本；
// 媒体类型变化时历史不再有意义。fallback 是缺少 taskCompletedAt 时的创建时间。
func RecordReplaced(previous, next map[string]any, source string, fallback time.Time) map[string]any {
	out := cloneMap(next)
	if out["metadata"] == nil {
		out["metadata"] = map[string]any{}
	}
	m := meta(out)
	if previous == nil || str(previous["type"]) != str(next["type"]) {
		delete(m, "mediaVersions")
		delete(m, "currentMediaVersionId")
		ensure(out, source, fallback)
		return out
	}
	if IsVersionNode(previous) {
		recorded := cloneMap(previous)
		if recorded["metadata"] == nil {
			recorded["metadata"] = map[string]any{}
		}
		ensure(recorded, "", fallback)
		setOrDelete(m, "mediaVersions", meta(recorded)["mediaVersions"])
		setOrDelete(m, "currentMediaVersionId", meta(recorded)["currentMediaVersionId"])
	}
	ensure(out, source, fallback)
	return out
}

// RecordGenerated 是任务回写的入口：把生成结果的媒体与来源字段写入节点副本，按
// recordReplacedMediaVersion 登记并采用新版本。
func RecordGenerated(node, media, generation map[string]any, now time.Time) map[string]any {
	return RecordReplaced(node, withResult(node, media, generation), "generate", now)
}

func withResult(node, media, generation map[string]any) map[string]any {
	next := cloneMap(node)
	if next["metadata"] == nil {
		next["metadata"] = map[string]any{}
	}
	m := meta(next)
	for _, key := range MediaKeys {
		delete(m, key)
	}
	for key, value := range cloneMap(media) {
		m[key] = value
	}
	for key, value := range cloneMap(generation) {
		m[key] = value
	}
	return next
}

// AppendGenerated 用于生成期间用户手动换过当前版本的情况：新结果只进入历史，
// 节点媒体与当前版本保持用户的选择。上游没有对应函数（浏览器生成不存在该竞态）。
func AppendGenerated(node, media, generation map[string]any, now time.Time) (map[string]any, string) {
	out := cloneMap(node)
	if out["metadata"] == nil {
		out["metadata"] = map[string]any{}
	}
	ensure(out, "", now)
	m := meta(out)
	versions := List(out)
	version := newVersion(meta(withResult(node, media, generation)), nextNumber(versions), "generate", now)
	m["mediaVersions"] = toAny(trimVersions(append(versions, version), str(version["id"]), str(m["currentMediaVersionId"])))
	return out, str(version["id"])
}

// AdoptBlock 对应 mediaVersionAdoptBlock，返回 Agent 合同的失败原因。
func AdoptBlock(node map[string]any) string {
	if node == nil {
		return "media_version_missing"
	}
	m := meta(node)
	if truthy(m["locked"]) {
		return "media_version_locked"
	}
	if m["status"] == "loading" || m["fileUpload"] == "uploading" {
		return "media_version_loading"
	}
	return ""
}

var taskMetadataKeys = []string{"taskId", "taskClientOperationId", "retryOf", "attemptGroupId", "taskStatus", "taskProgress", "taskStage", "taskMediaStage", "taskCanRecoverMedia", "taskProvider", "taskStartedAt", "taskCompletedAt", "taskDurationMs", "taskErrorCode", "taskOfficialStatus", "taskReceiptRecorded", "taskCreatedAt", "taskUpdatedAt", "generationOutputCount"}

// resetTaskMetadata 对应 resetGenerationTaskMetadata：解除旧任务绑定，避免任务同步把节点重新标成失败或生成中。
func resetTaskMetadata(m map[string]any, status string) map[string]any {
	out := cloneMap(m)
	out["status"] = status
	for _, key := range append([]string{"errorDetails", "generationErrorCode", "resourceReloadAvailable", "failedPromptFingerprint"}, taskMetadataKeys...) {
		delete(out, key)
	}
	return out
}

func jsRound(value float64) float64 { return math.Floor(value + 0.5) }

func adoptedGeometry(node, m, media map[string]any) (any, any) {
	keep := str(node["type"]) == "audio" || truthy(m["manualSize"]) || truthy(m["locked"]) || truthy(m["freeResize"])
	width, height := number(media["naturalWidth"]), number(media["naturalHeight"])
	if keep || width == 0 || height == 0 {
		return node["width"], node["height"]
	}
	longest := math.Max(number(node["width"]), number(node["height"]))
	if width >= height {
		return longest, jsRound(longest * height / width)
	}
	return jsRound(longest * width / height), longest
}

func applyVersionMedia(m, version map[string]any) {
	media := versionMedia(version)
	for _, key := range MediaKeys {
		setOrDelete(m, key, media[key])
	}
}

// IsCurrent 报告 versionID 是否就是节点当前显示的版本；Agent 采用当前版本是成功的空操作。
func IsCurrent(node map[string]any, versionID string) bool {
	current := Current(node)
	return current != nil && str(current["id"]) == versionID
}

// Adopt 先按 mediaVersionAdoptBlock 拦截，再执行 adoptMediaVersion：只替换媒体与来源信息，
// 提示词和下一次生成参数保持不变。
func Adopt(node map[string]any, versionID string) (map[string]any, error) {
	if reason := AdoptBlock(node); reason != "" {
		return nil, fail(reason, adoptMessages[reason])
	}
	version := Find(node, versionID)
	if version == nil || !hasMediaFields(versionMedia(version)) {
		return nil, fail("media_version_missing", adoptMessages["media_version_missing"])
	}
	ensured := cloneMap(node)
	if ensured["metadata"] == nil {
		ensured["metadata"] = map[string]any{}
	}
	ensure(ensured, "", clock())
	m := resetTaskMetadata(meta(ensured), "success")
	applyVersionMedia(m, version)
	generation := versionGeneration(version)
	setOrDelete(m, "producedModel", generation["producedModel"])
	setOrDelete(m, "taskDurationMs", generation["taskDurationMs"])
	m["currentMediaVersionId"] = versionID
	out := cloneMap(node)
	width, height := adoptedGeometry(node, meta(node), versionMedia(version))
	setOrDelete(out, "width", width)
	setOrDelete(out, "height", height)
	out["metadata"] = m
	return out, nil
}

var adoptMessages = map[string]string{
	"media_version_locked":  "节点已锁定，不能切换媒体版本",
	"media_version_loading": "节点正在生成或上传，完成后再切换版本",
	"media_version_missing": "媒体版本不存在或没有可用媒体，请重新读取 media_versions",
}

// Extract 对应 createNodeFromMediaVersion：把版本取出为独立节点，放在原节点右侧，
// 不携带版本历史和任务绑定。
func Extract(node map[string]any, versionID, id string) (map[string]any, error) {
	version := Find(node, versionID)
	if node == nil || version == nil || !hasMediaFields(versionMedia(version)) {
		return nil, fail("media_version_missing", adoptMessages["media_version_missing"])
	}
	m := resetTaskMetadata(meta(node), "success")
	applyVersionMedia(m, version)
	for _, key := range []string{"mediaVersions", "currentMediaVersionId", "versionOfNodeId", "versionLabel", "versionPrimary", "locked", "assetId"} {
		delete(m, key)
	}
	generation := versionGeneration(version)
	if prompt, ok := generation["prompt"]; ok && prompt != nil {
		m["prompt"], m["composerContent"] = prompt, prompt
	}
	setOrDelete(m, "producedModel", generation["producedModel"])
	setOrDelete(m, "taskDurationMs", generation["taskDurationMs"])
	m["copiedFromNodeId"] = node["id"]
	m["generationResultPlacement"] = "replace-node"
	out := cloneMap(node)
	free := cloneMap(meta(node))
	free["manualSize"], free["locked"], free["freeResize"] = false, false, false
	width, height := adoptedGeometry(node, free, versionMedia(version))
	out["id"] = id
	delete(out, "parentId")
	out["title"] = trailingVersionLabel.ReplaceAllString(str(node["title"]), "") + " · V" + strconv.FormatFloat(number(version["number"]), 'f', -1, 64)
	position, _ := node["position"].(map[string]any)
	out["position"] = map[string]any{"x": number(position["x"]) + number(node["width"]) + 48, "y": position["y"]}
	setOrDelete(out, "width", width)
	setOrDelete(out, "height", height)
	out["metadata"] = m
	return out, nil
}
