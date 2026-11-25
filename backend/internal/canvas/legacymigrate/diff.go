package legacymigrate

import (
	"fmt"
	"reflect"
	"sort"
)

// DiffDocuments returns deterministic leaf-level changes. It is intentionally
// path-based so previews can be reviewed and replayed across DB and ZIP input.
func DiffDocuments(before, after map[string]any) []Diff {
	out := []Diff{}
	diffValue("", before, after, &out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func diffValue(path string, before, after any, out *[]Diff) {
	beforeMap, beforeIsMap := before.(map[string]any)
	afterMap, afterIsMap := after.(map[string]any)
	if beforeIsMap && afterIsMap {
		keys := make([]string, 0, len(beforeMap)+len(afterMap))
		seen := map[string]bool{}
		for key := range beforeMap {
			seen[key] = true
			keys = append(keys, key)
		}
		for key := range afterMap {
			if !seen[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			left, leftOK := beforeMap[key]
			right, rightOK := afterMap[key]
			child := key
			if path != "" {
				child = path + "." + key
			}
			switch {
			case !leftOK:
				*out = append(*out, Diff{Path: child, After: right})
			case !rightOK:
				*out = append(*out, Diff{Path: child, Before: left})
			default:
				diffValue(child, left, right, out)
			}
		}
		return
	}
	beforeList, beforeIsList := before.([]any)
	afterList, afterIsList := after.([]any)
	if beforeIsList && afterIsList {
		limit := len(beforeList)
		if len(afterList) < limit {
			limit = len(afterList)
		}
		for index := 0; index < limit; index++ {
			diffValue(fmt.Sprintf("%s[%d]", path, index), beforeList[index], afterList[index], out)
		}
		for index := limit; index < len(beforeList); index++ {
			*out = append(*out, Diff{Path: fmt.Sprintf("%s[%d]", path, index), Before: beforeList[index]})
		}
		for index := limit; index < len(afterList); index++ {
			*out = append(*out, Diff{Path: fmt.Sprintf("%s[%d]", path, index), After: afterList[index]})
		}
		return
	}
	if !reflect.DeepEqual(before, after) {
		*out = append(*out, Diff{Path: path, Before: before, After: after})
	}
}
