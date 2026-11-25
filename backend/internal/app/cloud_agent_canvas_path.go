package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/document"
)

type canvasCommandFS struct {
	canvasID string
	revision int64
	doc      map[string]any
	nodes    []canvasCommandNode
	byID     map[string]int
	byPath   map[string]int
}

type canvasCommandNode struct {
	ID         string
	Node       map[string]any
	Type       string
	ScopeID    string
	LocalID    string
	Path       string
	Descriptor capability.Descriptor
	Known      bool
}

func newCanvasCommandFS(canvasID string, doc map[string]any, revision int64) *canvasCommandFS {
	fs := &canvasCommandFS{canvasID: canvasID, revision: revision, doc: doc, byID: map[string]int{}, byPath: map[string]int{}}
	for _, node := range creationMaps(doc["nodes"]) {
		id := strings.TrimSpace(stringValue(node["id"]))
		if id == "" {
			continue
		}
		entry := canvasCommandNode{ID: id, Node: node, Type: strings.TrimSpace(stringValue(node["type"]))}
		entry.ScopeID = strings.TrimSpace(stringValue(node["scopeId"]))
		descriptor, known := cloudAgentNodeCapabilityForNode(node)
		if known {
			entry.Descriptor = descriptor
			entry.Type = descriptor.Type
			entry.Known = true
			if descriptor.VisualOnly {
				continue
			}
		}
		if entry.ScopeID != "" && strings.HasPrefix(id, entry.ScopeID+"__") {
			entry.LocalID = strings.TrimPrefix(id, entry.ScopeID+"__")
			entry.Path = "./" + entry.ScopeID + "/" + entry.LocalID + "/"
		} else {
			entry.Path = "./" + id + "/"
		}
		key := canvasCommandPathKey(entry.Path)
		if _, duplicate := fs.byPath[key]; duplicate {
			continue
		}
		fs.byPath[key] = len(fs.nodes)
		fs.byID[id] = len(fs.nodes)
		fs.nodes = append(fs.nodes, entry)
	}
	return fs
}

func canvasCommandPathKey(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	for _, prefix := range []string{"./", "/"} {
		for strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
		}
	}
	for strings.Contains(path, "//") {
		path = strings.Replace(path, "//", "/", -1)
	}
	return strings.TrimSuffix(path, "/")
}

func (fs *canvasCommandFS) lookupNode(path string) (canvasCommandNode, bool) {
	key := canvasCommandPathKey(path)
	if index, ok := fs.byPath[key]; ok {
		return fs.nodes[index], true
	}
	for _, entry := range fs.nodes {
		title := strings.TrimSpace(stringValue(entry.Node["title"]))
		if title != "" && strings.EqualFold(title, strings.TrimSpace(path)) {
			return entry, true
		}
	}
	return canvasCommandNode{}, false
}

func (fs *canvasCommandFS) lookupNodeID(id string) (canvasCommandNode, bool) {
	index, ok := fs.byID[id]
	if !ok {
		return canvasCommandNode{}, false
	}
	return fs.nodes[index], true
}

func (fs *canvasCommandFS) children(submapID string) []canvasCommandNode {
	children := make([]canvasCommandNode, 0, 4)
	for _, entry := range fs.nodes {
		if entry.ScopeID == submapID {
			children = append(children, entry)
		}
	}
	return children
}

func (fs *canvasCommandFS) topLevel() []canvasCommandNode {
	nodes := make([]canvasCommandNode, 0, len(fs.nodes))
	for _, entry := range fs.nodes {
		if entry.ScopeID == "" {
			nodes = append(nodes, entry)
		}
	}
	return nodes
}

// resolveFieldPath 解析字段文件。返回节点、字段名与是否命中。
func (fs *canvasCommandFS) resolveFieldPath(path string) (canvasCommandNode, string, error) {
	key := canvasCommandPathKey(path)
	segments := strings.Split(key, "/")
	if len(segments) == 2 {
		if entry, ok := fs.lookupNode(segments[0]); ok {
			if field, ok := canvasCommandFieldByName(entry, segments[1]); ok {
				return entry, field, nil
			}
			if canvasCommandLooksLikeFieldFile(segments[1]) {
				return entry, "", canvasCommandFieldNotFound(entry, segments[1])
			}
		}
		if entry, ok := fs.lookupNode(key); ok {
			return entry, "", canvasCommandDirectoryError(entry)
		}
	}
	if len(segments) == 3 {
		if parent, ok := fs.lookupNodeID(segments[0]); ok && parent.Type == "submap" {
			for _, child := range fs.children(parent.ID) {
				if child.LocalID == segments[1] {
					if field, ok := canvasCommandFieldByName(child, segments[2]); ok {
						return child, field, nil
					}
					return child, "", canvasCommandFieldNotFound(child, segments[2])
				}
			}
		}
	}
	if len(segments) == 1 {
		if entry, ok := fs.lookupNode(key); ok {
			return entry, "", canvasCommandDirectoryError(entry)
		}
	}
	return canvasCommandNode{}, "", canvasCommandError("command", "not_found", canvasCommandPathMissing(fs, path))
}

func canvasCommandLooksLikeFieldFile(name string) bool {
	for _, ext := range []string{".txt", ".md", ".json", ".list", ".num", ".bool"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return false
}

func canvasCommandDirectoryError(entry canvasCommandNode) error {
	names := canvasCommandFieldFileNames(entry)
	listing := "（没有字段文件）"
	if len(names) > 0 {
		listing = strings.Join(names, "、")
	}
	return canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 是节点目录，不是文件。可用字段文件：%s。例如：cat %s%s", entry.Path, listing, entry.Path, firstOr(names, "title.txt")))
}

func canvasCommandFieldNotFound(entry canvasCommandNode, name string) error {
	names := canvasCommandFieldFileNames(entry)
	return canvasCommandError("command", "not_found", fmt.Sprintf("%s 下没有字段文件 %s；可用：%s", entry.Path, name, strings.Join(names, "、")))
}

func canvasCommandPathMissing(fs *canvasCommandFS, path string) string {
	entries := make([]string, 0, len(fs.nodes))
	for _, entry := range fs.nodes {
		if entry.ScopeID == "" {
			entries = append(entries, entry.Path)
		}
	}
	listing := "（画布上没有节点）"
	if len(entries) > 0 {
		listing = strings.Join(entries, "、")
	}
	return fmt.Sprintf("路径 %s 不在当前画布；可用节点目录：%s。节点是目录（./<节点ID>/），字段是目录里的文件（./<节点ID>/<字段>.<ext>）；submap 子节点写作 ./<submapID>/<localId>/<字段>.<ext>。先运行 canvas ls 或 canvas find <pattern> 取真实路径", path, listing)
}

func firstOr(values []string, fallback string) string {
	if len(values) > 0 {
		return values[0]
	}
	return fallback
}

// canvasCommandField 是字段文件声明，兼容新 Fields 与旧 PatchFields。
type canvasCommandField struct {
	Key       string
	Kind      string
	Extension string
	Label     string
	Writable  bool
	Reference bool
	Field     *capability.Field
	Patch     *capability.PatchField
}

func canvasCommandFields(entry canvasCommandNode) []canvasCommandField {
	if !entry.Known {
		return nil
	}
	fields := make([]canvasCommandField, 0, len(entry.Descriptor.Fields)+len(entry.Descriptor.PatchFields))
	seen := map[string]bool{}
	for _, key := range entry.Descriptor.Fields.Ordered() {
		field := entry.Descriptor.Fields[key]
		copied := field
		fields = append(fields, canvasCommandField{
			Key: key, Kind: canvasCommandFieldKind(field.Kind), Extension: canvasCommandFieldKindExtension(field.Kind), Label: field.Label,
			Writable: field.Writable || field.Editable, Reference: field.Reference, Field: &copied,
		})
		seen[key] = true
	}
	keys := make([]string, 0, len(entry.Descriptor.PatchFields))
	for key := range entry.Descriptor.PatchFields {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := entry.Descriptor.PatchFields[keys[i]], entry.Descriptor.PatchFields[keys[j]]
		if left.Order != right.Order {
			return left.Order < right.Order
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		if seen[key] {
			continue
		}
		patch := entry.Descriptor.PatchFields[key]
		copied := patch
		fields = append(fields, canvasCommandField{
			Key: key, Kind: canvasCommandPatchKind(patch.Kind), Extension: canvasCommandPatchExtension(patch), Label: patch.Label,
			Writable: entry.Descriptor.CanUpdate, Patch: &copied,
		})
	}
	return fields
}

func canvasCommandFieldKindExtension(kind string) string {
	switch kind {
	case capability.KindStrings:
		return ".list"
	case capability.KindMap, capability.KindCollection:
		return ".json"
	case capability.KindNumber:
		return ".num"
	case capability.KindBoolean:
		return ".bool"
	default:
		return ".txt"
	}
}

func canvasCommandPatchExtension(field capability.PatchField) string {
	switch field.Kind {
	case "array":
		if field.Items != nil && field.Items.Kind == "string" {
			return ".list"
		}
		return ".json"
	case "number":
		return ".num"
	case "boolean":
		return ".bool"
	default:
		return ".txt"
	}
}

func canvasCommandFieldKind(kind string) string {
	switch kind {
	case capability.KindString:
		return "string"
	case capability.KindNumber:
		return "number"
	case capability.KindBoolean:
		return "boolean"
	case capability.KindStrings, capability.KindCollection:
		return "array"
	case capability.KindMap:
		return "object"
	default:
		return "string"
	}
}

func canvasCommandPatchKind(kind string) string {
	switch kind {
	case "string":
		return "string"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		return "array"
	case "object":
		return "object"
	default:
		return "string"
	}
}

func canvasCommandFieldByName(entry canvasCommandNode, name string) (string, bool) {
	key := strings.TrimSpace(name)
	for _, ext := range []string{".txt", ".md", ".json", ".list", ".num", ".bool"} {
		if strings.HasSuffix(strings.ToLower(key), ext) {
			key = key[:len(key)-len(ext)]
			break
		}
	}
	// 供体把媒体提示词文件命名为 prompt.txt；4AI 的媒体提示词草稿落在
	// metadata.composerContent。媒体节点还有一个只读的已提交 prompt 字段，
	// 所以别名必须先于同名旧字段匹配，读写都落到草稿字段。
	if key == "prompt" && canvasCommandIsMediaNode(entry) {
		for _, field := range canvasCommandFields(entry) {
			if field.Key == "content" {
				return "content", true
			}
		}
	}
	for _, field := range canvasCommandFields(entry) {
		if field.Key == key || strings.EqualFold(field.Label, key) {
			return field.Key, true
		}
	}
	return "", false
}

func canvasCommandFieldExtension(field canvasCommandField) string {
	if field.Extension != "" {
		return field.Extension
	}
	return ".txt"
}

func canvasCommandIsMediaNode(entry canvasCommandNode) bool {
	switch entry.Type {
	case "image", "video", "audio":
		return true
	default:
		return false
	}
}

func canvasCommandFieldFileNames(entry canvasCommandNode) []string {
	fields := canvasCommandFields(entry)
	names := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		name := field.Key + canvasCommandFieldExtension(field)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if canvasCommandIsMediaNode(entry) && seen["content.txt"] && !seen["prompt.txt"] {
		names = append(names, "prompt.txt")
	}
	return names
}

func canvasCommandFieldValue(entry canvasCommandNode, key string) (any, bool) {
	field, ok := canvasCommandFieldByName(entry, key)
	if !ok {
		return nil, false
	}
	for _, declaration := range canvasCommandFields(entry) {
		if declaration.Key != field {
			continue
		}
		if declaration.Field != nil {
			return declaration.Field.Value(entry.Node)
		}
		if declaration.Patch != nil {
			return canvasCommandReadDotted(entry.Node, declaration.Patch.Path)
		}
	}
	return nil, false
}

func canvasCommandReadDotted(node map[string]any, path string) (any, bool) {
	var current any = node
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func canvasCommandValueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}

func canvasCommandRenderNode(entry canvasCommandNode) (string, error) {
	if !entry.Known {
		return "", canvasCommandError("command", "unsupported", fmt.Sprintf("节点 %s 的类型 %q 未注册能力，无法以文档形式读取；改用 canvas_get_state 查看基础信息", entry.ID, entry.Type))
	}
	text, err := document.Render(entry.Descriptor, entry.Node)
	if err != nil {
		return "", canvasCommandError("command", "invalid_value", fmt.Sprintf("节点 %s 无法渲染成文档：%s", entry.ID, err.Error()))
	}
	return text, nil
}

func canvasCommandSubmapIndex(fs *canvasCommandFS, submapID string) string {
	children := fs.children(submapID)
	if len(children) == 0 {
		return "# 子节点：（空 submap；用 new --in " + submapID + " <type> <title> 创建）\n"
	}
	var out strings.Builder
	out.WriteString("# 子节点（" + strconv.Itoa(len(children)) + " 个）：\n")
	for _, child := range children {
		out.WriteString("#   " + child.Path + "	" + strings.TrimSpace(stringValue(child.Node["title"])) + "\n")
	}
	return out.String()
}

func canvasCommandSplitLines(text string) []string {
	return document.ValueLines(text)
}

func canvasCommandTruncate(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	runes := []rune(text)
	return string(runes[:limit]) + "\n# 输出已截断", true
}

func canvasCommandGlobMatch(pattern, path string) bool {
	return canvasCommandGlobSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func canvasCommandGlobSegments(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		for index := 0; index <= len(path); index++ {
			if canvasCommandGlobSegments(pattern[1:], path[index:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 || !canvasCommandGlobSegment(pattern[0], path[0]) {
		return false
	}
	return canvasCommandGlobSegments(pattern[1:], path[1:])
}

func canvasCommandGlobSegment(pattern, text string) bool {
	p, t := []rune(pattern), []rune(text)
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star = pi
			mark = ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
