package capability

// 分镜脚本列合同：分镜节点 metadata.storyboard.visibleColumns 的唯一 Go 来源。
//
// 新建节点默认列、历史默认迁移、生成结果回写和 Agent 读取都以这里为准；
// 列顺序与前端单一来源（web/src/lib/canvas/canvas-storyboard-columns.ts）一致，
// editable.go 的 visibleColumns 枚举也由 StoryboardColumnNames 生成。

// storyboardColumnNames 是分镜节点全部可展示列，顺序与前端列选择器一致。
var storyboardColumnNames = []string{
	"shotNumber", "durationSeconds", "plotDescription", "dialogue", "narrativeIntent",
	"viewerPOV", "performanceBlocking", "shotSize", "emotion", "lightingAndAtmosphere",
	"audioEffects", "camera", "motion", "timeBeats", "imageGenerationPrompt",
	"videoMotionPrompt", "assets", "continuityOut", "negativePrompt",
}

// storyboardDefaultColumnNames 是新建/迁移后分镜节点的默认展示列。
var storyboardDefaultColumnNames = []string{
	"shotNumber", "durationSeconds", "plotDescription", "shotSize", "motion", "assets",
}

// storyboardLegacyDefaultColumnSets 是旧版本写入节点 metadata 的默认集合：
// 读取时归一到当前默认，避免旧默认锁死新默认。
//
// 最后一套是生成流程自动补出的“电影感”十五列，不是用户手选，因此同样视为历史默认。
var storyboardLegacyDefaultColumnSets = [][]string{
	{"shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets"},
	{"shotNumber", "durationSeconds", "plotDescription", "dialogue"},
	{"shotNumber", "plotDescription", "videoMotionPrompt", "dialogue"},
	{
		"shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets",
		"shotSize", "narrativeIntent", "viewerPOV", "performanceBlocking", "camera",
		"motion", "timeBeats", "lightingAndAtmosphere", "continuityOut", "negativePrompt",
	},
}

// StoryboardColumnNames 返回全部分镜列（19 个），顺序即 editable 枚举顺序。
func StoryboardColumnNames() []string {
	return append([]string(nil), storyboardColumnNames...)
}

// StoryboardDefaultColumns 返回新分镜节点的 6 个默认展示列。
func StoryboardDefaultColumns() []string {
	return append([]string(nil), storyboardDefaultColumnNames...)
}

// StoryboardDefaultColumnValues 以 []any 返回默认列，便于直接写入节点 metadata。
func StoryboardDefaultColumnValues() []any {
	return storyboardColumnValues(storyboardDefaultColumnNames)
}

// StoryboardLegacyDefaultColumnSets 返回历史默认集合的副本，供迁移审计与测试使用。
func StoryboardLegacyDefaultColumnSets() [][]string {
	sets := make([][]string, len(storyboardLegacyDefaultColumnSets))
	for index, set := range storyboardLegacyDefaultColumnSets {
		sets[index] = append([]string(nil), set...)
	}
	return sets
}

// NormalizeStoryboardColumns 解析节点里持久化的可见列：
//   - 缺失、为空或没有任何合法列时用当前默认；
//   - 与任一历史默认集合等价（长度相同且集合相同）时迁移到当前默认；
//   - 其他情况视为用户手选，原样保留（返回新切片，避免调用方改到节点数据）。
func NormalizeStoryboardColumns(columns []any) []any {
	if len(columns) == 0 {
		return StoryboardDefaultColumnValues()
	}
	values := make([]string, 0, len(columns))
	valid := true
	for _, column := range columns {
		text, ok := column.(string)
		if !ok || text == "" {
			valid = false
			continue
		}
		values = append(values, text)
	}
	if len(values) == 0 {
		return StoryboardDefaultColumnValues()
	}
	if valid && storyboardColumnsAreLegacyDefault(values) {
		return StoryboardDefaultColumnValues()
	}
	if !valid {
		return storyboardColumnValues(values)
	}
	return append([]any(nil), columns...)
}

func storyboardColumnsAreLegacyDefault(columns []string) bool {
	present := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		present[column] = struct{}{}
	}
	for _, legacy := range storyboardLegacyDefaultColumnSets {
		if len(legacy) != len(columns) {
			continue
		}
		matches := true
		for _, column := range legacy {
			if _, ok := present[column]; !ok {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func storyboardColumnValues(columns []string) []any {
	values := make([]any, len(columns))
	for index, column := range columns {
		values[index] = column
	}
	return values
}
