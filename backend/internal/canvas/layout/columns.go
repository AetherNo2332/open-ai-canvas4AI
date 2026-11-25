package layout

import (
	"sort"
	"strings"
)

// 分列沿两条轴：节点类型，以及稳定标题前缀。坐标、列宽与行距全部由这里计算，
// 调用方只提交节点和列成员，不接触几何常量。
const (
	ColumnGap    = 96
	ColumnRowGap = 48
)

const columnSeparators = "·-：:|—"

// ColumnGroup 是一列：Type 为节点类型，Prefix 为稳定标题前缀，
// Nodes 为列内节点，数组顺序即从上到下。
type ColumnGroup struct {
	Type   string
	Prefix string
	Nodes  []Node
}

// ColumnGroups 是自动分列的结果。Overflow 表示列数超过 maxGroups；
// 调用方应拒绝并附上列键清单，不应静默合并。
type ColumnGroups struct {
	Groups   []ColumnGroup
	Overflow bool
}

// ColumnPrefix 提取稳定标题前缀：在 trim 后的标题里找最早出现的分隔符，
// 取其左侧；左侧为空则回退整个标题。
func ColumnPrefix(title string) string {
	trimmed := strings.TrimSpace(title)
	for position, char := range trimmed {
		if !strings.ContainsRune(columnSeparators, char) {
			continue
		}
		prefix := strings.TrimSpace(trimmed[:position])
		if prefix == "" {
			return trimmed
		}
		return prefix
	}
	return trimmed
}

// AutoColumnGroups 按节点类型和稳定标题前缀自动分列。
// 列内按标题自然序，并列时按原位置 (y, x) 再按 ID；
// 列间先按泳道次序，再按列组最早出现位置 (minX, minY)。
// maxGroups <= 0 表示不限列数。
func AutoColumnGroups(nodes []Node, maxGroups int) ColumnGroups {
	buckets := make(map[string]*ColumnGroup, len(nodes))
	keys := make([]string, 0, len(nodes))
	for _, node := range nodes {
		prefix := ColumnPrefix(node.Title)
		key := node.Type + "\x00" + prefix
		group, ok := buckets[key]
		if !ok {
			group = &ColumnGroup{Type: node.Type, Prefix: prefix}
			buckets[key] = group
			keys = append(keys, key)
		}
		group.Nodes = append(group.Nodes, node)
	}
	groups := make([]ColumnGroup, 0, len(keys))
	for _, key := range keys {
		group := buckets[key]
		sort.SliceStable(group.Nodes, func(i, j int) bool {
			return columnNodeLess(group.Nodes[i], group.Nodes[j])
		})
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return columnGroupLess(groups[i], groups[j])
	})
	return ColumnGroups{Groups: groups, Overflow: maxGroups > 0 && len(groups) > maxGroups}
}

// Columns 计算分列几何：各列按给定顺序从左到右排布，列宽取该列最宽节点，
// 列内左对齐自上而下堆叠；整体锚定在参与节点包围盒的左上角。
// gap、rowGap <=0 时取 ColumnGap、ColumnRowGap。
func Columns(groups []ColumnGroup, gap, rowGap float64) map[string]Position {
	result := map[string]Position{}
	all := make([]Node, 0)
	for _, group := range groups {
		all = append(all, group.Nodes...)
	}
	if len(all) == 0 {
		return result
	}
	if gap <= 0 {
		gap = ColumnGap
	}
	if rowGap <= 0 {
		rowGap = ColumnRowGap
	}
	left, top := minX(all), minY(all)
	x := left
	for _, group := range groups {
		if len(group.Nodes) == 0 {
			continue
		}
		y := top
		for _, node := range group.Nodes {
			result[node.ID] = Position{X: x, Y: y}
			y += node.height() + rowGap
		}
		x += maxWidth(group.Nodes) + gap
	}
	return result
}

func columnNodeLess(first, second Node) bool {
	if compared := compareNatural(first.Title, second.Title); compared != 0 {
		return compared < 0
	}
	if first.Y != second.Y {
		return first.Y < second.Y
	}
	if first.X != second.X {
		return first.X < second.X
	}
	return first.ID < second.ID
}

func columnGroupLess(first, second ColumnGroup) bool {
	firstLane, secondLane := laneIndex(first), laneIndex(second)
	if firstLane != secondLane {
		return firstLane < secondLane
	}
	if firstMinX, secondMinX := groupMinX(first), groupMinX(second); firstMinX != secondMinX {
		return firstMinX < secondMinX
	}
	return groupMinY(first) < groupMinY(second)
}

func laneIndex(group ColumnGroup) int {
	if len(group.Nodes) == 0 {
		return len(LaneOrder)
	}
	for index, lane := range LaneOrder {
		if group.Nodes[0].Lane == lane {
			return index
		}
	}
	return len(LaneOrder)
}

func groupMinX(group ColumnGroup) float64 {
	if len(group.Nodes) == 0 {
		return 0
	}
	return minX(group.Nodes)
}

func groupMinY(group ColumnGroup) float64 {
	if len(group.Nodes) == 0 {
		return 0
	}
	return minY(group.Nodes)
}

// compareNatural 按自然序比较两个标题：数字段按数值比，其余按字节比。
// BMP 内与前端逐段等价；代理对的排序差异不影响当前标题范围。
func compareNatural(first, second string) int {
	i, j := 0, 0
	for i < len(first) && j < len(second) {
		if isASCIIDigit(first[i]) && isASCIIDigit(second[j]) {
			startI, startJ := i, j
			for i < len(first) && isASCIIDigit(first[i]) {
				i++
			}
			for j < len(second) && isASCIIDigit(second[j]) {
				j++
			}
			if compared := compareDigitRun(first[startI:i], second[startJ:j]); compared != 0 {
				return compared
			}
			continue
		}
		if first[i] != second[j] {
			if first[i] < second[j] {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch restI, restJ := len(first)-i, len(second)-j; {
	case restI < restJ:
		return -1
	case restI > restJ:
		return 1
	}
	return 0
}

func compareDigitRun(first, second string) int {
	trimmedFirst, trimmedSecond := strings.TrimLeft(first, "0"), strings.TrimLeft(second, "0")
	if len(trimmedFirst) != len(trimmedSecond) {
		if len(trimmedFirst) < len(trimmedSecond) {
			return -1
		}
		return 1
	}
	if compared := strings.Compare(trimmedFirst, trimmedSecond); compared != 0 {
		return compared
	}
	switch {
	case len(first) < len(second):
		return -1
	case len(first) > len(second):
		return 1
	}
	return 0
}

func isASCIIDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

// LanePriority 返回节点类型在 LaneOrder 里的下标；未知或空泳道排在最后。
func LanePriority(nodeType string) int {
	for index, lane := range LaneOrder {
		if lane == nodeType {
			return index
		}
	}
	return len(LaneOrder)
}

// SortNodesNatural 按标题自然序稳定排序节点，列内从上到下使用。
func SortNodesNatural(nodes []Node) []Node {
	sorted := append([]Node(nil), nodes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if compared := compareNatural(sorted[i].Title, sorted[j].Title); compared != 0 {
			return compared < 0
		}
		if sorted[i].Y != sorted[j].Y {
			return sorted[i].Y < sorted[j].Y
		}
		if sorted[i].X != sorted[j].X {
			return sorted[i].X < sorted[j].X
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}
