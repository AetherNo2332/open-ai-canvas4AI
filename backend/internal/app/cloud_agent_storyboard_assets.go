package app

import (
	"encoding/json"
	"reflect"

	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/references"
)

func cloudAgentStoryboardAssetAction(action string) bool {
	switch action {
	case "set_assets", "add_assets", "remove_assets", "upsert_assets":
		return true
	}
	return false
}

func cloudAgentStoryboardSharedAction(action string) bool {
	switch action {
	case "set_references", "add_references", "remove_references":
		return true
	}
	return false
}

func cloudAgentStoryboardLegacyAssetNode(doc, character map[string]any) string {
	if id := stringValue(character["characterImageNodeId"]); id != "" {
		return id
	}
	assetID := stringValue(character["characterAssetId"])
	if assetID == "" {
		return ""
	}
	id := ""
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(cloudAgentNodeMetadata(node)["characterAssetId"]) == assetID {
			if id != "" {
				return ""
			}
			id = stringValue(node["id"])
		}
	}
	return id
}

func cloudAgentStoryboardAssetDelta(doc, row map[string]any, requested []map[string]any, action string) error {
	bindings := append([]map[string]any{}, creationMaps(row["assetBindings"])...)
	characters := append([]map[string]any{}, creationMaps(row["characters"])...)
	for _, asset := range requested {
		id, role := stringValue(asset["nodeId"]), stringValue(asset["role"])
		// Binding maps use nodeId rather than the row id consumed by nodeIndex.
		index := -1
		for i, old := range bindings {
			if stringValue(old["nodeId"]) == id {
				index = i
				break
			}
		}
		switch action {
		case "add_assets":
			if index >= 0 {
				if stringValue(bindings[index]["role"]) != role {
					return cloudAgentFieldError("assets", "asset_role_conflict", "资产已有其他用途；修改用途请使用 upsert_assets")
				}
				continue
			}
			legacy := false
			for _, c := range characters {
				legacy = legacy || (role == "character" && cloudAgentStoryboardLegacyAssetNode(doc, c) == id)
			}
			if !legacy {
				bindings = append(bindings, asset)
			}
		case "upsert_assets":
			if index >= 0 {
				bindings[index] = asset
			} else {
				bindings = append(bindings, asset)
			}
		case "remove_assets":
			if index >= 0 && stringValue(bindings[index]["role"]) == role {
				bindings = append(bindings[:index], bindings[index+1:]...)
			}
		}
		if action == "upsert_assets" || (action == "remove_assets" && role == "character") {
			kept := []map[string]any{}
			for _, c := range characters {
				if cloudAgentStoryboardLegacyAssetNode(doc, c) != id {
					kept = append(kept, c)
				}
			}
			characters = kept
		}
	}
	row["assetBindings"], row["characters"] = mapsAsAny(bindings), mapsAsAny(characters)
	if err := capability.ValidateEditableFields(capability.StoryboardRowFields(), map[string]any{"assetBindings": row["assetBindings"]}); err != nil {
		return BadAuthRequest(err.Error())
	}
	if err := validateCloudAgentStoryboardReferences(doc, row); err != nil {
		return err
	}
	return nil
}

func cloudAgentStoryboardDeclaredAssets(doc, row map[string]any) []map[string]any {
	assets := []map[string]any{}
	seen := map[string]bool{}
	add := func(id, role string) {
		key := id + ":" + role
		if id != "" && !seen[key] {
			assets = append(assets, map[string]any{"nodeId": id, "role": role})
			seen[key] = true
		}
	}
	for _, a := range creationMaps(row["assetBindings"]) {
		add(stringValue(a["nodeId"]), stringValue(a["role"]))
	}
	for _, c := range creationMaps(row["characters"]) {
		add(cloudAgentStoryboardLegacyAssetNode(doc, c), "character")
	}
	return assets
}

func cloudAgentStoryboardAssetChanges(beforeDoc, doc map[string]any, nodeID string, beforeRows, rows []map[string]any, shared []string) ([]any, error) {
	changes := []any{}
	detailBudget := 8 << 10
	for _, row := range rows {
		id := stringValue(row["id"])
		i := cloudAgentNodeIndex(beforeRows, id)
		if i < 0 {
			continue
		}
		old := beforeRows[i]
		if reflect.DeepEqual(old["assetBindings"], row["assetBindings"]) && reflect.DeepEqual(old["characters"], row["characters"]) {
			continue
		}
		before, after := cloudAgentStoryboardDeclaredAssets(beforeDoc, old), cloudAgentStoryboardDeclaredAssets(doc, row)
		added, removed, retained := []any{}, []any{}, []any{}
		contains := func(all []map[string]any, target map[string]any) bool {
			for _, a := range all {
				if reflect.DeepEqual(a, target) {
					return true
				}
			}
			return false
		}
		for _, a := range before {
			if contains(after, a) {
				retained = append(retained, a)
			} else {
				removed = append(removed, a)
			}
		}
		for _, a := range after {
			if !contains(before, a) {
				added = append(added, a)
			}
		}
		resolved, err := references.Resolve(doc, references.Scope{Kind: "storyboard-row", NodeID: nodeID, RowID: id}, capability.BuiltinRegistry())
		if err != nil {
			return nil, cloudAgentReferenceError(err)
		}
		detail := map[string]any{"scope": "row", "rowId": id, "addedCount": len(added), "removedCount": len(removed), "retainedCount": len(retained), "sharedCount": len(shared), "effectiveInputCount": len(resolved.Bindings)}
		full := map[string]any{}
		for key, value := range detail {
			full[key] = value
		}
		full["added"], full["removed"], full["retained"], full["sharedNodeIds"], full["effectiveBindings"] = added, removed, retained, shared, resolved.Bindings
		raw, err := json.Marshal(full)
		if err != nil {
			return nil, err
		}
		if len(raw) <= detailBudget {
			changes = append(changes, full)
			detailBudget -= len(raw)
		} else {
			detail["detailsOmitted"] = true
			changes = append(changes, detail)
		}
	}
	return changes, nil
}
