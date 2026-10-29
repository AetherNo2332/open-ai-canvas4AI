package mediaversions

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// testdata/parity.json 的 expected 由 web/test/canvas-media-versions-parity.test.ts 用的上游 TS 函数决定；
// 两侧测试读同一份夹具，任何一侧偏离都会失败。
type parityCase struct {
	Name      string         `json:"name"`
	Op        string         `json:"op"`
	Now       string         `json:"now"`
	IDs       []string       `json:"ids"`
	Source    string         `json:"source"`
	VersionID string         `json:"versionId"`
	NewID     string         `json:"newId"`
	Node      map[string]any `json:"node"`
	Previous  map[string]any `json:"previous"`
	Args      struct {
		Media      map[string]any `json:"media"`
		Generation map[string]any `json:"generation"`
	} `json:"args"`
	Expected map[string]any `json:"expected"`
}

func normalized(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMediaVersionParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []parityCase `json:"cases"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 10 {
		t.Fatalf("parity fixture has only %d cases", len(fixture.Cases))
	}
	restoreID, restoreClock := newVersionID, clock
	t.Cleanup(func() { newVersionID, clock = restoreID, restoreClock })
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ids := append([]string(nil), c.IDs...)
			newVersionID = func() string {
				if len(ids) == 0 {
					t.Fatal("version id requested beyond fixture")
				}
				id := ids[0]
				ids = ids[1:]
				return id
			}
			now, err := time.Parse(time.RFC3339Nano, c.Now)
			if err != nil {
				t.Fatal(err)
			}
			clock = func() time.Time { return now }
			var got map[string]any
			switch c.Op {
			case "recordGenerated":
				got = map[string]any{"node": RecordGenerated(c.Node, c.Args.Media, c.Args.Generation, now)}
			case "recordReplaced":
				got = map[string]any{"node": RecordReplaced(c.Previous, c.Node, c.Source, now)}
			case "adopt":
				node, err := Adopt(c.Node, c.VersionID)
				if err != nil {
					got = map[string]any{"node": nil}
				} else {
					got = map[string]any{"node": node}
				}
			case "adoptBlock":
				block := map[string]string{"media_version_locked": "locked", "media_version_loading": "loading", "media_version_missing": "missing"}[AdoptBlock(c.Node)]
				if block == "" {
					got = map[string]any{"block": nil}
				} else {
					got = map[string]any{"block": block}
				}
			case "extract":
				node, err := Extract(c.Node, c.VersionID, c.NewID)
				if err != nil {
					got = map[string]any{"node": nil}
				} else {
					got = map[string]any{"node": node}
				}
			case "current":
				if version := Current(c.Node); version != nil {
					got = map[string]any{"versionId": version["id"]}
				} else {
					got = map[string]any{"versionId": nil}
				}
			default:
				t.Fatalf("unknown op %s", c.Op)
			}
			if len(ids) != 0 {
				t.Fatalf("unused version ids %v", ids)
			}
			if want, have := normalized(t, c.Expected), normalized(t, got); !reflect.DeepEqual(want, have) {
				wantJSON, _ := json.MarshalIndent(want, "", " ")
				haveJSON, _ := json.MarshalIndent(have, "", " ")
				t.Fatalf("want %s\nhave %s", wantJSON, haveJSON)
			}
		})
	}
}

func TestAdoptFailureReasonsMatchAgentContract(t *testing.T) {
	node := map[string]any{"id": "n", "type": "image", "metadata": map[string]any{"storageKey": "resource:a", "mediaVersions": []any{map[string]any{"id": "v1", "number": 1.0, "media": map[string]any{"storageKey": "resource:a"}}}}}
	if !IsCurrent(node, "v1") {
		t.Fatal("identity match must mark v1 current")
	}
	for reason, patch := range map[string]map[string]any{"media_version_locked": {"locked": true}, "media_version_loading": {"status": "loading"}} {
		blocked := cloneMap(node)
		for k, v := range patch {
			meta(blocked)[k] = v
		}
		_, err := Adopt(blocked, "v1")
		if e, ok := err.(*Error); !ok || e.Reason != reason {
			t.Fatalf("%s: %v", reason, err)
		}
	}
	if _, err := Adopt(node, "missing"); err == nil || err.(*Error).Reason != "media_version_missing" {
		t.Fatalf("missing: %v", err)
	}
}
