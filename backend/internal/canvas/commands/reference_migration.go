package commands

import (
	"sort"
	"strings"

	"yingce/backend/internal/canvas/references"
)

// OperationMigrateReferences 是仅画布客户端可用的一次性规范化入口：它把历史画布的
// 字段与旧连线矛盾按 scopeChoices 定案，然后写入权威字段、托管连线和
// referenceFieldsVersion=1。云端 Agent 的 canvas_apply_ops 是 app 侧独立分派，
// 既不认识这个操作，也没有对应的预览入口；不要在 Agent 合同里登记它。
const OperationMigrateReferences = "migrate_references"

// referenceMigrationRequired：文档尚未规范化，编辑或生成必须先迁移。
func referenceMigrationRequired() error {
	return &Error{Reason: "reference_migration_required", Message: "历史画布需要先完成一次引用规范化再编辑或生成"}
}

// referenceMigrationNotRequired：文档已经使用声明式引用，重复规范化会平白推进修订，
// 因此明确拒绝而不是返回空变更回执。
func referenceMigrationNotRequired() error {
	return &Error{Reason: "reference_migration_not_required", Message: "画布已经使用声明式引用，无需再次规范化"}
}

// referenceMigrationConflict：字段与旧连线仍有未决矛盾。冲突明细来自画布读取投影
// （Snapshot.ReferenceMigration），迁移本身绝不做静默合并。
func referenceMigrationConflict(conflicts []references.MigrationConflict) error {
	keys := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		keys = append(keys, conflict.ScopeKey)
	}
	sort.Strings(keys)
	if len(keys) > 5 {
		keys = append(keys[:5], "…")
	}
	return &Error{Reason: "reference_migration_conflict", Message: "历史画布的引用仍有未决冲突，请先为每个作用域选择 fields 或 connections：" + strings.Join(keys, "、")}
}

// referenceMigrationChoices 只接受 patch.scopeChoices：作用域标识到 fields／
// connections 的映射。未知 patch 字段直接拒绝，避免客户端以为某个选项生效了。
func referenceMigrationChoices(patch map[string]any) (map[string]string, error) {
	choices := map[string]string{}
	if len(patch) == 0 {
		return choices, nil
	}
	if len(patch) != 1 || patch["scopeChoices"] == nil {
		return nil, canvasFieldError("scopeChoices", "invalid_operation", "迁移命令只接受 patch.scopeChoices")
	}
	values := map[string]any{}
	switch typed := patch["scopeChoices"].(type) {
	case map[string]any:
		values = typed
	case map[string]string:
		for key, value := range typed {
			values[key] = value
		}
	default:
		return nil, canvasFieldError("scopeChoices", "invalid_operation", "scopeChoices 必须是作用域到来源的映射")
	}
	for key, value := range values {
		choice, isString := value.(string)
		key = strings.TrimSpace(key)
		if key == "" || !isString || (choice != references.MigrationChoiceFields && choice != references.MigrationChoiceConnections) {
			return nil, canvasFieldError("scopeChoices", "invalid_reference", "作用域来源只能是 fields 或 connections")
		}
		choices[key] = choice
	}
	return choices, nil
}

// migrateReferenceFields 在规划器内完成一次规范化，并返回迁移回执明细。
func (p *planner) migrateReferenceFields(doc Document, op Operation) ([]OperationSummary, error) {
	if op.ID != text(doc["id"]) {
		return nil, BadAuthRequest("画布目标 ID 无效")
	}
	if references.Enabled(doc) {
		return nil, referenceMigrationNotRequired()
	}
	choices, err := referenceMigrationChoices(op.Patch)
	if err != nil {
		return nil, err
	}
	result, err := references.NormalizeLegacy(doc, choices, p.registry, newID)
	if err != nil {
		if typed, ok := err.(*references.Error); ok {
			return nil, canvasFieldError(typed.Field, typed.Reason, typed.Message)
		}
		return nil, err
	}
	if len(result.Conflicts) > 0 {
		return nil, referenceMigrationConflict(result.Conflicts)
	}
	for key := range doc {
		delete(doc, key)
	}
	for key, value := range result.Document {
		doc[key] = value
	}
	keys := make([]string, 0, len(choices))
	for key := range choices {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	details := []string{}
	for _, key := range keys {
		details = append(details, key+"="+choices[key])
	}
	return []OperationSummary{{Operation: OperationMigrateReferences, NodeID: op.ID, Summary: "历史画布已完成一次引用规范化", Details: details}}, nil
}

// referenceMigrationGate 要求历史画布先完成一次规范化：旧文档可以读取、预览和导出，
// 但改动引用内容的编辑与生成必须建立在权威字段之上。它放在 Service 的写入口而不是
// 纯规划器 Plan 里：Plan 是校验与差异计算，测试和 Create 都会直接调用它；门禁属于
// 生命周期策略，和权限、活跃任务占用一样由 service 执行。注意三点：
//   - migrate_references 自身必须放行，否则迁移会被自己的门禁挡住；
//   - 改名、删除画布和画布外观设置不改变引用内容，放行，避免旧画布无法重命名或删除；
//   - 门禁只作用于客户端写路径与生成提交，不作用于任务结果回写（见 host_mutation.go）。
func referenceMigrationGate(doc Document, ops []Operation) error {
	if references.Enabled(doc) {
		return nil
	}
	content := false
	for _, op := range ops {
		switch op.Type {
		case OperationMigrateReferences, "rename_canvas", "delete_canvas", "edit_canvas_settings":
		default:
			content = true
		}
	}
	if !content {
		return nil
	}
	if len(ops) != 1 || ops[0].Type != OperationMigrateReferences {
		return referenceMigrationRequired()
	}
	return nil
}
