package app

func cloudAgentObjectOrder(value any) []string {
	result := []string{}
	for _, item := range creationMaps(value) {
		result = append(result, stringValue(item["id"]))
	}
	return result
}
func cloudAgentSameObjectOrder(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i, id := range left {
		if right[i] != id {
			return false
		}
	}
	return true
}
