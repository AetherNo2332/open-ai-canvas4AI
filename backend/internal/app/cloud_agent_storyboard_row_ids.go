package app

type cloudAgentStoryboardRowIDError struct {
	error
	InvalidRowIDs   []string
	DuplicateRowIDs []string
}

func (e *cloudAgentStoryboardRowIDError) Unwrap() error { return e.error }

func validateCloudAgentStoryboardRowIDs(rows []map[string]any, ids []string, field string) error {
	known, seen, invalidSeen, duplicateSeen := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		known[stringValue(row["id"])] = true
	}
	invalid, duplicate := []string{}, []string{}
	for _, id := range ids {
		if (id == "" || !known[id]) && !invalidSeen[id] {
			invalid = append(invalid, id)
			invalidSeen[id] = true
		}
		if seen[id] && !duplicateSeen[id] {
			duplicate = append(duplicate, id)
			duplicateSeen[id] = true
		}
		seen[id] = true
	}
	if len(invalid)+len(duplicate) == 0 {
		return nil
	}
	return &cloudAgentStoryboardRowIDError{
		error:         cloudAgentFieldError(field, "invalid_row_ids", "分镜行不存在或重复；按 invalidRowIds/duplicateRowIds 修正，使用最新读取的 rowId；整批未执行"),
		InvalidRowIDs: invalid, DuplicateRowIDs: duplicate,
	}
}
