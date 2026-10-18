package references

func BindStoryboardOutput(doc map[string]any, boardID, rowID, mode, outputID string) error {
	idx := index(doc)
	node := idx[boardID]
	row := rowFor(node, "storyboard", rowID)
	if node == nil || str(node["type"]) != "script" || row == nil {
		return fail("reference_missing", "rowId", "分镜行不存在")
	}
	if mode != "image" && mode != "video" {
		return fail("invalid_reference", "outputMode", "分镜输出模式必须是 image 或 video")
	}
	if activeStatus(str(row["status"])) || activeStatus(str(row["taskStatus"])) {
		return fail("reference_busy", "rowId", "分镜行仍有未完成任务")
	}
	if outputID != "" {
		output := idx[outputID]
		if output == nil || str(output["type"]) != mode {
			return fail("invalid_reference", "outputNodeId", "输出节点不存在或类型不符")
		}
		if activeStatus(str(meta(output)["status"])) || activeStatus(str(meta(output)["taskStatus"])) {
			return fail("reference_busy", "outputNodeId", "输出仍有未完成任务")
		}
		for _, other := range maps(doc["nodes"]) {
			for _, r := range maps(obj(meta(other)["storyboard"])["rows"]) {
				if (str(other["id"]) != boardID || str(r["id"]) != rowID) && str(r[mode+"NodeId"]) == outputID {
					return fail("invalid_reference", "outputNodeId", "产物已属于另一分镜行")
				}
			}
		}
		row[mode+"NodeId"] = outputID
	} else {
		delete(row, mode+"NodeId")
	}
	return nil
}
func activeStatus(status string) bool {
	return status == "queued" || status == "running" || status == "loading" || status == "pending" || status == "submitting"
}
