package poselibrary

import (
	"fmt"
	"net/url"
)

type importCandidate struct {
	entry       importEntry
	order       int
	remoteThumb string
	remoteImage string
}

func normalizeImportEntry(entry importEntry) importEntry {
	entry.key = clip(entry.key, 160)
	entry.name = clip(entry.name, 160)
	entry.system = clip(entry.system, 80)
	if entry.system == "" {
		entry.system = "未分类"
	}
	entry.category = clip(entry.category, 80)
	if entry.category == "" {
		entry.category = "其他"
	}
	entry.code = clip(entry.code, 40)
	entry.angle = normalizeAngle(entry.angle)
	entry.group = clip(entry.group, 160)
	if entry.group == "" {
		entry.group = entry.key
	}
	return entry
}

func selectImportCandidates(entries []importEntry, selected map[string]bool, base *url.URL, result *ImportResult) []importCandidate {
	systemIndex := map[string]int{}
	candidates := make([]importCandidate, 0, len(entries))
	seen := map[string]bool{}
	invalid := func(index int, reason string) {
		result.Skipped++
		if len(result.Invalid) < 8 {
			result.Invalid = append(result.Invalid, fmt.Sprintf("第 %d 条：%s", index+1, reason))
		}
	}
	for index, entry := range entries {
		entry = normalizeImportEntry(entry)
		position, known := systemIndex[entry.system]
		if !known {
			position = len(result.Systems)
			systemIndex[entry.system] = position
			result.Systems = append(result.Systems, ImportSystem{Name: entry.system, Imported: selected[entry.system]})
		}
		result.Systems[position].Count++
		if !selected[entry.system] {
			continue
		}
		if entry.key == "" || entry.name == "" {
			invalid(index, "缺少 ID 或名称")
			continue
		}
		if seen[entry.key] {
			invalid(index, "ID 重复："+entry.key)
			continue
		}
		remoteImage, err := resolveRemote(base, entry.image)
		if err != nil || remoteImage == "" {
			reason := "缺少原图路径"
			if err != nil {
				reason = err.Error()
			}
			invalid(index, reason)
			continue
		}
		remoteThumb, err := resolveRemote(base, entry.thumb)
		if err != nil {
			invalid(index, err.Error())
			continue
		}
		seen[entry.key] = true
		candidates = append(candidates, importCandidate{entry: entry, order: index, remoteThumb: remoteThumb, remoteImage: remoteImage})
	}
	return candidates
}
