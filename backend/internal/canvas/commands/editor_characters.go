package commands

import (
	"encoding/json"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
)

func characterBindingKey(op Operation) string {
	return op.AssetID + "\x00" + op.VersionID + "\x00" + op.Action
}

// Character snapshots are hydrated from the authorized asset version, never from editor JSON.
func loadCharacterBinding(tx *repository.Repository, ownerID string, op Operation) (map[string]any, error) {
	if err := validateCanvasID(op.AssetID, "角色资产 ID", 80); err != nil {
		return nil, err
	}
	if op.VersionID != "" {
		if err := validateCanvasID(op.VersionID, "角色版本 ID", 128); err != nil {
			return nil, err
		}
	}
	if op.Action != "current" && op.Action != "pinned" {
		return nil, BadAuthRequest("角色卡版本策略无效")
	}
	asset, err := tx.UserCharacterAsset(ownerID, op.AssetID)
	if err != nil || asset.Status == model.AssetVersionStatusArchived {
		return nil, failure("resource_not_owned", "角色资产不存在或未授权")
	}
	versionID := asset.PrimaryVersionID
	if op.Action == "pinned" {
		versionID = op.VersionID
	}
	version, err := tx.AssetVersion(versionID)
	if err != nil || version.AssetID != asset.ID {
		return nil, BadAuthRequest("角色版本不存在或不属于当前角色")
	}
	definition := map[string]any{}
	if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil {
		return nil, BadAuthRequest("角色设定无效")
	}
	aliases := []any{}
	for _, alias := range stringList(definition["aliases"]) {
		aliases = append(aliases, alias)
	}
	parts := []string{"【角色卡：" + asset.Title + "】"}
	for _, field := range []string{"role", "appearance", "physique", "clothing", "personality", "props", "consistencyPrompt"} {
		if value := strings.TrimSpace(text(definition[field])); value != "" {
			parts = append(parts, value)
		}
	}
	metadata := map[string]any{"workflowKind": "character", "assetId": asset.ID, "characterAssetId": asset.ID, "characterVersionId": version.ID, "characterVersionPolicy": op.Action, "characterName": asset.Title, "characterPrompt": strings.Join(parts, "\n"), "characterAliases": aliases, "characterDefinition": definition, "characterVisualStatus": "missing", "characterVoiceStatus": "missing", "status": "success"}
	representations, err := tx.AssetRepresentations(version.ID)
	if err != nil {
		return nil, err
	}
	roles := map[string]string{}
	for _, representation := range representations {
		if representation.MediaType != "image" && !strings.HasPrefix(representation.MediaType, "image/") {
			continue
		}
		resource, err := tx.ResourceForUser(ownerID, representation.ResourceID)
		if err != nil || resource.Status != model.ResourceStatusReady || resource.Kind != "image" {
			return nil, failure("resource_not_owned", "角色形象资源未就绪或未授权")
		}
		roles[representation.Role] = resource.ID
	}
	if roles["turnaround_sheet"] != "" || (roles["front"] != "" && roles["side"] != "" && roles["back"] != "") {
		metadata["characterVisualStatus"] = "ready"
	} else if len(roles) > 0 {
		metadata["characterVisualStatus"] = "partial"
	}
	for _, role := range []string{"turnaround_sheet", "primary", "front"} {
		if id := roles[role]; id != "" {
			metadata["characterCoverUrl"] = assets.FileURL(id)
			break
		}
	}
	binding, err := tx.CharacterVoiceBinding(version.ID)
	if err != nil && !tx.IsMissing(err) {
		return nil, err
	}
	if binding != nil && err == nil {
		profile, err := tx.VoiceProfileForUser(ownerID, binding.VoiceProfileID)
		if err == nil && profile.Status == "active" {
			metadata["characterVoiceStatus"], metadata["characterVoiceName"], metadata["characterVoiceInstructions"] = "ready", profile.Name, binding.Instructions
			metadata["characterVoiceProfile"] = map[string]any{"name": profile.Name, "provider": profile.Provider, "language": profile.Language, "timbre": profile.Timbre}
		} else {
			metadata["characterVoiceStatus"] = "unavailable"
		}
	}
	return metadata, nil
}

func (p *planner) bindCharacter(doc Document, op Operation, ctx ValidationContext) error {
	node := entity(doc, "node:"+op.ID)
	if node == nil {
		return failure("entity_deleted", "角色卡节点不存在")
	}
	if text(node["type"]) != "text" {
		return BadAuthRequest("角色卡必须绑定文本节点")
	}
	metadata, ok := ctx.CharacterBindings[characterBindingKey(op)]
	if !ok || text(metadata["characterAssetId"]) != op.AssetID || text(metadata["characterVersionPolicy"]) != op.Action || (op.Action != "current" && op.Action != "pinned") || (op.Action == "pinned" && text(metadata["characterVersionId"]) != op.VersionID) {
		return failure("resource_not_owned", "角色资产或版本未授权")
	}
	cloned, err := cloneDocument(Document(metadata))
	if err != nil {
		return err
	}
	existing := p.canvasNodeMetadata(node)
	for key := range existing {
		if strings.HasPrefix(key, "character") || key == "content" || key == "richText" || key == "assetId" {
			delete(existing, key)
		}
	}
	for key, value := range cloned {
		existing[key] = value
	}
	node["title"] = text(metadata["characterName"])
	return nil
}
