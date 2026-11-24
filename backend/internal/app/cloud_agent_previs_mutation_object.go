package app

import (
	"fmt"
)

func cloudAgentPrevisApplyObjectPatch(objects *[]map[string]any, operation cloudAgentPrevisPatchOperation, index int) (cloudAgentApprovalPreviewItem, error) {
	items := *objects
	byID, err := cloudAgentPrevisIDSet(items, "导演对象 ID")
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := validateCloudAgentID(operation.ID, fmt.Sprintf("operations[%d].id", index), 80); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	existing := byID[operation.ID]
	summary, addedTitle := "", ""
	switch operation.Type {
	case "object_add":
		if existing != nil {
			return cloudAgentApprovalPreviewItem{}, BadAuthRequest("新增对象 ID 已存在")
		}
		item, title, result, err := cloudAgentPrevisCreateObject(items, operation)
		if err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		items = append(items, item)
		addedTitle, summary = title, result
	case "object_update":
		if existing == nil {
			return cloudAgentApprovalPreviewItem{}, BadAuthRequest("要更新的对象不存在")
		}
		summary, err = cloudAgentPrevisUpdateObject(existing, operation)
		if err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
	case "object_remove":
		if existing == nil {
			return cloudAgentApprovalPreviewItem{}, BadAuthRequest("要删除的对象不存在")
		}
		for _, candidate := range items {
			if stringValue(candidate["parentId"]) == operation.ID {
				return cloudAgentApprovalPreviewItem{}, BadAuthRequest("存在子对象时不能删除父对象")
			}
		}
		for i, candidate := range items {
			if stringValue(candidate["id"]) == operation.ID {
				items = append(items[:i], items[i+1:]...)
				break
			}
		}
		summary = fmt.Sprintf("删除对象《%s》", stringValue(existing["name"]))
	default:
		return cloudAgentApprovalPreviewItem{}, nil
	}
	*objects = items
	nodeTitle := stringValue(existing["name"])
	if nodeTitle == "" && addedTitle != "" {
		nodeTitle = addedTitle
	}
	if nodeTitle == "" && operation.Type == "object_add" {
		nodeTitle = "新对象"
		if operation.Name != nil {
			nodeTitle = *operation.Name
		}
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: truncateRunes(nodeTitle, cloudAgentPrevisTextLimit), Summary: summary}, nil
}

func cloudAgentPrevisCreateObject(items []map[string]any, operation cloudAgentPrevisPatchOperation) (map[string]any, string, string, error) {
	if operation.Kind == "prop" || operation.Kind == "layout" {
		return cloudAgentPrevisCreateCatalogObject(operation)
	}
	return cloudAgentPrevisCreateBasicObject(items, operation)
}

func cloudAgentPrevisCreateCatalogObject(operation cloudAgentPrevisPatchOperation) (map[string]any, string, string, error) {
	summary := ""
	position, rotation, scale := []float64{0, 0, 0}, []float64{0, 0, 0}, []float64{1, 1, 1}
	if operation.Position != nil {
		position = *operation.Position
	}
	if operation.Rotation != nil {
		rotation = *operation.Rotation
	}
	if operation.Scale != nil {
		scale = *operation.Scale
	}
	for label, value := range map[string][]float64{"position": position, "rotation": rotation, "scale": scale} {
		if err := cloudAgentPrevisValidateVec3(value, label); err != nil {
			return nil, "", "", err
		}
	}
	if operation.Color != nil {
		if err := cloudAgentPrevisValidateColor(*operation.Color, "对象颜色"); err != nil {
			return nil, "", "", err
		}
	}
	name := ""
	if operation.Name != nil {
		name = *operation.Name
	}
	item, title, err := cloudAgentPrevisAddCatalogObject(operation, name, position, rotation, scale)
	if err != nil {
		return nil, "", "", err
	}
	if operation.Kind == "layout" {
		summary = fmt.Sprintf("搭建布景《%s》", title)
	} else {
		summary = fmt.Sprintf("新增道具《%s》", title)
	}
	return item, title, summary, nil
}

func cloudAgentPrevisCreateBasicObject(items []map[string]any, operation cloudAgentPrevisPatchOperation) (map[string]any, string, string, error) {
	if operation.Kind != "primitive" && operation.Kind != "actor" {
		return nil, "", "", BadAuthRequest("新增对象 kind 只支持 prop、layout、actor 或 primitive")
	}
	primitive := operation.Primitive
	if primitive == "" {
		primitive = "box"
	}
	if primitive != "box" && primitive != "sphere" && primitive != "cylinder" && primitive != "plane" && primitive != "character" {
		return nil, "", "", BadAuthRequest("新增对象 primitive 不受支持")
	}
	name := "新对象"
	if operation.Name != nil {
		name = *operation.Name
	}
	if err := cloudAgentPrevisValidateText(name, "对象名称", cloudAgentPrevisTextLimit, true); err != nil {
		return nil, "", "", err
	}
	position, rotation, scale, err := cloudAgentPrevisBasicObjectFields(operation, primitive)
	if err != nil {
		return nil, "", "", err
	}
	defaultColor := cloudAgentPrevisObjectColor(items, operation.Kind)
	item := map[string]any{"id": operation.ID, "name": name, "kind": operation.Kind, "primitive": primitive, "transform": map[string]any{"position": cloudAgentPrevisVec3(position), "rotation": cloudAgentPrevisVec3(rotation), "scale": cloudAgentPrevisVec3(scale)}, "color": defaultColor, "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}}
	if err := cloudAgentPrevisApplyBasicObjectProperties(item, operation); err != nil {
		return nil, "", "", err
	}
	return item, "", fmt.Sprintf("新增对象《%s》", name), nil
}

func cloudAgentPrevisApplyBasicObjectProperties(item map[string]any, operation cloudAgentPrevisPatchOperation) error {
	if operation.Color != nil {
		item["color"] = *operation.Color
	}
	if operation.Visible != nil {
		item["visible"] = *operation.Visible
	}
	if operation.Pose != "" {
		item["pose"] = operation.Pose
	}
	if operation.Kind == "actor" && operation.Pose == "" {
		item["pose"] = "stand"
	}
	if operation.Kind == "actor" {
		item["archetype"] = "adult"
		item["boneTracks"] = []any{}
		item["boneOverrides"] = map[string]any{}
		item["motionClips"] = []any{}
	}
	if operation.Archetype != "" {
		validArchetypes := map[string]bool{"adult": true, "child": true, "elderly": true, "man": true, "woman": true, "monster": true}
		if !validArchetypes[operation.Archetype] {
			return BadAuthRequest("archetype 值不受支持")
		}
		item["archetype"] = operation.Archetype
	}
	if operation.ParentID != nil && *operation.ParentID != "" {
		item["parentId"] = *operation.ParentID
	}
	if err := cloudAgentPrevisApplyCharacterBinding(item, operation); err != nil {
		return err
	}
	return nil
}

func cloudAgentPrevisBasicObjectFields(operation cloudAgentPrevisPatchOperation, primitive string) (position, rotation, scale []float64, err error) {
	position, rotation, scale = []float64{0, 0.5, 0}, []float64{0, 0, 0}, []float64{1, 1, 1}
	position = cloudAgentPrevisDefaultPosition(operation.Kind, primitive, position)
	if operation.Position != nil {
		position = *operation.Position
	}
	if operation.Rotation != nil {
		rotation = *operation.Rotation
	}
	if operation.Scale != nil {
		scale = *operation.Scale
	}
	if err := cloudAgentPrevisValidateVec3(position, "position"); err != nil {
		return nil, nil, nil, err
	}
	if err := cloudAgentPrevisValidateVec3(rotation, "rotation"); err != nil {
		return nil, nil, nil, err
	}
	if err := cloudAgentPrevisValidateVec3(scale, "scale"); err != nil {
		return nil, nil, nil, err
	}
	if operation.Color != nil {
		if err := cloudAgentPrevisValidateColor(*operation.Color, "对象颜色"); err != nil {
			return nil, nil, nil, err
		}
	}
	if operation.Pose != "" {
		if err := cloudAgentPrevisValidatePose(operation.Pose); err != nil {
			return nil, nil, nil, err
		}
	}
	if operation.ParentID != nil && *operation.ParentID != "" {
		if err := validateCloudAgentID(*operation.ParentID, "对象 parentId", 80); err != nil {
			return nil, nil, nil, err
		}
	}
	return position, rotation, scale, nil
}

func cloudAgentPrevisUpdateObject(existing map[string]any, operation cloudAgentPrevisPatchOperation) (string, error) {
	if err := cloudAgentPrevisApplyName(existing, operation.Name, "对象名称"); err != nil {
		return "", err
	}
	if operation.Color != nil {
		if err := cloudAgentPrevisValidateColor(*operation.Color, "对象颜色"); err != nil {
			return "", err
		}
		existing["color"] = *operation.Color
	}
	if operation.Visible != nil {
		existing["visible"] = *operation.Visible
	}
	if operation.Pose != "" {
		if err := cloudAgentPrevisValidatePose(operation.Pose); err != nil {
			return "", err
		}
		existing["pose"] = operation.Pose
	}
	if operation.ParentID != nil {
		if *operation.ParentID == "" {
			delete(existing, "parentId")
		} else {
			if err := validateCloudAgentID(*operation.ParentID, "对象 parentId", 80); err != nil {
				return "", err
			}
			existing["parentId"] = *operation.ParentID
		}
	}
	if err := cloudAgentPrevisApplyCharacterBinding(existing, operation); err != nil {
		return "", err
	}
	if err := cloudAgentPrevisUpdateCatalogFields(existing, operation); err != nil {
		return "", err
	}
	if err := cloudAgentPrevisApplyTransform(existing, operation); err != nil {
		return "", err
	}
	return fmt.Sprintf("修改对象《%s》", stringValue(existing["name"])), nil
}

func cloudAgentPrevisUpdateCatalogFields(existing map[string]any, operation cloudAgentPrevisPatchOperation) error {
	if operation.Params != nil {
		if stringValue(existing["kind"]) != "prop" {
			return BadAuthRequest("params 只适用于 kind=prop 的参数化组件")
		}
		base, _ := existing["params"].(map[string]any)
		params, err := cloudAgentPrevisNormalizeParams(stringValue(existing["propId"]), base, operation.Params)
		if err != nil {
			return err
		}
		existing["params"] = params
	}
	if operation.Variant != nil {
		if stringValue(existing["kind"]) != "prop" || *operation.Variant < 0 || *operation.Variant > 7 {
			return BadAuthRequest("variant 只适用于目录道具，范围 0-7")
		}
		existing["variant"] = *operation.Variant
	}
	if operation.LayoutTemplate != "" || operation.Seed != nil || operation.Size != nil || operation.Density != nil || operation.Crowd != nil {
		layout, _ := existing["layout"].(map[string]any)
		if stringValue(existing["kind"]) != "layout" || layout == nil {
			return BadAuthRequest("布景参数只适用于 kind=layout 对象")
		}
		next := make(map[string]any, len(layout))
		for key, value := range layout {
			next[key] = value
		}
		cloudAgentPrevisMergeLayout(next, operation)
		// 参数变化后实例序号重排，已拆出的序号失效。
		delete(next, "removed")
		if err := cloudAgentPrevisValidateLayout(next); err != nil {
			return err
		}
		existing["layout"] = next
	}
	return nil
}

func cloudAgentPrevisFindByID(items []map[string]any, id string) map[string]any {
	for _, item := range items {
		if stringValue(item["id"]) == id {
			return item
		}
	}
	return nil
}
