package layout

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"testing"
)

const columnFixturePath = "testdata/column-cases.json"

type columnFixtureNode struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Lane   string  `json:"lane"`
	Title  string  `json:"title"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type columnFixtureGroup struct {
	Type    string   `json:"type"`
	Prefix  string   `json:"prefix"`
	NodeIDs []string `json:"nodeIds"`
}

type columnFixtureCase struct {
	Name           string               `json:"name"`
	Note           string               `json:"note"`
	Nodes          []columnFixtureNode  `json:"nodes"`
	MaxGroups      int                  `json:"maxGroups"`
	ExplicitGroups []columnFixtureGroup `json:"explicitGroups"`
	WantGroups     []columnFixtureGroup `json:"wantGroups"`
	WantOverflow   bool                 `json:"wantOverflow"`
	WantPositions  map[string][]float64 `json:"wantPositions"`
}

type columnFixture struct {
	ColumnGap    float64 `json:"columnGap"`
	ColumnRowGap float64 `json:"columnRowGap"`
	Separators   string  `json:"separators"`
	Prefix       []struct {
		Title string `json:"title"`
		Want  string `json:"want"`
	} `json:"prefix"`
	NaturalOrder []struct {
		Titles []string `json:"titles"`
		Want   []string `json:"want"`
	} `json:"naturalOrder"`
	Columns []columnFixtureCase `json:"columns"`
}

func loadColumnFixture(t *testing.T) columnFixture {
	t.Helper()
	raw, err := os.ReadFile(columnFixturePath)
	if err != nil {
		t.Fatalf("读取夹具失败：%v", err)
	}
	var fixture columnFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("解析夹具失败：%v", err)
	}
	return fixture
}

func (node columnFixtureNode) toNode() Node {
	return Node{
		ID: node.ID, Type: node.Type, Lane: node.Lane, Title: node.Title,
		X: node.X, Y: node.Y, Width: node.Width, Height: node.Height,
	}
}

func TestColumnConstantsMatchFixture(t *testing.T) {
	fixture := loadColumnFixture(t)
	if ColumnGap != fixture.ColumnGap {
		t.Fatalf("ColumnGap = %v，夹具为 %v", ColumnGap, fixture.ColumnGap)
	}
	if ColumnRowGap != fixture.ColumnRowGap {
		t.Fatalf("ColumnRowGap = %v，夹具为 %v", ColumnRowGap, fixture.ColumnRowGap)
	}
	if columnSeparators != fixture.Separators {
		t.Fatalf("分隔符集 = %q，夹具为 %q", columnSeparators, fixture.Separators)
	}
}

func TestColumnPrefixGolden(t *testing.T) {
	fixture := loadColumnFixture(t)
	for _, item := range fixture.Prefix {
		if got := ColumnPrefix(item.Title); got != item.Want {
			t.Fatalf("ColumnPrefix(%q) = %q，期望 %q", item.Title, got, item.Want)
		}
	}
}

func TestColumnNaturalOrderGolden(t *testing.T) {
	fixture := loadColumnFixture(t)
	for _, item := range fixture.NaturalOrder {
		titles := append([]string(nil), item.Titles...)
		sort.SliceStable(titles, func(i, j int) bool { return compareNatural(titles[i], titles[j]) < 0 })
		if len(titles) != len(item.Want) {
			t.Fatalf("自然序长度 = %d，期望 %d", len(titles), len(item.Want))
		}
		for index := range titles {
			if titles[index] != item.Want[index] {
				t.Fatalf("自然序 %v 第 %d 项 = %q，期望 %q", item.Titles, index, titles[index], item.Want[index])
			}
		}
	}
}

func TestAutoColumnGroupsGolden(t *testing.T) {
	fixture := loadColumnFixture(t)
	for _, item := range fixture.Columns {
		if item.ExplicitGroups != nil {
			continue
		}
		nodes := make([]Node, 0, len(item.Nodes))
		for _, node := range item.Nodes {
			nodes = append(nodes, node.toNode())
		}
		result := AutoColumnGroups(nodes, item.MaxGroups)
		if result.Overflow != item.WantOverflow {
			t.Fatalf("%s：overflow = %v，期望 %v", item.Name, result.Overflow, item.WantOverflow)
		}
		assertColumnGroups(t, item.Name, fixtureGroupsOf(result.Groups), item.WantGroups)
		positions := Columns(result.Groups, 0, 0)
		assertColumnPositions(t, item.Name, positions, item.WantPositions)
		assertNoColumnOverlap(t, item.Name, nodes, positions)
	}
}

func TestExplicitColumnOrderGolden(t *testing.T) {
	fixture := loadColumnFixture(t)
	for _, item := range fixture.Columns {
		if item.ExplicitGroups == nil {
			continue
		}
		byID := make(map[string]Node, len(item.Nodes))
		for _, node := range item.Nodes {
			byID[node.ID] = node.toNode()
		}
		groups := make([]ColumnGroup, 0, len(item.ExplicitGroups))
		for _, group := range item.ExplicitGroups {
			members := make([]Node, 0, len(group.NodeIDs))
			for _, id := range group.NodeIDs {
				node, ok := byID[id]
				if !ok {
					t.Fatalf("%s：显式列 %s 引用了不存在的节点 %s", item.Name, group.Prefix, id)
				}
				members = append(members, node)
			}
			groups = append(groups, ColumnGroup{Type: group.Type, Prefix: group.Prefix, Nodes: members})
		}
		assertColumnGroups(t, item.Name, fixtureGroupsOf(groups), item.WantGroups)
		positions := Columns(groups, 0, 0)
		assertColumnPositions(t, item.Name, positions, item.WantPositions)
		nodes := make([]Node, 0, len(item.Nodes))
		for _, node := range item.Nodes {
			nodes = append(nodes, node.toNode())
		}
		assertNoColumnOverlap(t, item.Name, nodes, positions)
	}
}

func TestColumnsHonoursExplicitGaps(t *testing.T) {
	groups := []ColumnGroup{{Type: "text", Prefix: "A", Nodes: []Node{
		{ID: "a1", Type: "text", Lane: "text", Title: "A-01", X: 0, Y: 0, Width: 100, Height: 100},
		{ID: "a2", Type: "text", Lane: "text", Title: "A-02", X: 0, Y: 200, Width: 100, Height: 100},
	}}}
	positions := Columns(groups, 10, 20)
	assertPosition(t, positions, "a1", 0, 0)
	assertPosition(t, positions, "a2", 0, 120)
}

func assertColumnGroups(t *testing.T, name string, got, want []columnFixtureGroup) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s：列数 = %d，期望 %d（%+v）", name, len(got), len(want), got)
	}
	for index := range got {
		if got[index].Type != want[index].Type || got[index].Prefix != want[index].Prefix {
			t.Fatalf("%s：第 %d 列键 = (%s,%s)，期望 (%s,%s)", name, index, got[index].Type, got[index].Prefix, want[index].Type, want[index].Prefix)
		}
		if len(got[index].NodeIDs) != len(want[index].NodeIDs) {
			t.Fatalf("%s：第 %d 列节点数 = %d，期望 %d", name, index, len(got[index].NodeIDs), len(want[index].NodeIDs))
		}
		for position, id := range want[index].NodeIDs {
			if got[index].NodeIDs[position] != id {
				t.Fatalf("%s：第 %d 列第 %d 个节点 = %s，期望 %s", name, index, position, got[index].NodeIDs[position], id)
			}
		}
	}
}

func fixtureGroupsOf(groups []ColumnGroup) []columnFixtureGroup {
	projected := make([]columnFixtureGroup, 0, len(groups))
	for _, group := range groups {
		ids := make([]string, 0, len(group.Nodes))
		for _, node := range group.Nodes {
			ids = append(ids, node.ID)
		}
		projected = append(projected, columnFixtureGroup{Type: group.Type, Prefix: group.Prefix, NodeIDs: ids})
	}
	return projected
}

func assertColumnPositions(t *testing.T, name string, got map[string]Position, want map[string][]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s：位置数 = %d，期望 %d", name, len(got), len(want))
	}
	for id, expected := range want {
		position, ok := got[id]
		if !ok {
			t.Fatalf("%s：缺少节点 %s 的位置", name, id)
		}
		if math.Abs(position.X-expected[0]) > 0.001 || math.Abs(position.Y-expected[1]) > 0.001 {
			t.Fatalf("%s：节点 %s 位置 = (%v,%v)，期望 (%v,%v)", name, id, position.X, position.Y, expected[0], expected[1])
		}
	}
}

func assertNoColumnOverlap(t *testing.T, name string, nodes []Node, positions map[string]Position) {
	t.Helper()
	applied := withPositions(nodes, positions)
	if pairs := overlapsIn(applied); len(pairs) != 0 {
		t.Fatalf("%s：分列后出现重叠：%+v", name, pairs)
	}
}

func overlapsIn(nodes []Node) [][2]string {
	pairs := make([][2]string, 0)
	for index := 0; index < len(nodes); index++ {
		for next := index + 1; next < len(nodes); next++ {
			if overlaps(nodes[index], nodes[next], 0) {
				pairs = append(pairs, [2]string{nodes[index].ID, nodes[next].ID})
			}
		}
	}
	return pairs
}
