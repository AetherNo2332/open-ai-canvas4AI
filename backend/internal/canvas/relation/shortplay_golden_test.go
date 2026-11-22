package relation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type goldenFixture struct {
	Version int          `json:"version"`
	Cases   []goldenCase `json:"cases"`
}

type goldenCase struct {
	Name     string           `json:"name"`
	Nodes    []map[string]any `json:"nodes"`
	Expected []goldenEdge     `json:"expected"`
}

type goldenEdge struct {
	Family     Family `json:"family"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	Field      string `json:"field"`
	ID         string `json:"id"`
}

func TestShortplayRelationGoldenMatchesWebFixture(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "testdata", "shortplay-canvas-port", "relation-projection.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture goldenFixture
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			first := Project(test.Nodes)
			second := Project(test.Nodes)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("重复投影不一致：\n第一次 %#v\n第二次 %#v", first, second)
			}
			got := make([]goldenEdge, 0, len(first))
			for _, edge := range first {
				got = append(got, goldenEdge{
					Family: edge.Family, FromNodeID: edge.FromNodeID,
					ToNodeID: edge.ToNodeID, Field: edge.Field, ID: edge.ID(),
				})
			}
			if !reflect.DeepEqual(got, test.Expected) {
				t.Fatalf("投影不一致\n得到：%#v\n期望：%#v", got, test.Expected)
			}
		})
	}
}
