package shortplay

import (
	"encoding/json"
	"sort"
	"strings"

	"yingce/backend/internal/model"
)

// ExtractApplyPlan 是提取结果的落库计划：Rows 里既有要更新的既有行（ID 命中现有资产），
// 也有要新建的行（ID 由 nextID 起顺序分配）。app 层按「现有 ID 集合」决定 create/update，
// 新建与更新共用同一条写入路径，避免两套合并逻辑漂移。
type ExtractApplyPlan struct {
	Rows []model.ShortplayAsset
}

// PlanExtractApply 把提取结果合并到现有资产：
//   - assetId 命中同 kind 的行 → 更新该行；没命中再按 kind+标准名/别名匹配，避免模型
//     漏写 id 时把同一人物另起一行；
//   - Locked 行只并入别名与分集绑定，不覆盖设定字段（锁表示用户已确认这张设定）；
//   - 每个资产的 EpisodeNumbers 取本批原文里命中标准名或别名的集号，与既有值求并集；
//   - 新行按 nextID 起顺序分配项目内序号，不复用已删除编号。
func PlanExtractApply(shortplayID string, existing []model.ShortplayAsset, extracted []ExtractedAsset, batch []model.ShortplayEpisode, nextID int) ExtractApplyPlan {
	byID := make(map[int]int, len(existing))
	byKey := make(map[string]int, len(existing))
	for index, row := range existing {
		byID[row.ID] = index
		if key := assetMatchKey(row.Kind, row.Name); key != "" {
			byKey[key] = index
		}
		for _, alias := range SplitAliasesJSON(row.AliasesJSON) {
			if key := assetMatchKey(row.Kind, alias); key != "" {
				if _, ok := byKey[key]; !ok {
					byKey[key] = index
				}
			}
		}
	}
	rows := make([]model.ShortplayAsset, 0, len(extracted))
	used := make(map[int]struct{}, len(existing))
	// 新建行按 kind+标准名去重：模型偶尔在同一批里重复输出同一资产，不能落两行。
	newKeys := make(map[string]struct{}, len(extracted))
	for _, item := range extracted {
		if item.Kind == "" || strings.TrimSpace(item.Name) == "" {
			continue
		}
		index, found := -1, false
		if item.AssetID > 0 {
			if candidate, ok := byID[item.AssetID]; ok && existing[candidate].Kind == item.Kind {
				index, found = candidate, true
			}
		}
		if !found {
			if candidate, ok := byKey[assetMatchKey(item.Kind, item.Name)]; ok {
				index, found = candidate, true
			}
		}
		var row model.ShortplayAsset
		if found {
			row = existing[index]
			if _, taken := used[row.ID]; taken {
				continue
			}
			row = mergeExtractedAsset(row, item)
			used[row.ID] = struct{}{}
		} else {
			key := assetMatchKey(item.Kind, item.Name)
			if _, taken := newKeys[key]; taken && key != "" {
				continue
			}
			newKeys[key] = struct{}{}
			row = model.ShortplayAsset{
				ShortplayID: shortplayID,
				ID:          nextID,
				Kind:        item.Kind,
				Name:        strings.TrimSpace(item.Name),
				AliasesJSON: encodeStrings(nonEmptyStrings(item.Aliases)),
				Description: strings.TrimSpace(item.Description),
				Appearance:  strings.TrimSpace(item.Appearance),
				Voice:       strings.TrimSpace(item.Voice),
				ImageStatus: "",
				Position:    len(existing) + len(rows) + 1,
			}
			nextID++
		}
		episodeNumbers := mergeEpisodeNumbers(SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON), matchEpisodes(batch, row.Name, SplitAliasesJSON(row.AliasesJSON)))
		row.EpisodeNumbersJSON = encodeInts(episodeNumbers)
		rows = append(rows, row)
	}
	return ExtractApplyPlan{Rows: rows}
}

// mergeExtractedAsset 把一条提取结果并进既有行；Locked 行保护设定字段。
func mergeExtractedAsset(row model.ShortplayAsset, item ExtractedAsset) model.ShortplayAsset {
	aliases := MergeAliases(SplitAliasesJSON(row.AliasesJSON), item.Aliases)
	if name := strings.TrimSpace(item.Name); name != "" && name != row.Name {
		aliases = MergeAliases(aliases, []string{name})
	}
	row.AliasesJSON = encodeStrings(aliases)
	if row.Locked {
		return row
	}
	if value := strings.TrimSpace(item.Description); value != "" {
		row.Description = value
	}
	if value := strings.TrimSpace(item.Appearance); value != "" {
		row.Appearance = value
	}
	if value := strings.TrimSpace(item.Voice); value != "" {
		row.Voice = value
	}
	return row
}

// matchEpisodes 找出本批里提到该资产标准名或别名的集号（不区分 ASCII 大小写）。
func matchEpisodes(batch []model.ShortplayEpisode, name string, aliases []string) []int {
	names := append([]string{name}, aliases...)
	numbers := make([]int, 0, len(batch))
	for _, episode := range batch {
		text := strings.ToLower(episode.ScriptText)
		for _, candidate := range names {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if candidate == "" {
				continue
			}
			if strings.Contains(text, candidate) {
				numbers = append(numbers, episode.Number)
				break
			}
		}
	}
	return numbers
}

func mergeEpisodeNumbers(existing, incoming []int) []int {
	seen := make(map[int]struct{}, len(existing)+len(incoming))
	result := make([]int, 0, len(existing)+len(incoming))
	for _, group := range [][]int{existing, incoming} {
		for _, number := range group {
			if number <= 0 {
				continue
			}
			if _, ok := seen[number]; ok {
				continue
			}
			seen[number] = struct{}{}
			result = append(result, number)
		}
	}
	sort.Ints(result)
	return result
}

func assetMatchKey(kind, name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return strings.TrimSpace(kind) + "|" + name
}

func encodeStrings(values []string) string {
	raw, err := json.Marshal(nonEmptyStrings(values))
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func encodeInts(values []int) string {
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// ExtractCursorPayload 是提取 job 的游标负载：下一批从 cursor 集号开始，0 表示已读完。
func ExtractCursorPayload(cursor int) string {
	raw, err := json.Marshal(struct {
		Cursor int `json:"cursor"`
	}{Cursor: cursor})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// ParseExtractCursor 读回 job 负载里的 cursor；空串或非法 JSON 当作 0（从头开始）。
func ParseExtractCursor(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	var payload struct {
		Cursor int `json:"cursor"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return 0
	}
	return payload.Cursor
}
