package app

import (
	"sort"
	"strings"

	"yingce/backend/internal/repository"
)

// Table scope deliberately includes all rows and actual declared inputs. A
// layout edit outside this dependency set cannot authorize stale row content.
func cloudAgentStoryboardRevision(repo *repository.Repository, userID, canvasID string, doc map[string]any, nodeID string) (string, error) {
	node, board, rows, err := storyboardNodeFromDocument(doc, nodeID)
	if err != nil {
		return "", err
	}
	ids := map[string]bool{nodeID: true}
	for _, id := range cloudAgentStrings(board["referenceNodeIds"]) {
		ids[id] = true
	}
	for _, row := range rows {
		for _, asset := range cloudAgentStoryboardDeclaredAssets(doc, row) {
			ids[stringValue(asset["nodeId"])] = true
		}
		for _, field := range []string{"imageNodeId", "videoNodeId"} {
			if id := stringValue(row[field]); id != "" {
				ids[id] = true
			}
		}
	}
	epoch, err := repo.CommandEpoch(userID, canvasID)
	epochID := ""
	generations := map[string]any{}
	if err == nil {
		epochID = epoch.Epoch
		versions, err := repo.CommandVersions(canvasID, epochID)
		if err != nil {
			return "", err
		}
		for id := range ids {
			version := versions["node:"+id]
			generations[id] = map[string]any{"generation": version.Generation, "deleted": version.Deleted}
		}
	} else if !repo.IsMissing(err) {
		return "", err
	}
	index := map[string]map[string]any{}
	for _, value := range creationMaps(doc["nodes"]) {
		index[stringValue(value["id"])] = value
	}
	index[nodeID] = node
	ordered := []string{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	dependencies := []any{}
	for _, id := range ordered {
		value := index[id]
		if value == nil {
			dependencies = append(dependencies, map[string]any{"id": id, "missing": true})
			continue
		}
		content := cloudAgentCanvasContent(map[string]any{"nodes": []any{value}})
		entity := creationMaps(content["nodes"])[0]
		for _, field := range []string{"position", "width", "height", "createdAt", "updatedAt"} {
			delete(entity, field)
		}
		dependency := map[string]any{"node": entity}
		if cloudAgentCharacterNode(value) {
			resolved, err := cloudAgentStoryboardCharacterVersion(repo, userID, value)
			if err != nil {
				return "", err
			}
			dependency["resolvedCharacter"] = resolved
		} else if key := stringValue(cloudAgentNodeMetadata(value)["storageKey"]); strings.HasPrefix(key, "resource:") {
			resource, err := cloudAgentStoryboardResourceVersion(repo, userID, strings.TrimPrefix(key, "resource:"))
			if err != nil {
				return "", err
			}
			dependency["resolvedResource"] = resource
		}
		dependencies = append(dependencies, dependency)
	}
	return "storyboard-v1:" + creationHash(map[string]any{"canvasId": canvasID, "epoch": epochID, "generations": generations, "referenceFieldsVersion": doc["referenceFieldsVersion"], "dependencies": dependencies}), nil
}

func cloudAgentStoryboardResourceVersion(repo *repository.Repository, userID, id string) (map[string]any, error) {
	resource, err := repo.ResourceForUser(userID, id)
	if repo.IsMissing(err) {
		return map[string]any{"id": id, "unavailable": true}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": resource.ID, "status": resource.Status, "mimeType": resource.MimeType, "bytes": resource.Size, "width": resource.Width, "height": resource.Height, "durationMs": resource.DurationMs}, nil
}

func cloudAgentStoryboardCharacterVersion(repo *repository.Repository, userID string, node map[string]any) (map[string]any, error) {
	meta := cloudAgentNodeMetadata(node)
	id := stringValue(meta["characterAssetId"])
	state := map[string]any{"assetId": id}
	asset, err := repo.UserCharacterAsset(userID, id)
	if repo.IsMissing(err) {
		state["unavailable"] = true
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	versionID := asset.PrimaryVersionID
	if stringValue(meta["characterVersionPolicy"]) == "pinned" {
		versionID = stringValue(meta["characterVersionId"])
	}
	state["versionId"], state["name"] = versionID, asset.Title
	character, err := cloudAgentResolveCharacter(repo, userID, "", node)
	if err != nil {
		if !cloudAgentToolErrorIsBusiness(err) {
			return nil, err
		}
		state["unavailable"], state["issue"] = true, cloudAgentSafeToolError(err)
		return state, nil
	}
	state["card"] = character.Card
	if representation := character.imageRepresentation(); representation != nil {
		resource, err := cloudAgentStoryboardResourceVersion(repo, userID, representation.ResourceID)
		if err != nil {
			return nil, err
		}
		state["imageResource"] = resource
	}
	return state, nil
}

func cloudAgentStoryboardScopedEdit(args cloudAgentStoryboardEditArgs) bool {
	patches := []map[string]any{}
	switch args.Action {
	case "update":
		patches = append(patches, args.Patch)
	case "batch_update":
		for _, update := range args.Updates {
			patches = append(patches, update.Patch)
		}
	default:
		return false
	}
	for _, patch := range patches {
		for field := range patch {
			if field != "durationSeconds" && !cloudAgentStoryboardTextField(field) {
				return false
			}
		}
	}
	return true
}

func validateCloudAgentStoryboardRevision(repo *repository.Repository, userID, canvasID string, doc map[string]any, args cloudAgentStoryboardEditArgs) error {
	if args.StoryboardRevision == "" {
		if args.SnapshotHash != cloudAgentCanvasHash(doc) {
			return cloudAgentSnapshotConflictError("画布已变化，请重新读取后修改分镜")
		}
		return nil
	}
	if !cloudAgentStoryboardScopedEdit(args) {
		return cloudAgentFieldError("storyboardRevision", "invalid_revision_scope", "表级版本仅用于 update/batch_update 的纯创作字段；资产、追加、删除和产物动作仍须使用最新全画布快照")
	}
	current, err := cloudAgentStoryboardRevision(repo, userID, canvasID, doc, args.NodeID)
	if err != nil {
		return err
	}
	if current != args.StoryboardRevision {
		return cloudAgentSnapshotConflictError("分镜表或依赖资产已变化，本次未写入；请重读目标并核对原补丁")
	}
	return nil
}
