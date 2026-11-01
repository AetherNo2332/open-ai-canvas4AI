package app

import (
	"encoding/json"
	"time"

	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/repository"
)

// cloudAgentCanvasPage holds one canvas_get_state read: the request, the node
// selection it resolves to, and the page being assembled. Splitting the reader
// into steps keeps each one inside the guard limits without changing output.
type cloudAgentCanvasPage struct {
	repo                    *repository.Repository
	userID, canvasID        string
	doc                     map[string]any
	read                    cloudAgentCanvasReadOptions
	explicitView            bool
	offset, depth           int
	ids, focusNodeIDs       []string
	includeRelated          bool
	storyboardOffset        int
	connectionOffset        int
	readRows                int
	mode                    cloudAgentProjectionMode
	all                     []map[string]any
	nodeByID                map[string]map[string]any
	wanted, focus, selected map[string]bool
	selectionTruncated      bool
	candidates              []map[string]any
	nodes, edges            []any
	included                map[string]bool
	next, nextConnection    int
	pageBytes               int
	readAt                  time.Time
	selectedMode            bool
}

func cloudAgentCanvasStatePageSelected(repo *repository.Repository, userID, canvasID string, doc map[string]any, offset int, ids, focusNodeIDs []string, depth int, includeRelated bool, storyboardOffset, connectionOffset, readRows int, options ...cloudAgentCanvasReadOptions) (any, error) {
	page := &cloudAgentCanvasPage{repo: repo, userID: userID, canvasID: canvasID, doc: doc, read: cloudAgentCanvasReadOptions{View: "edit"}, explicitView: len(options) > 0,
		offset: offset, ids: ids, focusNodeIDs: focusNodeIDs, depth: depth, includeRelated: includeRelated, storyboardOffset: storyboardOffset, connectionOffset: connectionOffset, readRows: readRows}
	if page.explicitView {
		page.read = options[0]
	}
	if err := page.validate(); err != nil {
		return nil, err
	}
	page.readAt = time.Now()
	page.all = creationMaps(doc["nodes"])
	if connectionOffset < 0 {
		return nil, BadAuthRequest("连线分页参数无效")
	}
	// 显式按 nodeIds 读取是"逐字"路径：只有它返回真实 rowId，而编辑工具必须用 rowId。
	page.mode = cloudAgentProjectionPage
	if len(ids) > 0 {
		page.mode = cloudAgentProjectionDetail
	}
	if err := page.resolveTargets(); err != nil {
		return nil, err
	}
	page.selectRelated()
	page.collectCandidates()
	if err := page.appendNodes(); err != nil {
		return nil, err
	}
	page.appendConnections()
	return page.result(), nil
}

func (page *cloudAgentCanvasPage) validate() error {
	if page.offset < 0 || page.storyboardOffset < 0 || len(page.ids) > 8 || len(page.focusNodeIDs) > 8 || (len(page.ids) > 0 && len(page.focusNodeIDs) > 0) {
		return BadAuthRequest("画布读取分页参数无效")
	}
	if len(page.focusNodeIDs) > 0 && (page.depth < 0 || page.depth > 3) {
		return BadAuthRequest("关联子图 depth 必须在 0 到 3 之间")
	}
	if len(page.focusNodeIDs) == 0 && (page.depth != 0 || page.includeRelated) {
		return BadAuthRequest("depth/includeRelated 只能与 focusNodeIds 一起使用")
	}
	if page.includeRelated && page.depth != 0 {
		return BadAuthRequest("includeRelated 与 depth 不能同时使用")
	}
	if page.readRows < 0 || page.readRows > cloudAgentMaxReadRows {
		return BadAuthRequest("每页行数超出限制")
	}
	return nil
}

// resolveTargets indexes the canvas nodes and checks that every explicit or
// focus node exists before any selection is built.
func (page *cloudAgentCanvasPage) resolveTargets() error {
	page.wanted = map[string]bool{}
	for _, id := range page.ids {
		page.wanted[id] = true
	}
	page.focus = map[string]bool{}
	for _, id := range page.focusNodeIDs {
		if id == "" || page.focus[id] {
			return BadAuthRequest("关联子图 focusNodeIds 不能包含空值或重复节点")
		}
		page.focus[id] = true
	}
	page.nodeByID = make(map[string]map[string]any, len(page.all))
	for _, node := range page.all {
		if id := stringValue(node["id"]); id != "" {
			page.nodeByID[id] = node
		}
	}
	for id := range page.wanted {
		if page.nodeByID[id] == nil {
			return BadAuthRequest("指定节点不在当前画布")
		}
	}
	for id := range page.focus {
		if page.nodeByID[id] == nil {
			return BadAuthRequest("关联子图 focusNodeIds 中存在不在当前画布的节点")
		}
	}
	return nil
}

// selectRelated walks the connection graph from the focus nodes, either depth
// levels or (includeRelated) the whole component up to the related-node limit.
func (page *cloudAgentCanvasPage) selectRelated() {
	page.selected = map[string]bool{}
	if len(page.focus) == 0 {
		return
	}
	adjacency := make(map[string][]string, len(page.all))
	for _, edge := range creationMaps(page.doc["connections"]) {
		from, to := stringValue(edge["fromNodeId"]), stringValue(edge["toNodeId"])
		if page.nodeByID[from] == nil || page.nodeByID[to] == nil {
			continue
		}
		adjacency[from] = append(adjacency[from], to)
		adjacency[to] = append(adjacency[to], from)
	}
	frontier := make([]string, 0, len(page.focus))
	// Preserve the caller's focus order. This makes bounded results stable
	// and keeps the selected node itself ahead of its neighbours.
	for _, id := range page.focusNodeIDs {
		page.selected[id] = true
		frontier = append(frontier, id)
	}
	levels := page.depth
	if page.includeRelated {
		levels = len(page.all) + 1
	}
	for level := 0; level < levels && len(frontier) > 0; level++ {
		next := make([]string, 0)
		for _, id := range frontier {
			for _, neighbor := range adjacency[id] {
				if page.selected[neighbor] {
					continue
				}
				if page.includeRelated && len(page.selected) >= cloudAgentRelatedNodeLimit {
					page.selectionTruncated = true
					continue
				}
				page.selected[neighbor] = true
				next = append(next, neighbor)
			}
		}
		frontier = next
	}
}

func (page *cloudAgentCanvasPage) collectCandidates() {
	page.selectedMode = len(page.focus) > 0
	page.candidates = make([]map[string]any, 0, len(page.all))
	for _, node := range page.all {
		id := stringValue(node["id"])
		switch {
		case page.selectedMode && page.selected[id]:
			page.candidates = append(page.candidates, node)
		case !page.selectedMode && len(page.ids) > 0 && page.wanted[id]:
			page.candidates = append(page.candidates, node)
		case !page.selectedMode && len(page.ids) == 0:
			page.candidates = append(page.candidates, node)
		}
	}
}

// appendNodes projects candidates from offset until the node-count or byte
// budget is reached; next records where the following page starts.
func (page *cloudAgentCanvasPage) appendNodes() error {
	page.nodes = []any{}
	page.included = map[string]bool{}
	page.pageBytes = 1024
	for candidateIndex, node := range page.candidates {
		if candidateIndex < page.offset {
			continue
		}
		id := stringValue(node["id"])
		if len(page.ids) == 0 && len(page.focus) == 0 && len(page.nodes) >= cloudAgentListPageNodes {
			page.next = candidateIndex
			break
		}
		item, supported, err := page.nodeItem(node, id)
		if err != nil {
			return err
		}
		if !supported {
			body, _ := json.Marshal(item)
			if page.pageBytes+len(body) > cloudAgentReadPageBytes-(8<<10) {
				page.next = candidateIndex
				break
			}
			page.pageBytes += len(body)
			page.nodes = append(page.nodes, item)
			page.included[id] = true
			continue
		}
		body, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if page.pageBytes+len(body) > cloudAgentReadPageBytes-(8<<10) {
			if len(page.nodes) == 0 {
				return BadAuthRequest("节点详情超过单页读取预算，请使用节点对应的结构化分页工具")
			}
			page.next = candidateIndex
			break
		}
		page.pageBytes += len(body)
		page.nodes = append(page.nodes, item)
		page.included[id] = true
	}
	return nil
}

// nodeItem builds one node's view. supported=false means the type is unknown
// to the Agent: only the base fields are returned and the item is not compacted.
func (page *cloudAgentCanvasPage) nodeItem(node map[string]any, id string) (map[string]any, bool, error) {
	precise := len(page.ids) > 0 || page.focus[id] || (page.explicitView && page.read.View != "catalog")
	meta, _ := node["metadata"].(map[string]any)
	item := cloudAgentCanvasPageNodeBase(node, meta, id)
	capability, known := cloudAgentNodeCapabilityForNode(node)
	if !known {
		// Read visibility is not permission to mutate or use a node as a media reference.
		item["agentSupported"] = false
		item["agentUnsupportedReason"] = "仅展示基础信息；当前 Agent 不支持操作此类型节点"
		return item, false, nil
	}
	item["kind"], item["displayType"] = capability.Type, capability.Label
	if capability.Connection.CanReference {
		item["usage"] = cloudAgentNodeUsageView(node)
	}
	if err := page.projectFields(item, node, meta, capability, precise); err != nil {
		return nil, true, err
	}
	if page.read.View == "references" {
		item["references"] = cloudAgentNodeReferenceView(page.repo, page.userID, page.canvasID, page.doc, node)
	}
	if cloudAgentCharacterNode(node) {
		if err := page.characterView(item, node, id, precise); err != nil {
			return nil, true, err
		}
	}
	if capability.GenerationMode != "" {
		item["generation"] = page.generationView(node, meta, id, capability)
	}
	if draftRunID := stringValue(meta["agentDraftRunId"]); draftRunID != "" && stringValue(meta["taskId"]) == "" && stringValue(meta["generationTaskId"]) == "" {
		draft := map[string]any{"submitted": false, "requiresApproval": true, "ownerStatus": "unknown"}
		owner, err := page.repo.CloudAgent(page.userID, draftRunID)
		if err == nil && owner.CanvasID == page.canvasID {
			draft["ownerStatus"] = owner.Status
			draft["cleanupPending"] = owner.CleanupPending
		}
		item["generationDraft"] = draft
	}
	// 角色卡的引用可用性在 character.imageReference/audioReference 中给出。
	if capability.Connection.CanReference && !cloudAgentCharacterNode(node) {
		page.outputReference(item, node)
	}
	cloudAgentCompactEditable(item)
	return item, true, nil
}

func cloudAgentCanvasPageNodeBase(node, meta map[string]any, id string) map[string]any {
	item := map[string]any{"id": id, "kind": stringValue(node["type"])}
	if title, ok := node["title"].(string); ok {
		item["title"] = truncateRunes(title, 300)
	}
	if position, ok := node["position"].(map[string]any); ok {
		safePosition := map[string]any{}
		for _, axis := range []string{"x", "y"} {
			if value, ok := cloudAgentSafeNumber(position[axis]); ok {
				safePosition[axis] = value
			}
		}
		item["position"] = safePosition
	}
	for _, dimension := range []string{"width", "height"} {
		if value, ok := cloudAgentSafeNumber(node[dimension]); ok {
			item[dimension] = value
		}
	}
	if status, ok := meta["status"].(string); ok {
		item["status"] = truncateRunes(status, 40)
	}
	return item
}

func (page *cloudAgentCanvasPage) projectFields(item, node, meta map[string]any, capability cloudAgentNodeCapability, precise bool) error {
	fields := capability.SummaryFields
	projectMode := page.mode
	textLimit := cloudAgentPageTextLimit
	if precise {
		fields = capability.DetailFields
		projectMode = cloudAgentProjectionDetail
		textLimit = cloudAgentDetailTextLimit
		if page.read.View == "edit" {
			item["editable"] = cloudAgentEditableNodeProjection(node, capability, textLimit)
		}
	}
	if page.read.View == "catalog" {
		fields, projectMode, textLimit = capability.SummaryFields, cloudAgentProjectionIndex, cloudAgentIndexExcerptRunes
	} else if page.read.View == "detail" {
		textLimit = cloudAgentPageTextLimit
	} else if page.read.View == "references" {
		fields = nil
	}
	fields = cloudAgentReadFields(fields, page.read.View)
	projected, err := cloudAgentProjectNodeFields(node, meta, capability, fields, textLimit, projectMode, page.storyboardOffset, page.readRows)
	if err != nil {
		return err
	}
	for key, value := range projected {
		item[key] = value
	}
	cloudAgentContentContinuation(node, item)
	return nil
}

func (page *cloudAgentCanvasPage) characterView(item, node map[string]any, id string, precise bool) error {
	if page.repo == nil {
		item["character"] = map[string]any{"available": false, "issue": "角色资产读取服务不可用"}
		return nil
	}
	canvas, canvasErr := page.repo.CanvasProjectForUser(page.userID, page.canvasID)
	if canvasErr != nil {
		return canvasErr
	}
	character, characterErr := cloudAgentResolveCharacter(page.repo, page.userID, canvas.ProjectID, node)
	if characterErr != nil {
		item["character"] = map[string]any{"available": false, "issue": cloudAgentSafeToolError(characterErr)}
		return nil
	}
	characterView := character.read(precise && page.read.View != "catalog")
	if page.read.View == "references" {
		delete(characterView, "definition")
	}
	cloudAgentCharacterSourceCandidates(characterView, page.doc)
	if precise {
		_, referenceErr := cloudAgentCharacterImageReference(page.repo, page.userID, id, character)
		characterView["imageReference"] = map[string]any{"ready": referenceErr == nil}
		if referenceErr != nil {
			characterView["imageReference"].(map[string]any)["issue"] = cloudAgentSafeToolError(referenceErr)
		}
		_, audioErr := cloudAgentCharacterAudioReference(page.repo, page.userID, id, character)
		characterView["audioReference"] = map[string]any{"ready": audioErr == nil}
		if audioErr != nil {
			characterView["audioReference"].(map[string]any)["issue"] = cloudAgentSafeToolError(audioErr)
		}
	}
	item["character"] = characterView
	return nil
}

func (page *cloudAgentCanvasPage) generationView(node, meta map[string]any, id string, capability cloudAgentNodeCapability) map[string]any {
	generation := map[string]any{"taskStatus": "not_submitted"}
	reason, issue := cloudAgentMediaTargetIssue(node, capability.Type)
	if mediaversions.Supported(capability.GenerationMode) {
		reason, issue = cloudAgentMediaVersionTargetIssue(page.repo, page.userID, page.canvasID, node, cloudAgentMediaArgs{Mode: capability.GenerationMode, NodeID: id})
	}
	if reason != "" {
		generation["submitBlockedReason"], generation["submitBlockedIssue"] = reason, issue
	}
	taskID := stringValue(meta["taskId"])
	if taskID == "" {
		taskID = stringValue(meta["generationTaskId"])
	}
	if taskID != "" {
		// Do not infer success/failure from stale canvas metadata.
		generation["taskStatus"] = "unavailable"
		task, err := page.repo.TaskForUser(page.userID, taskID)
		if err == nil && task.ProjectID == page.canvasID {
			for key, value := range cloudAgentTaskDiagnostic(page.repo, task) {
				generation[key] = value
			}
		}
	}
	if mediaversions.Supported(capability.GenerationMode) {
		if current := mediaversions.Current(node); current != nil {
			generation["currentMediaVersionId"] = current["id"]
		}
		generation["versionCount"] = len(mediaversions.List(node))
		scope, scopeErr := mediaversions.ScopeForNode(page.repo, page.userID, page.canvasID, id)
		if scopeErr == nil {
			admission := mediaversions.CheckAdmission(page.repo, scope, node, "new_version")
			generation["canCreateVersion"] = admission == nil
			if e, ok := admission.(*mediaversions.Error); ok {
				generation["newVersionBlockedReason"] = e.Reason
			}
		}
	}
	return generation
}

func (page *cloudAgentCanvasPage) outputReference(item, node map[string]any) {
	ref, _, err := cloudAgentReference(page.repo, page.userID, node)
	outputReference := map[string]any{"ready": err == nil}
	item["outputReference"] = outputReference
	if err != nil {
		outputReference["issue"] = cloudAgentSafeToolError(err)
		return
	}
	// Provider references contain a storage key for task submission.
	// The model only needs the verified public characteristics; never
	// forward the provider payload or storage locator into the read tool.
	// 只给能影响下游决策的特征：画幅与时长。MIME、字节数与输入分类属于底层细节。
	asset := map[string]any{}
	for _, key := range []string{"width", "height", "durationMs"} {
		if value, ok := cloudAgentSafeNumber(ref[key]); ok && numberValue(value, 0) > 0 {
			asset[key] = value
		}
	}
	if len(asset) > 0 {
		item["asset"] = asset
	}
}

// appendConnections adds edges touching the included nodes after the nodes,
// sharing the same byte budget; nextConnection marks the following page.
func (page *cloudAgentCanvasPage) appendConnections() {
	page.edges = []any{}
	for index, edge := range creationMaps(page.doc["connections"]) {
		if page.read.Connections == "none" {
			break
		}
		if index < page.connectionOffset {
			continue
		}
		fromIncluded := page.included[stringValue(edge["fromNodeId"])]
		toIncluded := page.included[stringValue(edge["toNodeId"])]
		includeEdge := (page.selectedMode && fromIncluded && toIncluded) || (!page.selectedMode && (fromIncluded || toIncluded))
		if page.read.Connections == "selected" {
			includeEdge = fromIncluded && toIncluded
		}
		if page.read.Connections == "incident" {
			includeEdge = fromIncluded || toIncluded
		}
		if !includeEdge {
			continue
		}
		// 连线只读：没有工具接收连线 ID，模型只需要两端与关系。
		item := map[string]any{"fromNodeId": edge["fromNodeId"], "toNodeId": edge["toNodeId"]}
		for _, key := range []string{"fromHandleId", "toHandleId", "relation", "storyboardRowId"} {
			if value := stringValue(edge[key]); value != "" {
				item[key] = value
			}
		}
		body, _ := json.Marshal(item)
		if page.pageBytes+len(body) > cloudAgentReadPageBytes {
			page.nextConnection = index
			break
		}
		page.pageBytes += len(body)
		page.edges = append(page.edges, item)
	}
}

func (page *cloudAgentCanvasPage) result() map[string]any {
	doc := page.doc
	// mediaSnapshotHash 是 generate_media 的 snapshotHash 取值（只覆盖影响生成的内容），与画布写入用的 snapshotHash 不同，必须保留。
	result := map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "mediaSnapshotHash": cloudAgentMediaContentHash(doc), "nodes": page.nodes, "connections": page.edges, "totalNodes": len(page.all)}
	if page.next > 0 {
		result["hasMore"], result["nextOffset"] = true, page.next
	}
	if page.nextConnection > 0 {
		result["hasMoreConnections"], result["nextConnectionOffset"] = true, page.nextConnection
	}
	result["view"], result["connectionMode"] = page.read.View, page.read.Connections
	migration := map[string]any{"required": !references.Enabled(doc)}
	if !references.Enabled(doc) {
		migration["reason"] = "reference_migration_required"
		migration["nextAction"] = "这是旧版引用格式的画布，Agent 暂不能在其上关联素材或生成媒体；请告知用户在画布顶部的「引用迁移」提示中完成一次迁移后再继续；不要重试或改写其他字段来替代绑定"
	}
	result["referenceMigration"] = migration
	// 读取来源与时间：画布内容是最近一次保存的快照，canvasRevision 用来判断是否已有更新的写入。
	result["source"], result["readAt"] = "canvas_snapshot", page.readAt.Format(time.RFC3339)
	if page.repo != nil {
		if canvas, err := page.repo.CanvasProjectMetadataForUser(page.userID, page.canvasID); err == nil && canvas != nil {
			result["canvasRevision"] = canvas.Revision
		}
	}
	if page.selectedMode {
		result["totalSelectedNodes"] = len(page.candidates)
		selection := map[string]any{"mode": "subgraph", "focusNodeIds": page.focusNodeIDs, "selectedNodes": len(page.candidates)}
		if page.includeRelated {
			selection["relationMode"] = "all"
			selection["truncated"] = page.selectionTruncated
			selection["nodeLimit"] = cloudAgentRelatedNodeLimit
		} else {
			selection["depth"] = page.depth
		}
		result["selection"] = selection
	}
	return result
}
