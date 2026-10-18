package commands

import (
	"reflect"
	"testing"

	"infinite-canvas/backend/internal/canvas/capability"
)

func generatedStoryboardDoc(t *testing.T, visibleColumns string) Document {
	t.Helper()
	columns := ""
	if visibleColumns != "" {
		columns = `,"visibleColumns":` + visibleColumns
	}
	return fixture(t, `{"id":"canvas","nodes":[{"id":"target","type":"script","title":"旧分镜","metadata":{"storyboard":{"rows":[{"id":"old","durationSeconds":6}],"referenceNodeIds":[]`+columns+`,"unknown":"keep"}}}],"connections":[]}`)
}

func applyGeneratedStoryboardForTest(t *testing.T, doc Document) map[string]any {
	t.Helper()
	raw := `{"title":"专业拆镜","rows":[{"durationSeconds":4,"plotDescription":"入场","motion":"推近"}]}`
	result, err := ApplyGeneratedStoryboard(doc, "target", "task", raw)
	if err != nil {
		t.Fatal(err)
	}
	node := entity(result, "node:target")
	if node == nil {
		t.Fatal("generated storyboard lost its target node")
	}
	return node["metadata"].(map[string]any)["storyboard"].(map[string]any)
}

func wantDefaultStoryboardColumns() []any {
	return capability.StoryboardDefaultColumnValues()
}

func TestGeneratedStoryboardNormalizesDefaultColumns(t *testing.T) {
	for name, visibleColumns := range map[string]string{
		"missing":      "",
		"empty":        `[]`,
		"legacy-5":     `["shotNumber","durationSeconds","videoMotionPrompt","dialogue","assets"]`,
		"cinematic-15": `["shotNumber","durationSeconds","videoMotionPrompt","dialogue","assets","shotSize","narrativeIntent","viewerPOV","performanceBlocking","camera","motion","timeBeats","lightingAndAtmosphere","continuityOut","negativePrompt"]`,
	} {
		t.Run(name, func(t *testing.T) {
			board := applyGeneratedStoryboardForTest(t, generatedStoryboardDoc(t, visibleColumns))
			if got := board["visibleColumns"]; !reflect.DeepEqual(got, wantDefaultStoryboardColumns()) {
				t.Fatalf("generated storyboard columns = %v, want current default", got)
			}
			if board["unknown"] != "keep" {
				t.Fatalf("unrelated metadata was dropped: %+v", board)
			}
			rows := maps(board["rows"])
			if len(rows) != 1 || rows[0]["id"] != "shot-task-1" || rows[0]["plotDescription"] != "入场" || rows[0]["motion"] != "推近" {
				t.Fatalf("generated rows are wrong: %+v", rows)
			}
		})
	}
}

func TestGeneratedStoryboardKeepsManualColumns(t *testing.T) {
	board := applyGeneratedStoryboardForTest(t, generatedStoryboardDoc(t, `["shotNumber","dialogue"]`))
	want := []any{"shotNumber", "dialogue"}
	if got := board["visibleColumns"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("manual storyboard columns were rewritten: %v", got)
	}
}
