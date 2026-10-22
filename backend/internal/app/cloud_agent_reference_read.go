package app

import (
	"infinite-canvas/backend/internal/canvas/references"
	"infinite-canvas/backend/internal/repository"
)

type cloudAgentReferenceReader struct {
	repo             *repository.Repository
	userID, canvasID string
	doc              map[string]any
	nodes            map[string]map[string]any
	availability     map[string]map[string]any
}

func newCloudAgentReferenceReader(repo *repository.Repository, userID, canvasID string, doc map[string]any) *cloudAgentReferenceReader {
	r := &cloudAgentReferenceReader{repo: repo, userID: userID, canvasID: canvasID, doc: doc, nodes: map[string]map[string]any{}, availability: map[string]map[string]any{}}
	for _, node := range creationMaps(doc["nodes"]) {
		r.nodes[stringValue(node["id"])] = node
	}
	return r
}

func (r *cloudAgentReferenceReader) nodeAvailability(id, mediaType string) map[string]any {
	key := id + ":" + mediaType
	if value, exists := r.availability[key]; exists {
		return value
	}
	value := map[string]any{"referenceReady": false}
	r.availability[key] = value
	node := r.nodes[id]
	if node == nil {
		value["issue"] = "引用节点不存在"
		return value
	}
	if descriptor, ok := cloudAgentNodeCapabilityForNode(node); ok {
		value["kind"] = descriptor.Type
	}
	value["usageStatus"] = cloudAgentNodeUsageStatus(node)
	if mediaType == "text" {
		value["referenceReady"] = true
		return value
	}
	if r.repo == nil {
		delete(value, "referenceReady")
		value["availability"] = "unknown"
		return value
	}
	if cloudAgentCharacterNode(node) {
		character, err := cloudAgentResolveCharacter(r.repo, r.userID, "", node)
		if err != nil {
			value["issue"] = cloudAgentSafeToolError(err)
			return value
		}
		value["assetId"], value["versionId"], value["versionPolicy"] = character.AssetID, character.Card.VersionID, character.VersionPolicy
		if mediaType == "audio" {
			_, err = cloudAgentCharacterAudioReference(r.repo, r.userID, id, character)
		} else {
			_, err = cloudAgentCharacterImageReference(r.repo, r.userID, id, character)
		}
		value["referenceReady"] = err == nil
		if err != nil {
			value["issue"] = cloudAgentSafeToolError(err)
		}
		return value
	}
	_, _, err := cloudAgentReference(r.repo, r.userID, node)
	value["referenceReady"] = err == nil
	if err != nil {
		value["issue"] = cloudAgentSafeToolError(err)
	}
	return value
}

func (r *cloudAgentReferenceReader) resolve(scope references.Scope) map[string]any {
	view := map[string]any{"effectiveInputs": []any{}, "migrationRequired": !references.Enabled(r.doc)}
	resolution, err := references.Resolve(r.doc, scope, canvasCapabilityRegistry)
	if err != nil {
		view["state"] = "invalid"
		issue := map[string]any{"message": cloudAgentSafeToolError(err)}
		if typed, ok := err.(*references.Error); ok {
			issue["reason"], issue["field"] = typed.Reason, typed.Field
		}
		view["issue"] = issue
		return view
	}
	inputs := []any{}
	mediaCount, unavailable, unknown := 0, 0, 0
	for _, binding := range resolution.Bindings {
		input := map[string]any{"nodeId": binding.NodeID, "mediaType": binding.MediaType, "role": binding.Role, "order": binding.Order}
		if binding.MediaType != "text" {
			mediaCount++
		}
		if binding.NodeID != "" {
			for key, value := range r.nodeAvailability(binding.NodeID, binding.MediaType) {
				input[key] = value
			}
		} else if binding.ResourceID != "" && r.repo != nil {
			resource, readErr := r.repo.ResourceForUser(r.userID, binding.ResourceID)
			input["resourceId"], input["referenceReady"] = binding.ResourceID, readErr == nil && resource != nil && resource.Status == "ready"
		} else {
			input["availability"] = "unknown"
		}
		if input["referenceReady"] == false {
			unavailable++
		}
		if _, known := input["referenceReady"]; !known {
			unknown++
		}
		inputs = append(inputs, input)
	}
	view["effectiveInputs"], view["inputCount"], view["mediaInputCount"] = inputs, len(inputs), mediaCount
	view["unavailableInputs"], view["unknownInputs"], view["state"] = unavailable, unknown, "resolved"
	if !references.Enabled(r.doc) {
		view["state"] = "migration_required"
	}
	return view
}

func cloudAgentNodeReferenceView(repo *repository.Repository, userID, canvasID string, doc, node map[string]any) map[string]any {
	meta, _ := node["metadata"].(map[string]any)
	scope := references.Scope{Kind: "node", NodeID: stringValue(node["id"])}
	if meta["generationSpec"] != nil {
		scope.Kind = "generation"
	}
	if stringValue(node["type"]) == "script" {
		scope.Kind = "storyboard-context"
	}
	view := newCloudAgentReferenceReader(repo, userID, canvasID, doc).resolve(scope)
	if scope.Kind == "storyboard-context" {
		board, _ := meta["storyboard"].(map[string]any)
		view["referenceNodeIds"] = cloudAgentStrings(board["referenceNodeIds"])
		view["nextRead"] = map[string]any{"tool": "canvas_read_storyboard", "arguments": map[string]any{"nodeId": node["id"], "view": "bindings"}}
	}
	return view
}
