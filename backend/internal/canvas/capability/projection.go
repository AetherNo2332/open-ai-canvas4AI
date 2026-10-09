package capability

import "strings"

// EditableValues reads only declared paths and recursively removes unknown
// object properties. Legacy metadata may contain service-owned fields beside
// user configuration, so a read must never return the whole stored object.
func (d Descriptor) EditableValues(node map[string]any, textLimit int) (map[string]any, map[string]bool) {
	values, truncated := map[string]any{}, map[string]bool{}
	for key, field := range d.PatchFields {
		var current any = node
		for _, part := range strings.Split(field.Path, ".") {
			object, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current = object[part]
		}
		if current == nil {
			continue
		}
		value, cut := field.projectValue(current, textLimit, 0)
		if value != nil {
			values[key] = value
			if cut {
				truncated[key] = true
			}
		}
	}
	return values, truncated
}

func (field PatchField) projectValue(value any, textLimit, depth int) (any, bool) {
	if depth > 64 {
		return nil, false
	}
	switch field.Kind {
	case patchKindObject:
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		projected := map[string]any{}
		truncated := false
		for key, property := range field.Properties {
			if item, exists := object[key]; exists {
				safe, cut := property.projectValue(item, textLimit, depth+1)
				if safe != nil {
					projected[key] = safe
				}
				truncated = truncated || cut
			}
		}
		if field.MapValues != nil {
			for key, item := range object {
				if len(projected) >= field.MaxProperties && field.MaxProperties > 0 {
					truncated = true
					break
				}
				if !validDictionaryKey(key) {
					continue
				}
				if _, exists := field.Properties[key]; exists {
					continue
				}
				safe, cut := field.MapValues.projectValue(item, textLimit, depth+1)
				if safe != nil {
					projected[key] = safe
				}
				truncated = truncated || cut
			}
		}
		return projected, truncated
	case patchKindArray:
		array, ok := value.([]any)
		if !ok || field.Items == nil {
			return nil, false
		}
		count := len(array)
		if field.MaxItems > 0 && count > field.MaxItems {
			count = field.MaxItems
		}
		projected := make([]any, 0, count)
		truncated := count < len(array)
		for _, item := range array[:count] {
			safe, cut := field.Items.projectValue(item, textLimit, depth+1)
			if safe != nil {
				projected = append(projected, safe)
			}
			truncated = truncated || cut
		}
		return projected, truncated
	case patchKindString:
		if err := field.validateValue(value, "read", depth); err != nil {
			return nil, false
		}
		text := value.(string)
		runes := []rune(text)
		if len(field.Enum) > 0 || textLimit <= 0 || len(runes) <= textLimit {
			return text, false
		}
		return string(runes[:textLimit]), true
	default:
		if err := field.validateValue(value, "read", depth); err != nil {
			return nil, false
		}
		return value, false
	}
}
