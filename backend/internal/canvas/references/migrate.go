package references

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/contract"
)

// 规范化的两种来源选择：字段是权威，还是旧连线是权威。
const (
	MigrationChoiceFields      = "fields"
	MigrationChoiceConnections = "connections"
)

// MigrationConflict 是一个字段与旧连线互相矛盾的作用域。FieldNodeIDs 是只有权威
// 字段声明的引用节点，EdgeNodeIDs 是只有旧连线声明的引用节点；两者都按作用域聚合。
// 解决方式是在下一次调用中用 ScopeKey 传入 MigrationChoiceFields 或
// MigrationChoiceConnections。
type MigrationConflict struct {
	ScopeKey     string
	FieldNodeIDs []string
	EdgeNodeIDs  []string
}

// MigrationResult 是规范化结果，并列出仍需用户选择来源的作用域。
type MigrationResult struct {
	Result
	Conflicts []MigrationConflict
}

// ScopeKey 返回迁移来源选择与冲突使用的稳定作用域标识。
// 分镜行与批量行必须带行标识，其余作用域以节点为界。
func ScopeKey(scope Scope) string {
	switch scope.Kind {
	case "storyboard-row", "batch-row":
		return scope.Kind + ":" + scope.NodeID + ":" + scope.RowID
	default:
		return scope.Kind + ":" + scope.NodeID
	}
}

// migrationRef 是一条由字段或旧连线声明的引用。role 在分镜行里是素材角色，其余
// 作用域使用 ReferenceBinding.role。
type migrationRef struct {
	nodeID string
	role   string
	media  string
}

// migrationGroup 是一份可独立写作的引用列表（分镜行的 assetBindings、批量行的
// inputNodeIds/textNodeIds、生成合同或 inputBindings 的引用数组）。
type migrationGroup struct {
	present  bool
	declared []migrationRef
	claims   []migrationRef
	edges    []string
}

// migrationOutput 是分镜行的一个输出槽位（imageNodeId / videoNodeId）。
type migrationOutput struct {
	declared string
	claims   []string
	edges    []string
}

type migrationPlan struct {
	key       string
	kind      string
	nodeID    string
	rowID     string
	inherited []migrationRef
	groups    map[string]*migrationGroup
	groupKeys []string
	outputs   map[string]*migrationOutput
	spec      contract.GenerationSpec
	skip      bool
	choice    string
}

func emptyMigrationResult() MigrationResult {
	return MigrationResult{Result: Result{ReadNodeIDs: []string{}, ReadEdgeIDs: []string{}}, Conflicts: []MigrationConflict{}}
}

// NormalizeLegacy 把一份历史画布一次性切换到声明式引用合同：
//
//   - 字段缺失而旧连线可唯一解释时，用连线补全字段（分镜行素材、表级共享引用、
//     分镜输出、批量行输入、普通输入节点与历史媒体节点的引用）。
//   - 显式字段与旧连线不一致时该作用域是冲突：不做静默并集，也不清掉普通连线，
//     而是要求对作用域选择 fields 或 connections。
//   - 未决冲突保持整份文档不变，只有全部解决后才写入 referenceFieldsVersion=1，
//     并把被接管的旧连线替换为托管连线；因此同一 choices 重复执行是幂等的。
//   - 模式不明的首尾帧只按已声明字段确认，普通连线只能解释为普通引用。
//
// 输入文档不会被修改，调用方可以把它原样保存为撤销前像。本函数不访问数据库、
// 网络或任务队列。
func NormalizeLegacy(doc map[string]any, choices map[string]string, registry *capability.Registry, nextID func() string) (MigrationResult, error) {
	if registry == nil {
		registry = capability.BuiltinRegistry()
	}
	if nextID == nil {
		nextID = uuid.NewString
	}
	result := emptyMigrationResult()
	next, err := Clone(doc)
	if err != nil {
		return result, err
	}
	result.Document = next
	// 一次切换：已标记的文档不再规范化；恢复历史快照后按标记重新进入本入口。
	if Enabled(doc) {
		return result, nil
	}
	plans, order := collectMigrationPlans(next, registry)
	conflicts := []MigrationConflict{}
	applied := []*migrationPlan{}
	for _, key := range order {
		plan := plans[key]
		describeMigrationPlan(next, registry, plan)
		if plan.skip {
			continue
		}
		fieldOnly, edgeOnly := plan.evaluate()
		if len(edgeOnly) == 0 {
			applied = append(applied, plan)
			continue
		}
		if choice := migrationChoice(choices[key]); choice != "" {
			plan.choice = choice
			applied = append(applied, plan)
			continue
		}
		conflicts = append(conflicts, MigrationConflict{ScopeKey: key, FieldNodeIDs: fieldOnly, EdgeNodeIDs: edgeOnly})
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].ScopeKey < conflicts[j].ScopeKey })
	if len(conflicts) > 0 {
		result.Conflicts = conflicts
		return result, nil
	}
	consumed := map[string]bool{}
	for _, plan := range applied {
		if err := applyMigrationPlan(next, plan); err != nil {
			return emptyMigrationResult(), err
		}
		for _, id := range plan.claimedEdges() {
			consumed[id] = true
		}
	}
	next["referenceFieldsVersion"] = Version
	if len(consumed) > 0 {
		kept := []any{}
		for _, edge := range maps(next["connections"]) {
			if !consumed[str(edge["id"])] {
				kept = append(kept, edge)
			}
		}
		next["connections"] = kept
	}
	reconciled, err := Reconcile(doc, next, registry, nextID)
	if err != nil {
		return emptyMigrationResult(), err
	}
	result.Result = reconciled
	for id := range consumed {
		result.ReadEdgeIDs = append(result.ReadEdgeIDs, id)
	}
	result.ReadEdgeIDs = sortedUnique(result.ReadEdgeIDs)
	return result, nil
}

func migrationChoice(value string) string {
	switch strings.TrimSpace(value) {
	case MigrationChoiceFields:
		return MigrationChoiceFields
	case MigrationChoiceConnections:
		return MigrationChoiceConnections
	}
	return ""
}

// collectMigrationPlans 把仍是普通连线的历史边解释成作用域声明。
func collectMigrationPlans(doc map[string]any, registry *capability.Registry) (map[string]*migrationPlan, []string) {
	idx := index(doc)
	plans := map[string]*migrationPlan{}
	order := []string{}
	planFor := func(key, kind, nodeID, rowID string) *migrationPlan {
		if plan, exists := plans[key]; exists {
			return plan
		}
		plan := &migrationPlan{key: key, kind: kind, nodeID: nodeID, rowID: rowID, groups: map[string]*migrationGroup{}, outputs: map[string]*migrationOutput{}}
		plans[key] = plan
		order = append(order, key)
		return plan
	}
	group := func(plan *migrationPlan, tag string) *migrationGroup {
		if item, exists := plan.groups[tag]; exists {
			return item
		}
		item := &migrationGroup{}
		plan.groups[tag] = item
		plan.groupKeys = append(plan.groupKeys, tag)
		return item
	}
	frames := keyframeOutputs(maps(doc["nodes"]))
	columns := map[string]*migrationGroup{}
	columnTables := map[string][]string{}
	for _, edge := range maps(doc["connections"]) {
		if str(edge["managedBy"]) == ManagedBy {
			continue
		}
		edgeID := str(edge["id"])
		fromID, toID := str(edge["fromNodeId"]), str(edge["toNodeId"])
		from, to := idx[fromID], idx[toID]
		if from == nil || to == nil || fromID == toID {
			continue
		}
		if str(to["type"]) == "script" {
			handle := str(edge["toHandleId"])
			if rowID, ok := strings.CutPrefix(handle, "row:"); ok {
				if rowFor(to, "storyboard", rowID) == nil {
					continue
				}
				binding, err := bindNode(idx, registry, fromID, "reference")
				if err != nil {
					continue
				}
				role, ok := storyboardAssetRole(from)
				if !ok || !storyboardRoleMatches(role, binding.MediaType) {
					continue
				}
				item := group(planFor(ScopeKey(Scope{Kind: "storyboard-row", NodeID: toID, RowID: rowID}), "storyboard-row", toID, rowID), "")
				item.claims = append(item.claims, migrationRef{nodeID: fromID, role: role, media: binding.MediaType})
				item.edges = append(item.edges, edgeID)
				continue
			}
			if handle == "storyboard:context" {
				binding, err := bindNode(idx, registry, fromID, "reference")
				if err != nil {
					continue
				}
				item := group(planFor(ScopeKey(Scope{Kind: "storyboard-context", NodeID: toID}), "storyboard-context", toID, ""), "")
				item.claims = append(item.claims, migrationRef{nodeID: fromID, role: binding.Role, media: binding.MediaType})
				item.edges = append(item.edges, edgeID)
				continue
			}
		}
		if str(from["type"]) == "script" && (str(to["type"]) == "image" || str(to["type"]) == "video") {
			if rowID, ok := strings.CutPrefix(str(edge["fromHandleId"]), "row:"); ok {
				if rowFor(from, "storyboard", rowID) == nil {
					continue
				}
				plan := planFor(ScopeKey(Scope{Kind: "storyboard-row", NodeID: fromID, RowID: rowID}), "storyboard-row", fromID, rowID)
				mode := str(to["type"])
				output := plan.outputs[mode]
				if output == nil {
					output = &migrationOutput{}
					plan.outputs[mode] = output
				}
				output.claims = append(output.claims, toID)
				output.edges = append(output.edges, edgeID)
				continue
			}
		}
		if str(to["type"]) == "batch-table" {
			tag, columnID, ok := batchColumnHandle(str(edge["toHandleId"]))
			if !ok {
				continue
			}
			key := toID + "\x00" + tag + "\x00" + columnID
			column := columns[key]
			if column == nil {
				column = &migrationGroup{}
				columns[key] = column
				columnTables[toID] = append(columnTables[toID], key)
			}
			if columnSourceSeen(column, fromID) {
				continue
			}
			column.claims = append(column.claims, migrationRef{nodeID: fromID, role: batchColumnRole(tag)})
			column.edges = append(column.edges, edgeID)
			continue
		}
		kind := mediaScopeKind(registry, to)
		if kind == "" {
			continue
		}
		relation := str(edge["relation"])
		if relation != "" && relation != "storyboard-asset-reference" {
			continue
		}
		binding, err := bindNode(idx, registry, fromID, "reference")
		if err != nil {
			continue
		}
		source, ok := descriptor(registry, from)
		target, ok2 := descriptor(registry, to)
		if !ok || !ok2 || !target.AllowsInput(source.InputKind) {
			continue
		}
		role := binding.Role
		if frameID, ok := frames[toID]; ok && frameID == fromID && role == "reference" {
			role = "first-frame"
		}
		item := group(planFor(ScopeKey(Scope{Kind: kind, NodeID: toID}), kind, toID, ""), "")
		item.claims = append(item.claims, migrationRef{nodeID: fromID, role: role, media: binding.MediaType})
		item.edges = append(item.edges, edgeID)
	}
	collectBatchColumns(doc, planFor, group, idx, columns, columnTables)
	return plans, order
}

func columnSourceSeen(column *migrationGroup, nodeID string) bool {
	for _, claim := range column.claims {
		if claim.nodeID == nodeID {
			return true
		}
	}
	return false
}

// collectBatchColumns 按批量表列把连线展开到行：单元素列广播到每一行，多元素列按行序
// 对齐；连线只解释它所在列，因此不代表整行输入列表。
func collectBatchColumns(doc map[string]any, planFor func(string, string, string, string) *migrationPlan, group func(*migrationPlan, string) *migrationGroup, idx map[string]map[string]any, columns map[string]*migrationGroup, columnTables map[string][]string) {
	for _, node := range maps(doc["nodes"]) {
		if str(node["type"]) != "batch-table" || len(columnTables[str(node["id"])]) == 0 {
			continue
		}
		tableID := str(node["id"])
		table := obj(meta(node)["batchTable"])
		rows := maps(table["rows"])
		if len(rows) == 0 {
			// 没有行可以归属：连线保持原样，不凭列生成用户没有创建过的任务行。
			continue
		}
		keys := []string{}
		seen := map[string]bool{}
		for _, tag := range []string{"node", "text"} {
			field := "referenceColumns"
			if tag == "text" {
				field = "textColumns"
			}
			for _, column := range maps(table[field]) {
				key := tableID + "\x00" + tag + "\x00" + str(column["id"])
				if seen[key] || columns[key] == nil {
					continue
				}
				seen[key] = true
				keys = append(keys, key)
			}
		}
		for _, key := range columnTables[tableID] {
			if seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
		for index, row := range rows {
			for _, key := range keys {
				column := columns[key]
				if column == nil {
					continue
				}
				position := index
				if len(column.claims) == 1 {
					position = 0
				}
				if position >= len(column.claims) {
					continue
				}
				claim := column.claims[position]
				tag := key[strings.Index(key, "\x00")+1:]
				tag = tag[:strings.Index(tag, "\x00")]
				plan := planFor(ScopeKey(Scope{Kind: "batch-row", NodeID: tableID, RowID: str(row["id"])}), "batch-row", tableID, str(row["id"]))
				item := group(plan, tag)
				item.claims = append(item.claims, migrationRef{nodeID: claim.nodeID, role: batchColumnRole(tag)})
				item.edges = append(item.edges, column.edges[position])
			}
		}
	}
}

func batchColumnHandle(handle string) (string, string, bool) {
	if column, ok := strings.CutPrefix(handle, "batch-reference:"); ok && column != "" {
		return "node", column, true
	}
	if column, ok := strings.CutPrefix(handle, "batch-text:"); ok && column != "" {
		return "text", column, true
	}
	return "", "", false
}

func batchColumnRole(tag string) string {
	if tag == "text" {
		return "source-text"
	}
	return "reference"
}

// describeMigrationPlan 读取作用域的字段权威状态。
func describeMigrationPlan(doc map[string]any, registry *capability.Registry, plan *migrationPlan) {
	idx := index(doc)
	node := idx[plan.nodeID]
	if node == nil {
		plan.skip = true
		return
	}
	switch plan.kind {
	case "storyboard-row":
		row := rowFor(node, "storyboard", plan.rowID)
		if row == nil || str(node["type"]) != "script" {
			plan.skip = true
			return
		}
		item := migrationGroupFor(plan, "")
		raw, present := row["assetBindings"]
		item.present = present
		for _, asset := range maps(raw) {
			id, role := str(asset["nodeId"]), str(asset["role"])
			if id == "" {
				continue
			}
			binding, err := bindNode(idx, registry, id, "reference")
			if err != nil {
				plan.skip = true
				return
			}
			item.declared = append(item.declared, migrationRef{nodeID: id, role: role, media: binding.MediaType})
		}
		resolution, err := resolveIndexed(idx, Scope{Kind: "storyboard-row", NodeID: plan.nodeID, RowID: plan.rowID}, registry)
		if err != nil {
			plan.skip = true
			return
		}
		own := map[string]bool{}
		for _, ref := range item.declared {
			own[ref.nodeID] = true
		}
		for _, binding := range resolution.Bindings {
			if own[binding.NodeID] {
				continue
			}
			plan.inherited = append(plan.inherited, migrationRef{nodeID: binding.NodeID, role: binding.Role, media: binding.MediaType})
		}
		for _, mode := range []string{"image", "video"} {
			output := plan.outputs[mode]
			if output == nil {
				continue
			}
			output.declared = str(row[mode+"NodeId"])
		}
		capGroup(item, 100)
	case "storyboard-context":
		if str(node["type"]) != "script" {
			plan.skip = true
			return
		}
		storyboard := obj(meta(node)["storyboard"])
		item := migrationGroupFor(plan, "")
		raw, present := storyboard["referenceNodeIds"]
		item.present = present
		for _, id := range stringsList(raw) {
			binding, err := bindNode(idx, registry, id, "reference")
			if err != nil {
				plan.skip = true
				return
			}
			item.declared = append(item.declared, migrationRef{nodeID: id, role: binding.Role, media: binding.MediaType})
		}
		capGroup(item, 100)
	case "batch-row":
		row := rowFor(node, "batchTable", plan.rowID)
		if row == nil {
			plan.skip = true
			return
		}
		for _, tag := range plan.groupKeys {
			item := plan.groups[tag]
			key := "inputNodeIds"
			if tag == "text" {
				key = "textNodeIds"
			}
			raw, present := row[key]
			item.present = present
			for _, id := range stringsList(raw) {
				binding, err := bindNode(idx, registry, id, "reference")
				if err != nil {
					plan.skip = true
					return
				}
				role := "reference"
				if tag == "text" {
					role = "source-text"
				}
				item.declared = append(item.declared, migrationRef{nodeID: id, role: role, media: binding.MediaType})
			}
			capGroup(item, 10)
		}
	case "generation":
		item := migrationGroupFor(plan, "")
		raw := meta(node)["generationSpec"]
		if raw == nil {
			spec, err := legacyGenerationSpec(node)
			if err != nil {
				plan.skip = true
				return
			}
			plan.spec = spec
			break
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			plan.skip = true
			return
		}
		spec, err := contract.Decode(encoded)
		if err != nil {
			plan.skip = true
			return
		}
		plan.spec = spec
		item.present = true
		for _, binding := range spec.ReferenceBindings {
			item.declared = append(item.declared, migrationRef{nodeID: binding.NodeID, role: binding.Role, media: binding.MediaType})
		}
		if target, ok := descriptor(registry, node); ok {
			capGroup(item, target.Connection.MaxInputCount)
		}
	case "node":
		item := migrationGroupFor(plan, "")
		raw, present := meta(node)["inputBindings"]
		item.present = present
		bindings, err := decodeBindings(raw)
		if err != nil {
			plan.skip = true
			return
		}
		for _, binding := range bindings {
			item.declared = append(item.declared, migrationRef{nodeID: binding.NodeID, role: binding.Role, media: binding.MediaType})
		}
		if target, ok := descriptor(registry, node); ok {
			capGroup(item, target.Connection.MaxInputCount)
		}
	}
}

func migrationGroupFor(plan *migrationPlan, tag string) *migrationGroup {
	if item, exists := plan.groups[tag]; exists {
		return item
	}
	item := &migrationGroup{}
	plan.groups[tag] = item
	plan.groupKeys = append(plan.groupKeys, tag)
	return item
}

// capGroup 丢弃超出目标容量的声明：这些旧连线不属于可解释范围，保持原样交给用户处理。
func capGroup(item *migrationGroup, limit int) {
	if limit <= 0 || len(item.claims) <= limit {
		return
	}
	item.claims = item.claims[:limit]
	item.edges = item.edges[:limit]
}

// evaluate 返回字段侧与连线侧各自独有的引用节点。连线没有带来新引用时不算冲突。
func (p *migrationPlan) evaluate() ([]string, []string) {
	fieldOnly := []string{}
	edgeOnly := []string{}
	roles := map[string][]string{}
	declaredIDs := []string{}
	for _, ref := range p.inherited {
		if _, exists := roles[ref.nodeID]; !exists {
			declaredIDs = append(declaredIDs, ref.nodeID)
		}
		roles[ref.nodeID] = append(roles[ref.nodeID], ref.role)
	}
	claimed := map[string]bool{}
	for _, tag := range p.groupKeys {
		item := p.groups[tag]
		for _, ref := range item.declared {
			if _, exists := roles[ref.nodeID]; !exists {
				declaredIDs = append(declaredIDs, ref.nodeID)
			}
			roles[ref.nodeID] = append(roles[ref.nodeID], ref.role)
		}
		for _, ref := range item.claims {
			claimed[ref.nodeID] = true
			if !item.present {
				continue
			}
			if _, exists := roles[ref.nodeID]; !exists {
				edgeOnly = append(edgeOnly, ref.nodeID)
				continue
			}
			if !migrationRoleCovered(roles[ref.nodeID], ref.role) {
				edgeOnly = append(edgeOnly, ref.nodeID)
			}
		}
	}
	for _, id := range declaredIDs {
		if !claimed[id] {
			fieldOnly = append(fieldOnly, id)
		}
	}
	for _, mode := range []string{"image", "video"} {
		output := p.outputs[mode]
		if output == nil || len(output.claims) == 0 || output.declared == "" {
			continue
		}
		targets := sortedUnique(output.claims)
		if len(targets) == 1 && targets[0] == output.declared {
			continue
		}
		fieldOnly = append(fieldOnly, output.declared)
		for _, target := range targets {
			if target != output.declared {
				edgeOnly = append(edgeOnly, target)
			}
		}
	}
	return sortedUnique(fieldOnly), sortedUnique(edgeOnly)
}

// migrationRoleCovered 判断字段声明是否已经覆盖一条连线引用。字段把节点声明为首帧、
// 尾帧或遮罩时，普通连线不再产生额外引用，也不会据此推断角色。
func migrationRoleCovered(declared []string, claim string) bool {
	for _, role := range declared {
		if role == claim {
			return true
		}
		switch role {
		case "first-frame", "last-frame", "mask":
			return true
		}
	}
	return false
}

func applyMigrationPlan(doc map[string]any, plan *migrationPlan) error {
	idx := index(doc)
	node := idx[plan.nodeID]
	if node == nil {
		return fail("reference_missing", "nodeId", "引用所属节点不存在")
	}
	switch plan.kind {
	case "storyboard-row":
		row := rowFor(node, "storyboard", plan.rowID)
		if row == nil {
			return fail("reference_missing", "rowId", "分镜行不存在")
		}
		if item := plan.groups[""]; item != nil && len(item.claims) > 0 && (!item.present || plan.choice == MigrationChoiceConnections) {
			row["assetBindings"] = storyboardBindingValues(item.claims)
		}
		for _, mode := range []string{"image", "video"} {
			output := plan.outputs[mode]
			if output == nil || len(output.claims) == 0 {
				continue
			}
			if output.declared != "" && plan.choice != MigrationChoiceConnections {
				continue
			}
			// 同一输出槽位有多条历史连线时以最后一条为准。
			row[mode+"NodeId"] = output.claims[len(output.claims)-1]
		}
	case "storyboard-context":
		item := plan.groups[""]
		if item == nil || len(item.claims) == 0 || (item.present && plan.choice != MigrationChoiceConnections) {
			return nil
		}
		storyboard := obj(meta(node)["storyboard"])
		storyboard["referenceNodeIds"] = nodeIDValues(item.claims)
		metadata := meta(node)
		metadata["storyboard"] = storyboard
		node["metadata"] = metadata
	case "batch-row":
		row := rowFor(node, "batchTable", plan.rowID)
		if row == nil {
			return fail("reference_missing", "rowId", "批量表行不存在")
		}
		for _, tag := range plan.groupKeys {
			item := plan.groups[tag]
			if item == nil || len(item.claims) == 0 {
				continue
			}
			key := "inputNodeIds"
			if tag == "text" {
				key = "textNodeIds"
			}
			if !item.present {
				row[key] = nodeIDValues(item.claims)
				continue
			}
			if plan.choice != MigrationChoiceConnections {
				continue
			}
			// 连线只解释它所在列，不删除该行已有的手工输入，只补齐缺失项。
			merged := stringsList(row[key])
			seen := map[string]bool{}
			for _, id := range merged {
				seen[id] = true
			}
			for _, claim := range item.claims {
				if seen[claim.nodeID] {
					continue
				}
				seen[claim.nodeID] = true
				merged = append(merged, claim.nodeID)
			}
			row[key] = stringValues(merged)
		}
	case "generation", "node":
		item := plan.groups[""]
		if item == nil || len(item.claims) == 0 {
			return nil
		}
		metadata := meta(node)
		if plan.kind == "generation" {
			spec := plan.spec
			spec.ReferenceBindings = referenceBindings(item.claims)
			if err := spec.Validate(); err != nil {
				return fail("invalid_reference", "generationSpec", err.Error())
			}
			stored, err := storedGenerationSpec(spec)
			if err != nil {
				return err
			}
			metadata["generationSpec"] = stored
		} else {
			metadata["inputBindings"] = bindingValues(item.claims)
		}
		node["metadata"] = metadata
	}
	return nil
}

func (p *migrationPlan) claimedEdges() []string {
	out := []string{}
	for _, tag := range p.groupKeys {
		if item := p.groups[tag]; item != nil {
			out = append(out, item.edges...)
		}
	}
	for _, mode := range []string{"image", "video"} {
		if output := p.outputs[mode]; output != nil {
			out = append(out, output.edges...)
		}
	}
	return out
}

// mediaScopeKind 判断节点承载引用的权威字段：已有生成合同的节点用 generationSpec，
// 其余可接收输入的节点用 inputBindings。
func mediaScopeKind(registry *capability.Registry, node map[string]any) string {
	d, ok := descriptor(registry, node)
	if !ok || !d.Connection.CanTarget {
		return ""
	}
	if _, exists := d.PatchFields["generationSpec"]; exists {
		return "generation"
	}
	if _, exists := d.PatchFields["inputBindings"]; exists {
		return "node"
	}
	return ""
}

// keyframeOutputs 返回已声明为关键帧首帧的产物节点到首帧节点的映射：只有字段声明的
// 首帧才会让普通连线确认成 first-frame，模式不明时不做猜测。
func keyframeOutputs(nodes []map[string]any) map[string]string {
	out := map[string]string{}
	for _, node := range nodes {
		if str(node["type"]) != "script" || str(meta(node)["storyboardVideoInputMode"]) != "keyframe" {
			continue
		}
		for _, row := range maps(obj(meta(node)["storyboard"])["rows"]) {
			frame, output := str(row["imageNodeId"]), str(row["videoNodeId"])
			if frame == "" || output == "" {
				continue
			}
			out[output] = frame
		}
	}
	return out
}

// storyboardAssetRole 对应画布上的素材角色推断；没有素材角色语义的节点不能作为行素材。
func storyboardAssetRole(node map[string]any) (string, bool) {
	metadata := meta(node)
	switch {
	case str(metadata["workflowKind"]) == "character", str(metadata["assetCategory"]) == "character":
		return "character", true
	case str(node["type"]) == "audio":
		return "audio", true
	case str(node["type"]) == "video":
		return "motion", true
	case str(metadata["assetCategory"]) == "environment", str(metadata["assetCategory"]) == "prop":
		return str(metadata["assetCategory"]), true
	case str(node["type"]) == "image", str(node["type"]) == "drawing":
		return "style", true
	}
	return "", false
}

func storyboardRoleMatches(role, media string) bool {
	switch role {
	case "character", "environment", "wardrobe", "prop", "weapon", "style":
		return media == "image"
	case "motion":
		return media == "image" || media == "video"
	case "audio":
		return media == "audio"
	}
	return false
}

func defaultRolePriority(role string) int {
	switch role {
	case "character":
		return 100
	case "environment":
		return 90
	case "prop", "weapon", "wardrobe":
		return 80
	case "motion", "audio":
		return 70
	}
	return 60
}

// legacyGenerationSpec 把历史扁平元数据转换成正式生成合同，规则与画布兼容层
// readNodeGenerationSpec 一致；无法确认的字段跳过而不是猜测。
func legacyGenerationSpec(node map[string]any) (contract.GenerationSpec, error) {
	metadata := meta(node)
	mode := str(node["type"])
	switch mode {
	case "image", "video", "audio":
	case "config":
		mode = "image"
		if value := str(metadata["generationMode"]); value == "video" || value == "audio" {
			mode = value
		}
	default:
		return contract.GenerationSpec{}, fail("invalid_reference", "generationSpec", "节点不支持生成合同")
	}
	prompt := ""
	if value, exists := metadata["composerContent"]; exists {
		prompt = str(value)
	} else if value, exists := metadata["prompt"]; exists {
		prompt = str(value)
	}
	spec := contract.GenerationSpec{
		Version: contract.GenerationVersion, Mode: mode, Prompt: prompt,
		Options: legacyOptions(mode, metadata), ReferenceBindings: []contract.ReferenceBinding{}, TextInputMode: "append-sources",
	}
	if mode == "video" {
		spec.TextInputMode = "prompt-only"
	}
	if selection := legacyModelSelection(metadata); selection != nil {
		spec.ModelSelection = selection
	}
	if err := spec.Validate(); err != nil {
		return spec, fail("invalid_reference", "generationSpec", err.Error())
	}
	return spec, nil
}

func legacyOptions(mode string, metadata map[string]any) contract.Options {
	options := contract.Options{}
	value := reflect.ValueOf(&options).Elem()
	typ := reflect.TypeOf(options)
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !strings.Contains(","+field.Tag.Get("modes")+",", ","+mode+",") {
			continue
		}
		raw, exists := metadata[field.Tag.Get("node")]
		if !exists || raw == nil || fmt.Sprint(raw) == "" {
			continue
		}
		converted, ok := legacyOptionValue(field.Type.Elem().Kind(), raw)
		if !ok {
			continue
		}
		pointer := reflect.New(field.Type.Elem())
		switch field.Type.Elem().Kind() {
		case reflect.String:
			pointer.Elem().SetString(converted.(string))
		case reflect.Bool:
			pointer.Elem().SetBool(converted.(bool))
		case reflect.Int:
			pointer.Elem().SetInt(int64(converted.(int)))
		default:
			continue
		}
		value.Field(index).Set(pointer)
	}
	return options
}

func legacyOptionValue(kind reflect.Kind, raw any) (any, bool) {
	switch kind {
	case reflect.String:
		switch current := raw.(type) {
		case string:
			return current, true
		case float64, bool:
			return fmt.Sprint(current), true
		}
	case reflect.Bool:
		switch current := raw.(type) {
		case bool:
			return current, true
		case string:
			if parsed, err := strconv.ParseBool(strings.TrimSpace(current)); err == nil {
				return parsed, true
			}
		}
	case reflect.Int:
		switch current := raw.(type) {
		case int:
			return current, true
		case float64:
			return int(current), true
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(current)); err == nil {
				return parsed, true
			}
		}
	}
	return nil, false
}

func legacyModelSelection(metadata map[string]any) *contract.ModelSelection {
	if id := strings.TrimSpace(str(metadata["logicalModelId"])); id != "" {
		return &contract.ModelSelection{Kind: "logical", LogicalModelID: id}
	}
	if id, key := strings.TrimSpace(str(metadata["channelId"])), strings.TrimSpace(str(metadata["channelModelKey"])); id != "" && key != "" {
		return &contract.ModelSelection{Kind: "channel", ChannelID: id, ModelKey: key}
	}
	if id, key, ok := strings.Cut(strings.TrimSpace(str(metadata["model"])), "::"); ok {
		id, key = strings.TrimSpace(id), strings.TrimSpace(key)
		if id != "" && key != "" {
			return &contract.ModelSelection{Kind: "channel", ChannelID: id, ModelKey: key}
		}
	}
	return nil
}

func storedGenerationSpec(spec contract.GenerationSpec) (map[string]any, error) {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	stored := map[string]any{}
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, err
	}
	return stored, nil
}

func storyboardBindingValues(claims []migrationRef) []any {
	out := []any{}
	seen := map[string]bool{}
	for _, claim := range claims {
		if seen[claim.nodeID] {
			continue
		}
		seen[claim.nodeID] = true
		out = append(out, map[string]any{"nodeId": claim.nodeID, "role": claim.role, "priority": float64(defaultRolePriority(claim.role))})
	}
	return out
}

func bindingValues(claims []migrationRef) []any {
	out := []any{}
	seen := map[string]bool{}
	for _, claim := range claims {
		id := bindingID(claim.nodeID, claim.role)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, map[string]any{"id": id, "nodeId": claim.nodeID, "mediaType": claim.media, "role": claim.role, "order": len(out), "resolution": "latest"})
	}
	return out
}

func referenceBindings(claims []migrationRef) []contract.ReferenceBinding {
	out := []contract.ReferenceBinding{}
	seen := map[string]bool{}
	for _, claim := range claims {
		id := bindingID(claim.nodeID, claim.role)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, contract.ReferenceBinding{ID: id, NodeID: claim.nodeID, MediaType: claim.media, Role: claim.role, Order: len(out), Resolution: "latest"})
	}
	return out
}

func nodeIDValues(claims []migrationRef) []any {
	ids := []string{}
	seen := map[string]bool{}
	for _, claim := range claims {
		if seen[claim.nodeID] {
			continue
		}
		seen[claim.nodeID] = true
		ids = append(ids, claim.nodeID)
	}
	return stringValues(ids)
}

func stringValues(values []string) []any {
	out := []any{}
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func sortedUnique(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
