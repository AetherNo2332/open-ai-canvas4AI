package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/canvas/capability"
)

func canvasCommandEdit(doc map[string]any, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	args := step.Args
	if len(args) == 0 {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "required", "edit 需要 <字段文件> 或 --glob <pattern>")
	}
	glob := false
	if args[0] == "--glob" {
		if len(args) < 2 {
			return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "required", "edit --glob 缺少 pattern")
		}
		glob = true
		args = args[1:]
	}
	if glob {
		pattern := canvasCommandPathKey(args[0])
		value := strings.Join(args[1:], " ")
		targets := canvasCommandFieldPaths(fs)
		matched := make([]string, 0)
		for _, path := range targets {
			if canvasCommandGlobMatch(pattern, canvasCommandPathKey(path)) {
				matched = append(matched, path)
			}
		}
		if len(matched) == 0 {
			return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "not_found", fmt.Sprintf("--glob %s 没有匹配到任何字段文件", args[0]))
		}
		outputs := make([]string, 0, len(matched))
		for _, path := range matched {
			entry, key, err := fs.resolveFieldPath(path)
			if err != nil {
				return cloudAgentApprovalPreviewItem{}, "", err
			}
			if err := canvasCommandWriteField(entry, key, value); err != nil {
				return cloudAgentApprovalPreviewItem{}, "", err
			}
			outputs = append(outputs, entry.Path+key)
		}
		return cloudAgentApprovalPreviewItem{Operation: "edit", Fields: []string{args[0]}, Summary: fmt.Sprintf("批量写入 %d 个字段文件", len(matched))}, "已写入 " + strings.Join(outputs, "、"), nil
	}
	if len(args) < 1 {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "required", "edit 需要字段文件路径")
	}
	entry, key, err := fs.resolveFieldPath(args[0])
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	value := strings.Join(args[1:], " ")
	if err := canvasCommandWriteField(entry, key, value); err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	return cloudAgentApprovalPreviewItem{Operation: "edit", NodeID: entry.ID, NodeTitle: strings.TrimSpace(stringValue(entry.Node["title"])), NodeType: entry.Type, NodeTypeLabel: entry.Descriptor.Label, Fields: []string{key}, Summary: fmt.Sprintf("写入 %s%s", entry.Path, key)}, fmt.Sprintf("已写入 %s%s", entry.Path, key), nil
}

func canvasCommandSed(doc map[string]any, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	if len(step.Args) != 3 || step.Args[0] != "-i" {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "invalid_value", "sed 只支持 sed -i s/<原文>/<替换>/ <字段文件>")
	}
	from, to, err := canvasCommandParseSubstitution(step.Args[1])
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	entry, key, err := fs.resolveFieldPath(step.Args[2])
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	if err := canvasCommandRequireUnlocked(entry); err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	value, _ := canvasCommandFieldValue(entry, key)
	text := canvasCommandValueText(value)
	if count := strings.Count(text, from); count != 1 {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "invalid_value", fmt.Sprintf("sed 原文必须在字段值里唯一命中，当前命中 %d 次；请扩大匹配片段，或改用 cat > <字段文件> <<'EOF' 整段重写", count))
	}
	if err := canvasCommandWriteField(entry, key, strings.Replace(text, from, to, 1)); err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	return cloudAgentApprovalPreviewItem{Operation: "sed", NodeID: entry.ID, NodeTitle: strings.TrimSpace(stringValue(entry.Node["title"])), NodeType: entry.Type, NodeTypeLabel: entry.Descriptor.Label, Fields: []string{key}, Summary: fmt.Sprintf("替换 %s%s 中的一段文本", entry.Path, key)}, fmt.Sprintf("已替换 %s%s 中的一段文本", entry.Path, key), nil
}

func canvasCommandParseSubstitution(script string) (string, string, error) {
	if len(script) < 4 || script[0] != 's' {
		return "", "", canvasCommandError("command", "invalid_value", "sed 脚本必须是 s/<原文>/<替换>/")
	}
	delimiter := script[1]
	if delimiter == '\\' || delimiter == '\n' {
		return "", "", canvasCommandError("command", "invalid_value", "sed 分隔符无效")
	}
	parts := strings.Split(script[2:], string(delimiter))
	if len(parts) < 2 {
		return "", "", canvasCommandError("command", "invalid_value", "sed 脚本缺少替换段；格式 s/<原文>/<替换>/")
	}
	if parts[0] == "" {
		return "", "", canvasCommandError("command", "invalid_value", "sed 原文不能为空")
	}
	return parts[0], parts[1], nil
}

func canvasCommandHeredocWrite(doc map[string]any, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	if step.Body == "" {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "required", "cat > 需要 heredoc 正文；格式 cat > ./image-1/prompt.txt <<'EOF'")
	}
	entry, key, err := fs.resolveFieldPath(step.Redirect)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	if err := canvasCommandWriteField(entry, key, step.Body); err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	return cloudAgentApprovalPreviewItem{Operation: "edit", NodeID: entry.ID, NodeTitle: strings.TrimSpace(stringValue(entry.Node["title"])), NodeType: entry.Type, NodeTypeLabel: entry.Descriptor.Label, Fields: []string{key}, Summary: fmt.Sprintf("用 heredoc 写入 %s%s", entry.Path, key)}, fmt.Sprintf("已写入 %s%s（%d 字）", entry.Path, key, utf8.RuneCountInString(step.Body)), nil
}

func canvasCommandWriteField(entry canvasCommandNode, key, raw string) error {
	if err := canvasCommandRequireUnlocked(entry); err != nil {
		return err
	}
	for _, field := range canvasCommandFields(entry) {
		if field.Key != key {
			continue
		}
		if !field.Writable {
			return canvasCommandError("command", "read_only_field", fmt.Sprintf("%s%s 是只读字段，Agent 不能写入", entry.Path, key))
		}
		if field.Field != nil {
			value, err := canvasCommandCoerceField(field, raw)
			if err != nil {
				return err
			}
			coerced, err := field.Field.Coerce(value, key)
			if err != nil {
				return canvasCommandError("command", "invalid_value", err.Error())
			}
			field.Field.Write(entry.Node, coerced)
			return nil
		}
		value, err := canvasCommandCoercePatch(field, raw)
		if err != nil {
			return err
		}
		if err := entry.Descriptor.ApplyPatch(entry.Node, map[string]any{key: value}); err != nil {
			return canvasCommandError("command", "invalid_value", err.Error())
		}
		return nil
	}
	return canvasCommandFieldNotFound(entry, key)
}

func canvasCommandCoerceField(field canvasCommandField, raw string) (any, error) {
	if field.Field == nil {
		return nil, canvasCommandError("command", "invalid_value", "字段声明缺失")
	}
	switch field.Field.Kind {
	case capability.KindString:
		return raw, nil
	case capability.KindNumber:
		number, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是数字", field.Key))
		}
		return number, nil
	case capability.KindBoolean:
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是布尔值", field.Key))
		}
		return value, nil
	case capability.KindStrings:
		values, err := canvasCommandStringList(raw)
		if err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是 JSON 数组或一行一项的文本列表", field.Key))
		}
		return values, nil
	case capability.KindMap:
		if strings.TrimSpace(raw) == "" {
			return map[string]any{}, nil
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是 JSON 对象", field.Key))
		}
		return value, nil
	case capability.KindCollection:
		if strings.TrimSpace(raw) == "" {
			return []any{}, nil
		}
		var value []any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是 JSON 对象数组", field.Key))
		}
		return value, nil
	default:
		return raw, nil
	}
}

func canvasCommandCoercePatch(field canvasCommandField, raw string) (any, error) {
	if field.Patch == nil {
		return nil, canvasCommandError("command", "invalid_value", "字段声明缺失")
	}
	switch field.Patch.Kind {
	case "string":
		return raw, nil
	case "number":
		number, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是数字", field.Key))
		}
		return number, nil
	case "boolean":
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是布尔值", field.Key))
		}
		return value, nil
	case "array":
		if field.Patch.Items != nil && field.Patch.Items.Kind == "string" {
			values, err := canvasCommandStringList(raw)
			if err != nil {
				return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是 JSON 数组或一行一项的文本列表", field.Key))
			}
			return values, nil
		}
		if strings.TrimSpace(raw) == "" {
			return []any{}, nil
		}
		var value []any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是 JSON 数组", field.Key))
		}
		return value, nil
	case "object":
		if strings.TrimSpace(raw) == "" {
			return map[string]any{}, nil
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 必须是 JSON 对象", field.Key))
		}
		return value, nil
	default:
		return raw, nil
	}
}

func canvasCommandStringList(raw string) ([]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []any{}, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var values []any
		if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
			return nil, err
		}
		return values, nil
	}
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []any{}, nil
	}
	lines := strings.Split(text, "\n")
	values := make([]any, 0, len(lines))
	for _, line := range lines {
		values = append(values, line)
	}
	return values, nil
}

func canvasCommandRequireUnlocked(entry canvasCommandNode) error {
	if canvasCommandLocked(entry.Node) {
		return canvasCommandError("command", "locked", fmt.Sprintf("节点 %s 已锁定；请让用户解锁后再修改", entry.Path))
	}
	return nil
}

func canvasCommandLocked(node map[string]any) bool {
	meta, _ := node["metadata"].(map[string]any)
	return meta != nil && meta["locked"] == true
}

func canvasCommandLockedReferrers(doc map[string]any, removed map[string]bool) []string {
	blocked := make([]string, 0)
	for _, node := range creationMaps(doc["nodes"]) {
		id := stringValue(node["id"])
		if removed[id] || !canvasCommandLocked(node) {
			continue
		}
		if canvasCommandNodeReferencesAny(node, removed) {
			title := strings.TrimSpace(stringValue(node["title"]))
			if title == "" {
				title = id
			}
			blocked = append(blocked, title+"（"+id+"）")
		}
	}
	sort.Strings(blocked)
	return blocked
}

func canvasCommandNodeReferencesAny(node map[string]any, removed map[string]bool) bool {
	meta, _ := node["metadata"].(map[string]any)
	if meta == nil {
		return false
	}
	return canvasCommandReferenceObjectMatches(meta, removed)
}

func canvasCommandReferenceObjectMatches(object map[string]any, removed map[string]bool) bool {
	for key, value := range object {
		if canvasCommandReferenceValueMatches(key, value, removed) {
			return true
		}
	}
	return false
}

func canvasCommandReferenceValueMatches(key string, value any, removed map[string]bool) bool {
	switch typed := value.(type) {
	case string:
		return (strings.HasSuffix(key, "NodeId") || key == "primaryImageId" || key == "batchRootId") && removed[typed]
	case []any:
		return canvasCommandReferenceListMatches(key, typed, removed)
	case map[string]any:
		return canvasCommandReferenceObjectMatches(typed, removed)
	default:
		return false
	}
}

func canvasCommandReferenceListMatches(key string, items []any, removed map[string]bool) bool {
	for _, item := range items {
		switch entry := item.(type) {
		case string:
			if (strings.HasSuffix(key, "NodeIds") || key == "batchChildIds") && removed[entry] {
				return true
			}
		case map[string]any:
			if (key == "assetBindings" || key == "referenceBindings" || key == "inputBindings") && removed[stringValue(entry["nodeId"])] {
				return true
			}
			if canvasCommandReferenceObjectMatches(entry, removed) {
				return true
			}
		}
	}
	return false
}

func canvasCommandCloneNode(source map[string]any, id, title string, keepScope, keepReferences bool) (map[string]any, error) {
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, err
	}
	clone["id"] = id
	clone["title"] = title
	if position, ok := clone["position"].(map[string]any); ok {
		if x, ok := position["x"].(float64); ok {
			position["x"] = x + canvasCommandCopyOffset
		}
		if y, ok := position["y"].(float64); ok {
			position["y"] = y + canvasCommandCopyOffset
		}
	}
	meta, _ := clone["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		clone["metadata"] = meta
	}
	remove := []string{
		"taskId", "generationTaskId", "assetId", "storageKey", "resourceId", "currentMediaVersionId",
		"mediaVersions", "media", "output", "agentDraftRunId", "batchRootId", "batchChildIds",
		"locked", "parentId",
	}
	if !keepReferences {
		remove = append(remove,
			"referenceNodeIds", "referenceTextNodeIds", "videoStartFrameNodeId", "videoEndFrameNodeId",
			"generationSpec", "inputBindings", "referenceBindings", "assetBindings",
		)
	}
	for _, key := range remove {
		delete(meta, key)
	}
	delete(clone, "parentId")
	if !keepScope {
		delete(clone, "scopeId")
	}
	return clone, nil
}

// canvasCommandRemapReferences 在副本子树内重映射引用；指向子树外的引用被删除并计数。
func canvasCommandRemapReferences(node map[string]any, idMap map[string]string) int {
	meta, _ := node["metadata"].(map[string]any)
	if meta == nil {
		return 0
	}
	dropped := 0
	canvasCommandRemapObject(meta, idMap, &dropped)
	return dropped
}

func canvasCommandRemapObject(object map[string]any, idMap map[string]string, dropped *int) {
	for key, value := range object {
		switch typed := value.(type) {
		case string:
			canvasCommandRemapStringField(object, key, typed, idMap, dropped)
		case []any:
			object[key] = canvasCommandRemapList(key, typed, idMap, dropped)
		case map[string]any:
			canvasCommandRemapObject(typed, idMap, dropped)
		}
	}
}

func canvasCommandRemapStringField(object map[string]any, key, value string, idMap map[string]string, dropped *int) {
	if key == "parentId" || key == "scopeId" {
		return
	}
	if !strings.HasSuffix(key, "NodeId") && key != "primaryImageId" && key != "batchRootId" {
		return
	}
	if mapped, ok := idMap[value]; ok {
		object[key] = mapped
	} else if value != "" {
		delete(object, key)
		*dropped++
	}
}

func canvasCommandRemapList(key string, items []any, idMap map[string]string, dropped *int) []any {
	next := make([]any, 0, len(items))
	for _, item := range items {
		switch entry := item.(type) {
		case string:
			if strings.HasSuffix(key, "NodeIds") || key == "batchChildIds" {
				if mapped, ok := idMap[entry]; ok {
					next = append(next, mapped)
				} else {
					*dropped++
				}
				continue
			}
			next = append(next, entry)
		case map[string]any:
			if key == "referenceBindings" || key == "inputBindings" || key == "assetBindings" {
				nodeID := stringValue(entry["nodeId"])
				if mapped, ok := idMap[nodeID]; ok {
					entry["nodeId"] = mapped
				} else if nodeID != "" {
					*dropped++
					continue
				}
			}
			canvasCommandRemapObject(entry, idMap, dropped)
			next = append(next, entry)
		default:
			next = append(next, item)
		}
	}
	return next
}

func canvasCommandTitleTaken(doc map[string]any, scopeID, title, exceptID string) bool {
	want := strings.TrimSpace(title)
	if want == "" {
		return false
	}
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == exceptID {
			continue
		}
		if strings.TrimSpace(stringValue(node["scopeId"])) != scopeID {
			continue
		}
		if strings.TrimSpace(stringValue(node["title"])) == want {
			return true
		}
	}
	return false
}

func canvasCommandLoadCounters(doc map[string]any) map[string]int64 {
	counters := map[string]int64{}
	raw, _ := doc["idCounters"].(map[string]any)
	for key, value := range raw {
		switch typed := value.(type) {
		case float64:
			counters[key] = int64(typed)
		case int64:
			counters[key] = typed
		case int:
			counters[key] = int64(typed)
		case json.Number:
			number, _ := typed.Int64()
			counters[key] = number
		}
	}
	return counters
}

func canvasCommandStoreCounters(doc map[string]any, counters map[string]int64) {
	if len(counters) == 0 {
		return
	}
	raw := make(map[string]any, len(counters))
	for key, value := range counters {
		raw[key] = value
	}
	doc["idCounters"] = raw
}

func canvasCommandNextID(doc map[string]any, counters map[string]int64, prefix string) string {
	return canvasCommandNextIDIn(doc, counters, creationMaps(doc["nodes"]), prefix)
}

func canvasCommandNextIDIn(doc map[string]any, counters map[string]int64, nodes []map[string]any, prefix string) string {
	max := counters[prefix]
	for _, node := range nodes {
		id := stringValue(node["id"])
		if !strings.HasPrefix(id, prefix+"-") {
			continue
		}
		rest := strings.TrimPrefix(id, prefix+"-")
		index, err := strconv.ParseInt(rest, 10, 64)
		if err == nil && index > max {
			max = index
		}
	}
	index := max + 1
	counters[prefix] = index
	return fmt.Sprintf("%s-%d", prefix, index)
}

func canvasCommandNextPosition(doc map[string]any, scopeID string) map[string]any {
	count := 0
	for _, node := range creationMaps(doc["nodes"]) {
		if strings.TrimSpace(stringValue(node["scopeId"])) == scopeID {
			count++
		}
	}
	x := 80.0 + float64(count%4)*320
	y := 80.0 + float64(count/4)*240
	if scopeID != "" {
		x = 40.0 + float64(count%3)*280
		y = 40.0 + float64(count/3)*200
	}
	return map[string]any{"x": x, "y": y}
}

func canvasCommandCloneDocument(doc map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, err
	}
	return clone, nil
}

func canvasCommandFieldPaths(fs *canvasCommandFS) []string {
	paths := make([]string, 0, len(fs.nodes)*4)
	for _, entry := range fs.nodes {
		promptAlias := false
		for _, field := range canvasCommandFields(entry) {
			paths = append(paths, entry.Path+field.Key+canvasCommandFieldExtension(field))
			if field.Key == "prompt" {
				promptAlias = true
			}
		}
		if canvasCommandIsMediaNode(entry) && !promptAlias {
			paths = append(paths, entry.Path+"prompt.txt")
		}
	}
	sort.Strings(paths)
	return paths
}
