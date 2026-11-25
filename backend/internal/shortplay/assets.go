package shortplay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"yingce/backend/internal/model"
)

// 资产域纯函数：项目内整数身份、类型校验与别名合并。不依赖数据库。

// NextAssetID 返回本项目内下一个未占用的资产 id：现有最大 id +1，从 1 开始。
//
// 不复用已删除的编号：历史上下文里的旧引用永远不会指向新建对象。id 只在本项目内唯一，
// 跨项目全局唯一由 (shortplayId, id) 复合主键保证。
func NextAssetID(rows []model.ShortplayAsset) int {
	max := 0
	for _, row := range rows {
		if row.ID > max {
			max = row.ID
		}
	}
	return max + 1
}

// ParseAssetID 把工具/请求里的资产 id 文本解析成项目内整数序号；非正整数一律拒绝。
func ParseAssetID(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ValidAssetKind 校验三类资产。短剧的 kind 直接就是生图模板分类，不映射到素材库。
func ValidAssetKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case AssetCharacter, AssetProp, AssetScene:
		return true
	}
	return false
}

// ValidateAssetKind 是写入入口的统一校验，返回可直接展示的错误。
func ValidateAssetKind(kind string) error {
	if ValidAssetKind(kind) {
		return nil
	}
	return fmt.Errorf("资产类型只支持 character、prop、scene")
}

// AssetKindLabel 是三类资产的中文名，用于错误提示。
func AssetKindLabel(kind string) string {
	switch strings.TrimSpace(kind) {
	case AssetCharacter:
		return "角色"
	case AssetProp:
		return "道具"
	case AssetScene:
		return "场景"
	}
	return ""
}

// MergeAliases 合并别名：去首尾空白、丢空串、按原串精确去重并保留首次出现的写法。
func MergeAliases(existing []string, incoming []string) []string {
	result := make([]string, 0, len(existing)+len(incoming))
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	for _, group := range [][]string{existing, incoming} {
		for _, alias := range group {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			if _, ok := seen[alias]; ok {
				continue
			}
			seen[alias] = struct{}{}
			result = append(result, alias)
		}
	}
	return result
}

// SplitAliasesJSON 读 aliases_json 成切片；非法 JSON 返回空。
func SplitAliasesJSON(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil
	}
	return values
}

// SplitEpisodeNumbersJSON 读 episode_numbers_json；非法 JSON 返回空切片。
func SplitEpisodeNumbersJSON(raw string) []int {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values []int
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil
	}
	return values
}
