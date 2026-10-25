package commands

func editsFolderBinding(op Operation) bool {
	_, project := op.Patch["folderProjectId"]
	_, folder := op.Patch["folderAssetFolderId"]
	return project || folder
}

func editedFolderBinding(doc Document, op Operation) (string, string) {
	projectID, folderID := "", ""
	if node := entity(doc, "node:"+op.ID); node != nil {
		metadata, _ := node["metadata"].(map[string]any)
		folder, _ := metadata["folder"].(map[string]any)
		projectID, folderID = text(folder["projectId"]), text(folder["assetFolderId"])
	}
	if value, ok := op.Patch["folderProjectId"]; ok {
		projectID = text(value)
	}
	if value, ok := op.Patch["folderAssetFolderId"]; ok {
		folderID = text(value)
	}
	return projectID, folderID
}
