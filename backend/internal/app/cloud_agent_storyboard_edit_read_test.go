package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func storyboardFieldReadFixture(t *testing.T, rows []map[string]any) (*Service, *model.CanvasProject) {
	t.Helper()
	s, db, canvas := cloudAgentStoryboardFixture(t)
	doc := map[string]any{"referenceFieldsVersion": 1, "connections": []any{}, "nodes": []any{map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": rows}}}}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	canvas.PayloadJSON = string(raw)
	if err := db.Save(canvas).Error; err != nil {
		t.Fatal(err)
	}
	return s, canvas
}

func storyboardFieldRead(t *testing.T, s *Service, canvas *model.CanvasProject, args map[string]any) map[string]any {
	t.Helper()
	args["nodeId"] = "board"
	result, err := cloudAgentReadStoryboard(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "read", args))
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

func TestStoryboardReadMissingFields(t *testing.T) {
	rows := make([]map[string]any, 89)
	for i := range rows {
		rows[i] = map[string]any{"id": fmt.Sprintf("row-%02d", i), "shotNumber": i + 1, "durationSeconds": 5, "plotDescription": "fallback", "dialogue": "private dialogue", "imageGenerationPrompt": fmt.Sprintf("image %d", i), "videoMotionPrompt": fmt.Sprintf("video %d", i)}
	}
	rows[3]["imageGenerationPrompt"] = " \t"
	delete(rows[27], "videoMotionPrompt")
	rows[88]["imageGenerationPrompt"], rows[88]["videoMotionPrompt"] = "", ""
	s, canvas := storyboardFieldReadFixture(t, rows)
	fields := []string{"imageGenerationPrompt", "videoMotionPrompt"}
	first := storyboardFieldRead(t, s, canvas, map[string]any{"fields": fields, "missingAnyFields": fields, "rows": 1})
	board := first["storyboard"].(map[string]any)
	got := creationMaps(board["rows"])
	if len(got) != 1 || got[0]["rowId"] != "row-03" || got[0]["videoMotionPrompt"] != "video 3" {
		t.Fatalf("missing OR filter/projection: %v", board)
	}
	if _, exists := got[0]["dialogue"]; exists {
		t.Fatal("unrequested creative field leaked")
	}
	cov := first["coverage"].(map[string]any)
	if cov["selectedRows"] != 3 || cov["totalRows"] != 89 || cov["selectionComplete"] != false || cov["tableComplete"] != false {
		t.Fatalf("coverage: %v", cov)
	}
	next := board["nextRead"].(map[string]any)["arguments"].(map[string]any)
	if !reflect.DeepEqual(next["fields"], fields) || !reflect.DeepEqual(next["missingAnyFields"], fields) {
		t.Fatalf("continuation loses filters: %v", next)
	}
	second := storyboardFieldRead(t, s, canvas, next)
	if got := creationMaps(second["storyboard"].(map[string]any)["rows"]); len(got) != 1 || got[0]["rowId"] != "row-27" {
		t.Fatalf("filtered offset: %v", got)
	}
	selected := storyboardFieldRead(t, s, canvas, map[string]any{"rowIds": []string{"row-88", "row-04", "row-03"}, "fields": fields, "missingAnyFields": fields, "rows": 1})
	next = selected["storyboard"].(map[string]any)["nextRead"].(map[string]any)["arguments"].(map[string]any)
	last := storyboardFieldRead(t, s, canvas, next)
	if got := creationMaps(last["storyboard"].(map[string]any)["rows"]); len(got) != 1 || got[0]["rowId"] != "row-03" {
		t.Fatalf("rowIds filtered continuation: %v", got)
	}
	if last["coverage"].(map[string]any)["selectionComplete"] != true || last["coverage"].(map[string]any)["tableComplete"] != false {
		t.Fatalf("selection coverage: %v", last["coverage"])
	}
	for _, key := range []string{"fields", "missingAnyFields"} {
		for _, value := range []any{[]string{}, nil, []string{"status"}, []string{"imageGenerationPrompt", "imageGenerationPrompt"}} {
			_, err := cloudAgentReadStoryboard(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "bad", map[string]any{"nodeId": "board", key: value}))
			if err == nil {
				t.Fatalf("accepted %s=%v", key, value)
			}
		}
	}
}

func TestStoryboardReadMissingFieldsDuration(t *testing.T) {
	rows := []map[string]any{{"id": "valid", "durationSeconds": 5}, {"id": "missing"}, {"id": "zero", "durationSeconds": 0}, {"id": "invalid", "durationSeconds": "5"}, {"id": "too-long", "durationSeconds": 86401}}
	s, canvas := storyboardFieldReadFixture(t, rows)
	result := storyboardFieldRead(t, s, canvas, map[string]any{"fields": []string{"durationSeconds"}, "missingAnyFields": []string{"durationSeconds"}})
	got := creationMaps(result["storyboard"].(map[string]any)["rows"])
	if len(got) != 4 || got[0]["rowId"] != "missing" {
		t.Fatalf("invalid duration must match: %v", got)
	}
}

func TestStoryboardEffectivePromptSource(t *testing.T) {
	rows := []map[string]any{
		{"id": "override", "plotDescription": " fallback ", "imageGenerationPrompt": " image override ", "videoMotionPrompt": " video override "},
		{"id": "fallback", "plotDescription": " fallback ", "imageGenerationPrompt": " \t", "videoMotionPrompt": ""},
		{"id": "none", "plotDescription": "  "},
	}
	s, canvas := storyboardFieldReadFixture(t, rows)
	result := storyboardFieldRead(t, s, canvas, map[string]any{"fields": []string{"imageGenerationPrompt", "videoMotionPrompt"}})
	got := creationMaps(result["storyboard"].(map[string]any)["rows"])
	doc, _ := creationDocument(canvas.PayloadJSON)
	for i, row := range got {
		if row["hasImagePromptOverride"] != (i == 0) || row["hasVideoPromptOverride"] != (i == 0) {
			t.Fatalf("override metadata: %v", row)
		}
		sources, ok := row["effectivePromptSource"].(map[string]any)
		if !ok {
			t.Fatalf("missing sources: %v", row)
		}
		for _, mode := range []string{"image", "video"} {
			resolved, _, err := resolveCloudAgentStoryboardGeneration(doc, cloudAgentMediaArgs{Mode: mode, StoryboardNodeID: "board", StoryboardRowID: row["rowId"].(string)})
			if err != nil {
				t.Fatal(err)
			}
			expectedSource, expectedPrompt := "none", ""
			if i == 0 {
				expectedSource = map[string]string{"image": "imageGenerationPrompt", "video": "videoMotionPrompt"}[mode]
				expectedPrompt = mode + " override"
			}
			if i == 1 {
				expectedSource, expectedPrompt = "plotDescription", "fallback"
			}
			if sources[mode] != expectedSource || resolved.Prompt != expectedPrompt {
				t.Fatalf("%s source/generation disagreement: %v %q", mode, sources, resolved.Prompt)
			}
		}
	}
}

func TestStoryboardReadSelectionCoverage(t *testing.T) {
	long := " \n" + strings.Repeat("镜🙂", 7000) + "\n "
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{{"id": "long", "shotNumber": 1, "imageGenerationPrompt": long}, {"id": "other", "imageGenerationPrompt": "other"}})
	result := storyboardFieldRead(t, s, canvas, map[string]any{"rowIds": []string{"long"}, "fields": []string{"imageGenerationPrompt"}})
	cov := result["coverage"].(map[string]any)
	if cov["selectionComplete"] != true || cov["tableComplete"] != false || cov["complete"] != false || cov["contentTruncated"] != true {
		t.Fatalf("misleading coverage: %v", cov)
	}
	row := creationMaps(result["storyboard"].(map[string]any)["rows"])[0]
	if row["imageGenerationPromptTruncated"] != true {
		t.Fatalf("missing truncation: %v", row)
	}
	prefix := row["imageGenerationPrompt"].(string)
	args := row["nextReads"].(map[string]any)["imageGenerationPrompt"].(map[string]any)["arguments"]
	for {
		page, err := cloudAgentContentRead(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_content", "content", args))
		if err != nil {
			t.Fatal(err)
		}
		body := page.(map[string]any)
		prefix += body["content"].(string)
		if body["hasMore"] == false {
			break
		}
		args = map[string]any{"nodeId": "board", "rowId": "long", "field": "imageGenerationPrompt", "offset": body["nextOffset"], "limit": 8000}
	}
	if prefix != long {
		t.Fatal("continuation did not preserve true prompt")
	}
	raw, _ := json.Marshal(result)
	if len(raw) > cloudAgentReadPageBytes {
		t.Fatalf("page exceeded budget: %d", len(raw))
	}
	whole := storyboardFieldRead(t, s, canvas, map[string]any{"fields": []string{"imageGenerationPrompt"}})
	if whole["coverage"].(map[string]any)["tableComplete"] != true || whole["coverage"].(map[string]any)["contentTruncated"] != true {
		t.Fatalf("whole rows and text coverage conflated: %v", whole["coverage"])
	}
}

func TestStoryboardReadFieldProjectionBudget(t *testing.T) {
	row := map[string]any{"id": "many-fields", "shotNumber": 1, "durationSeconds": 5}
	for _, field := range cloudAgentStoryboardTextFields {
		row[field] = strings.Repeat("镜🙂", 7000)
	}
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{row})
	result := storyboardFieldRead(t, s, canvas, map[string]any{"fields": cloudAgentStoryboardTextFields})
	projected := creationMaps(result["storyboard"].(map[string]any)["rows"])[0]
	for _, field := range cloudAgentStoryboardTextFields {
		text := projected[field].(string)
		if text == "" || projected[field+"Truncated"] != true {
			t.Fatalf("unusable preview for %s", field)
		}
		next := projected["nextReads"].(map[string]any)[field].(map[string]any)["arguments"].(map[string]any)
		if next["offset"] != len([]rune(text)) || next["field"] != field {
			t.Fatalf("incorrect continuation for %s: %v", field, next)
		}
	}
	raw, _ := json.Marshal(result)
	if len(raw) > cloudAgentReadPageBytes {
		t.Fatalf("projection exceeds budget: %d", len(raw))
	}
}

func TestStoryboardReadFieldProjectionOmitsUnrequestedCreativeFields(t *testing.T) {
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{{"id": "one", "shotNumber": 1, "imageGenerationPrompt": "image", "plotDescription": "plot", "mustHave": []any{"private"}, "sourceStartMs": 5, "imagePromptTemplateVariables": map[string]any{"private": "value"}}})
	result := storyboardFieldRead(t, s, canvas, map[string]any{"view": "full", "fields": []string{"imageGenerationPrompt"}})
	row := creationMaps(result["storyboard"].(map[string]any)["rows"])[0]
	for _, field := range []string{"plotDescription", "durationSeconds", "mustHave", "sourceStartMs", "imagePromptTemplateVariables"} {
		if _, exists := row[field]; exists {
			t.Fatalf("projection leaked unrequested creative field %s", field)
		}
	}
}

func TestStoryboardReadSummaryExplicitFields(t *testing.T) {
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{{"id": "one", "imageGenerationPrompt": strings.Repeat("image ", 1000)}})
	plain := storyboardFieldRead(t, s, canvas, map[string]any{"view": "summary"})
	if len(creationMaps(plain["storyboard"].(map[string]any)["rows"])) != 0 {
		t.Fatal("default summary unexpectedly includes rows")
	}
	result := storyboardFieldRead(t, s, canvas, map[string]any{"view": "summary", "fields": []string{"imageGenerationPrompt"}})
	rows := creationMaps(result["storyboard"].(map[string]any)["rows"])
	if len(rows) != 1 || rows[0]["imageGenerationPrompt"] == nil {
		t.Fatalf("summary discarded explicit fields: %v", result["storyboard"])
	}
	if result["coverage"].(map[string]any)["contentTruncated"] != true || rows[0]["nextReads"] == nil {
		t.Fatalf("summary hides truncation: %v", result)
	}
}

func TestStoryboardReadFullExactContinuation(t *testing.T) {
	// The retained prefix ends in real dots; the original also has a literal
	// ellipsis and whitespace at its end, which must survive reconstruction.
	original := " \n" + strings.Repeat("镜🙂", 7997) + "X..." + strings.Repeat("🙂tail", 2000) + "... \n"
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{{"id": "one", "shotNumber": 1, "imageGenerationPrompt": original, "dialogue": " \nshort... \n"}})
	result := storyboardFieldRead(t, s, canvas, map[string]any{"view": "full"})
	row := creationMaps(result["storyboard"].(map[string]any)["rows"])[0]
	prefix := row["imageGenerationPrompt"].(string)
	if !strings.HasPrefix(original, prefix) {
		t.Fatal("full preview contains synthetic text rather than an original prefix")
	}
	if row["dialogue"] != " \nshort... \n" {
		t.Fatal("untruncated literal ellipsis/whitespace changed")
	}
	if result["coverage"].(map[string]any)["contentTruncated"] != true {
		t.Fatal("full text truncation hidden")
	}
	args := row["nextReads"].(map[string]any)["imageGenerationPrompt"].(map[string]any)["arguments"].(map[string]any)
	if args["offset"] != len([]rune(prefix)) {
		t.Fatalf("continuation skips source runes: %v", args)
	}
	for {
		page, err := cloudAgentContentRead(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_content", "full-content", args))
		if err != nil {
			t.Fatal(err)
		}
		body := page.(map[string]any)
		prefix += body["content"].(string)
		if body["hasMore"] == false {
			break
		}
		args = map[string]any{"nodeId": "board", "rowId": "one", "field": "imageGenerationPrompt", "offset": body["nextOffset"], "limit": 8000}
	}
	if prefix != original {
		t.Fatal("full continuation did not reconstruct exact original text")
	}
	raw, _ := json.Marshal(result)
	if len(raw) > cloudAgentReadPageBytes {
		t.Fatalf("full view exceeded existing byte budget: %d", len(raw))
	}
}

func TestStoryboardReadFullExplicitFieldsIgnoresUnrequestedLongText(t *testing.T) {
	row := map[string]any{"id": "one", "shotNumber": 1, "dialogue": " \nrequested dialogue... \n", "imageNodeId": "image-output", "videoNodeId": "video-output", "assetBindings": []any{map[string]any{"nodeId": "asset", "role": "environment", "priority": 1}}}
	for _, field := range []string{"plotDescription", "videoMotionPrompt", "imageGenerationPrompt"} {
		row[field] = strings.Repeat("镜🙂", 10000)
	}
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{row})
	result := storyboardFieldRead(t, s, canvas, map[string]any{"view": "full", "rows": 1, "fields": []string{"dialogue"}})
	projected := creationMaps(result["storyboard"].(map[string]any)["rows"])[0]
	if projected["dialogue"] != row["dialogue"] || projected["rowId"] != "one" || projected["shotNumber"] != float64(1) {
		t.Fatalf("requested projection/handles lost: %v", projected)
	}
	if len(creationMaps(projected["assetBindings"])) != 1 {
		t.Fatal("explicit field projection dropped asset metadata")
	}
	if projected["imageNodeId"] != "image-output" || projected["videoNodeId"] != "video-output" || projected["references"] == nil {
		t.Fatal("explicit field projection dropped output ownership/reference metadata")
	}
	for _, field := range []string{"plotDescription", "videoMotionPrompt", "imageGenerationPrompt"} {
		if _, exists := projected[field]; exists {
			t.Fatalf("unrequested field leaked: %s", field)
		}
	}
}

func TestStoryboardReadFullExplicitFieldsDefaultPage(t *testing.T) {
	rows := make([]map[string]any, 21)
	for i := range rows {
		rows[i] = map[string]any{"id": fmt.Sprintf("row-%02d", i), "shotNumber": i + 1, "dialogue": fmt.Sprintf("dialogue %d", i)}
	}
	s, canvas := storyboardFieldReadFixture(t, rows)
	result := storyboardFieldRead(t, s, canvas, map[string]any{"view": "full", "fields": []string{"dialogue"}})
	board := result["storyboard"].(map[string]any)
	got := creationMaps(board["rows"])
	if len(got) != 1 || got[0]["rowId"] != "row-00" || board["hasMore"] != true {
		t.Fatalf("full explicit fields default must remain one row: count=%d hasMore=%v", len(got), board["hasMore"])
	}
	next := board["nextRead"].(map[string]any)["arguments"].(map[string]any)
	if next["rows"] != 1 || next["offset"] != 1 {
		t.Fatalf("full continuation changed default page size: %v", next)
	}
	continued := storyboardFieldRead(t, s, canvas, next)
	if page := creationMaps(continued["storyboard"].(map[string]any)["rows"]); len(page) != 1 || page[0]["rowId"] != "row-01" {
		t.Fatalf("full continuation lost one-row pagination: %v", page)
	}
}

func TestStoryboardReadFullExhaustedTextBudget(t *testing.T) {
	row := map[string]any{"id": "one", "shotNumber": 1, "dialogue": " \nshort dialogue... \n"}
	for _, field := range []string{"plotDescription", "videoMotionPrompt", "imageGenerationPrompt"} {
		row[field] = " \n" + strings.Repeat("镜a", 9997) + "... "
	}
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{row})
	result := storyboardFieldRead(t, s, canvas, map[string]any{"view": "full"})
	projected := creationMaps(result["storyboard"].(map[string]any)["rows"])[0]
	for _, field := range []string{"plotDescription", "videoMotionPrompt", "imageGenerationPrompt", "dialogue"} {
		original := row[field].(string)
		text := projected[field].(string)
		if !strings.HasPrefix(original, text) {
			t.Fatalf("%s preview is not original prefix", field)
		}
		reads, _ := projected["nextReads"].(map[string]any)
		if read, exists := reads[field]; exists {
			args := read.(map[string]any)["arguments"].(map[string]any)
			if args["offset"] != len([]rune(text)) {
				t.Fatalf("wrong %s continuation offset: %v", field, args)
			}
			for {
				page, err := cloudAgentContentRead(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_content", "exhausted-content", args))
				if err != nil {
					t.Fatal(err)
				}
				body := page.(map[string]any)
				text += body["content"].(string)
				if body["hasMore"] == false {
					break
				}
				args = map[string]any{"nodeId": "board", "rowId": "one", "field": field, "offset": body["nextOffset"], "limit": 8000}
			}
		}
		if text != original {
			t.Fatalf("%s continuation lost original text after exhausted budget", field)
		}
	}
	raw, _ := json.Marshal(result)
	if len(raw) > cloudAgentReadPageBytes {
		t.Fatalf("default full budget exceeded: %d", len(raw))
	}
}
