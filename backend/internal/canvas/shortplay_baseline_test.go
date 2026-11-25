package canvas

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func shortplayFixtureRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "shortplay-canvas-port")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("fixture root %s: %v", root, err)
	}
	return root
}

func shortplayReadJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func shortplayMap(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s has type %T, want object", label, value)
	}
	return result
}

func shortplayList(t *testing.T, value any, label string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s has type %T, want array", label, value)
	}
	return result
}

func shortplayStrings(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func shortplayNode(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range shortplayList(t, document["nodes"], "document.nodes") {
		node := shortplayMap(t, raw, "document.nodes[]")
		if node["id"] == id {
			return node
		}
	}
	t.Fatalf("node %s not found", id)
	return nil
}

func shortplayMetadata(t *testing.T, node map[string]any) map[string]any {
	t.Helper()
	return shortplayMap(t, node["metadata"], "node.metadata")
}

func TestShortplayS01DonorSubmapBaseline(t *testing.T) {
	root := shortplayFixtureRoot(t)
	var provenance map[string]any
	shortplayReadJSON(t, filepath.Join(root, "donor", "provenance.json"), &provenance)
	if provenance["evidence"] != "runtime" {
		t.Fatalf("donor evidence = %v, want runtime", provenance["evidence"])
	}
	donor := shortplayMap(t, provenance["donor"], "provenance.donor")
	if donor["commit"] != "13731aae6c4cefadc2a1719e3e7e59b22e249f94" {
		t.Fatalf("donor commit = %v", donor["commit"])
	}

	var duplicated map[string]any
	shortplayReadJSON(t, filepath.Join(root, "donor", "duplicated.json"), &duplicated)
	document := shortplayMap(t, duplicated["document"], "duplicated.document")
	order := shortplayStrings(duplicated["nodeOrder"])
	wantOrder := []string{
		"submap-1", "submap-1__text-1", "submap-1__text-2", "submap-1__text-3",
		"submap-1__text-4", "submap-1__image-1", "submap-1__video-1",
		"image-1", "text-1", "submap-2", "submap-2__text-1", "submap-2__text-2",
		"submap-2__text-3", "submap-2__text-4", "submap-2__image-1", "submap-2__video-1",
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("node order = %v, want %v", order, wantOrder)
	}
	if connections, ok := duplicated["connections"].([]any); ok && len(connections) != 0 {
		t.Fatalf("donor persisted connections should be field-derived, got %v", connections)
	}

	image := shortplayNode(t, document, "submap-1__image-1")
	if image["scopeId"] != "submap-1" {
		t.Fatalf("image scopeId = %v", image["scopeId"])
	}
	if _, exists := image["parentId"]; exists {
		t.Fatal("submap children must not persist parentId")
	}
	if got := shortplayMetadata(t, image)["generationSpec"].(map[string]any)["prompt"]; got != "雨夜霓虹下的女主角，湿发，近景" {
		t.Fatalf("edited image prompt = %v", got)
	}
	video := shortplayNode(t, document, "submap-1__video-1")
	videoMetadata := shortplayMetadata(t, video)
	if videoMetadata["videoStartFrameNodeId"] != "submap-1__image-1" {
		t.Fatalf("original start frame = %v", videoMetadata["videoStartFrameNodeId"])
	}
	if got := shortplayStrings(videoMetadata["referenceTextNodeIds"]); !reflect.DeepEqual(got, []string{
		"submap-1__text-1", "submap-1__text-2", "submap-1__text-3", "text-1",
	}) {
		t.Fatalf("original text reference order = %v", got)
	}

	copied := shortplayNode(t, document, "submap-2__video-1")
	copiedMetadata := shortplayMetadata(t, copied)
	if copiedMetadata["videoStartFrameNodeId"] != "submap-2__image-1" {
		t.Fatalf("copied start frame = %v", copiedMetadata["videoStartFrameNodeId"])
	}
	if got := shortplayStrings(copiedMetadata["referenceNodeIds"]); !reflect.DeepEqual(got, []string{"submap-2__image-1"}) {
		t.Fatalf("copied media references = %v", got)
	}
	if got := shortplayStrings(copiedMetadata["referenceTextNodeIds"]); !reflect.DeepEqual(got, []string{
		"submap-2__text-1", "submap-2__text-2", "submap-2__text-3",
	}) {
		t.Fatalf("copied text references = %v", got)
	}
	copiedImage := shortplayNode(t, document, "submap-2__image-1")
	if copiedImage["scopeId"] != "submap-2" {
		t.Fatalf("copied image scopeId = %v", copiedImage["scopeId"])
	}
	if metadata := shortplayMetadata(t, copiedImage); metadata["generationSpec"].(map[string]any)["prompt"] != "雨夜霓虹下的女主角，湿发，近景" {
		t.Fatal("copy did not retain the edited prompt")
	}

	var operations map[string]any
	shortplayReadJSON(t, filepath.Join(root, "donor", "operations.json"), &operations)
	if operations["reopenEqual"] != true {
		t.Fatal("fresh repository reopen must match the persisted payload")
	}
	commands := make([]string, 0, len(shortplayList(t, operations["operations"], "operations")))
	for _, raw := range shortplayList(t, operations["operations"], "operations") {
		operation := shortplayMap(t, raw, "operations[]")
		commands = append(commands, operation["command"].(string))
	}
	for _, want := range []string{
		"new submap --workflow storyboard 镜头1",
		"cat > ./submap-1/image-1/prompt.txt <<'EOF'",
		"edit ./submap-1/video-1/videoStartFrameNodeId.txt image-1",
		"edit ./submap-1/video-1/videoStartFrameNodeId.txt submap-1__image-1",
		"dup ./submap-1 镜头2",
	} {
		found := false
		for _, command := range commands {
			if command == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("operation %q missing from donor fixture", want)
		}
	}
	fieldListing, _ := operations["fieldListing"].(string)
	for _, field := range []string{"videoStartFrameNodeId.txt", "referenceNodeIds.list", "referenceTextNodeIds.list"} {
		if !strings.Contains(fieldListing, field) {
			t.Fatalf("field listing missing %s", field)
		}
	}
	registered := shortplayList(t, operations["registeredTypes"], "operations.registeredTypes")
	knownTypes := map[string]bool{}
	for _, raw := range registered {
		knownTypes[shortplayMap(t, raw, "registeredTypes[]")["type"].(string)] = true
	}
	for _, nodeType := range []string{"submap", "text", "image", "video"} {
		if !knownTypes[nodeType] {
			t.Fatalf("registered capability %s missing", nodeType)
		}
	}
	typeListing, _ := operations["typeListing"].(string)
	if !strings.Contains(typeListing, "submap") || !strings.Contains(typeListing, "video") {
		t.Fatalf("runtime type listing is incomplete: %q", typeListing)
	}
}

type shortplayLegacyResource struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	MimeType   string `json:"mimeType"`
	Size       int64  `json:"size"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	DurationMs int64  `json:"durationMs"`
}

type shortplayLegacyAsset struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Status  string         `json:"status"`
	Title   string         `json:"title"`
	Payload map[string]any `json:"payload"`
}

type shortplayLegacyResources struct {
	Owner     string                    `json:"owner"`
	Resources []shortplayLegacyResource `json:"resources"`
	Assets    []shortplayLegacyAsset    `json:"assets"`
}

func TestShortplayS01LegacyPayloadRoundTrip(t *testing.T) {
	root := shortplayFixtureRoot(t)
	var payload map[string]any
	shortplayReadJSON(t, filepath.Join(root, "legacy", "payload-with-extensions.json"), &payload)
	var resources shortplayLegacyResources
	shortplayReadJSON(t, filepath.Join(root, "legacy", "resources.json"), &resources)

	svc := newCanvasOperationTestService(t)
	for _, item := range resources.Resources {
		resource := model.Resource{
			ID: item.ID, UserID: resources.Owner, Kind: item.Kind,
			Status: model.ResourceStatus(item.Status), MimeType: item.MimeType,
			Size: item.Size, Width: item.Width, Height: item.Height, DurationMs: item.DurationMs,
		}
		if err := svc.repo.Create(&resource); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range resources.Assets {
		raw, err := json.Marshal(item.Payload)
		if err != nil {
			t.Fatal(err)
		}
		asset := model.Asset{
			ID: item.ID, UserID: resources.Owner, Kind: item.Kind,
			Status: model.AssetVersionStatusConfirmed, Title: item.Title, PayloadJSON: string(raw),
		}
		if err := svc.repo.Create(&asset); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	project := model.CanvasProject{
		ID: "legacy-shortplay-001", UserID: resources.Owner,
		Title: "旧分镜与批量表样本", PayloadJSON: string(raw),
	}
	if err := svc.repo.UpsertCanvasProject(&project); err != nil {
		t.Fatalf("write legacy payload through target repository: %v", err)
	}
	stored, err := svc.repo.CanvasProjectForUser(resources.Owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal([]byte(stored.PayloadJSON), &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload, restored) {
		t.Fatalf("target repository changed the legacy payload:\nwant=%#v\ngot=%#v", payload, restored)
	}
	if stored.Revision != 1 {
		t.Fatalf("stored revision = %d, want 1", stored.Revision)
	}

	script := shortplayNode(t, restored, "script-1")
	storyboard := shortplayMetadata(t, script)["storyboard"].(map[string]any)
	rows := shortplayList(t, storyboard["rows"], "storyboard.rows")
	if len(rows) != 2 {
		t.Fatalf("storyboard rows = %d, want 2", len(rows))
	}
	row1 := shortplayMap(t, rows[0], "storyboard.rows[0]")
	row2 := shortplayMap(t, rows[1], "storyboard.rows[1]")
	if row1["imageNodeId"] != "image-frame-1" || row1["videoNodeId"] != "video-existing" {
		t.Fatalf("row-1 output bindings = %v", row1)
	}
	if row2["imageNodeId"] != "image-frame-2" {
		t.Fatalf("row-2 image binding = %v", row2["imageNodeId"])
	}
	if row1["unknown-row-extension"] == nil || row2["unknown-row-extension"] == nil {
		t.Fatal("unknown storyboard row fields were dropped")
	}
	if storyboard["unknown-storyboard-extension"] == nil {
		t.Fatal("unknown storyboard field was dropped")
	}

	table := shortplayNode(t, restored, "batch-table-1")
	batch := shortplayMetadata(t, table)["batchTable"].(map[string]any)
	columns := shortplayList(t, batch["textColumns"], "batchTable.textColumns")
	if len(columns) != 2 {
		t.Fatalf("batch text columns = %d, want 2", len(columns))
	}
	batchRows := shortplayList(t, batch["rows"], "batchTable.rows")
	batchRow1 := shortplayMap(t, batchRows[0], "batchTable.rows[0]")
	batchRow2 := shortplayMap(t, batchRows[1], "batchTable.rows[1]")
	cells := shortplayMap(t, batchRow1["cells"], "batchTable.rows[0].cells")
	if cells["col-description"] == "" || cells["col-dialogue"] == "" {
		t.Fatalf("batch cells lost explicit values: %v", cells)
	}
	emptyCells := shortplayMap(t, batchRow2["cells"], "batchTable.rows[1].cells")
	if _, explicit := emptyCells["col-description"]; !explicit || emptyCells["col-description"] != "" {
		t.Fatalf("explicit empty column override lost: %v", emptyCells)
	}
	if batchRow1["textNodeIds"] == nil || batchRow1["referenceOverrides"] == nil {
		t.Fatal("batch row textNodeIds / referenceOverrides lost")
	}
	if batch["unknown-table-extension"] == nil {
		t.Fatal("unknown batch-table field was dropped")
	}

	connections := shortplayList(t, restored["connections"], "legacy.connections")
	ports := map[string]bool{}
	for _, raw := range connections {
		connection := shortplayMap(t, raw, "legacy.connections[]")
		if connection["fromHandleId"] != nil {
			ports[connection["fromHandleId"].(string)] = true
		}
		if connection["toHandleId"] != nil {
			ports[connection["toHandleId"].(string)] = true
		}
	}
	for _, port := range []string{"row:row-1", "row:row-2", "batch-text:col-description", "batch-text:col-dialogue"} {
		if !ports[port] {
			t.Fatalf("legacy port %q missing from stored connections", port)
		}
	}
	for _, id := range []string{"frame-1", "frame-2", "video-1", "character-1", "batch-output"} {
		resource, err := svc.repo.ResourceForUser(resources.Owner, id)
		if err != nil || resource.ID != id {
			t.Fatalf("resource owner lookup %s: %v", id, err)
		}
	}
}

func TestShortplayS01ZipVersionRelationships(t *testing.T) {
	root := shortplayFixtureRoot(t)
	var v3, v4, entries map[string]any
	shortplayReadJSON(t, filepath.Join(root, "zip", "projects-v3.json"), &v3)
	shortplayReadJSON(t, filepath.Join(root, "zip", "projects-v4.json"), &v4)
	shortplayReadJSON(t, filepath.Join(root, "zip", "archive-entries.json"), &entries)
	if v3["app"] != "infinite-canvas" || v3["version"].(float64) != 3 {
		t.Fatalf("v3 manifest identity = %v/%v", v3["app"], v3["version"])
	}
	if v4["app"] != "yingce" || v4["version"].(float64) != 4 {
		t.Fatalf("v4 manifest identity = %v/%v", v4["app"], v4["version"])
	}
	v3Project := shortplayMap(t, shortplayList(t, v3["projects"], "v3.projects")[0], "v3.projects[0]")
	v4Project := shortplayMap(t, shortplayList(t, v4["projects"], "v4.projects")[0], "v4.projects[0]")
	v3Drawing := shortplayMap(t, shortplayList(t, v3Project["drawingDocuments"], "v3.drawingDocuments")[0], "v3.drawingDocuments[0]")
	v4Drawing := shortplayMap(t, shortplayList(t, v4Project["drawingDocuments"], "v4.drawingDocuments")[0], "v4.drawingDocuments[0]")
	if v3Drawing["version"].(float64) != 1 || v3Drawing["engine"] != nil {
		t.Fatalf("v3 drawing document = %v", v3Drawing)
	}
	if v4Drawing["version"].(float64) != 2 || v4Drawing["engine"] != "tldraw" {
		t.Fatalf("v4 drawing document = %v", v4Drawing)
	}
	for _, raw := range shortplayList(t, entries["entries"], "archive entries") {
		entry := shortplayMap(t, raw, "archive entries[]")
		if entry["base64"] == "" {
			t.Fatalf("archive entry %v has no bytes", entry["path"])
		}
	}
}

func TestShortplayS01TargetTiming(t *testing.T) {
	if os.Getenv("SHORTPLAY_S01_MEASURE") != "1" {
		t.Skip("set SHORTPLAY_S01_MEASURE=1 to collect local timing")
	}
	const nodeCount = 200
	const rounds = 10
	svc := newCanvasOperationTestService(t)
	nodes := make([]map[string]any, 0, nodeCount)
	for index := 0; index < nodeCount; index++ {
		nodes = append(nodes, map[string]any{
			"id": fmt.Sprintf("timing-node-%03d", index), "type": "text",
			"title": fmt.Sprintf("节点 %d", index), "position": map[string]any{"x": index * 20, "y": index * 10},
			"width": 340, "height": 220,
			"metadata": map[string]any{"content": strings.Repeat("基准文本", 20), "status": "idle"},
		})
	}
	raw, err := json.Marshal(map[string]any{"id": "timing-canvas", "title": "200 节点基准", "nodes": nodes})
	if err != nil {
		t.Fatal(err)
	}
	project := &model.CanvasProject{ID: "timing-canvas", UserID: "user", Title: "200 节点基准", PayloadJSON: string(raw)}
	if err := svc.repo.UpsertCanvasProject(project); err != nil {
		t.Fatal(err)
	}
	openTimes := make([]time.Duration, 0, rounds)
	saveTimes := make([]time.Duration, 0, rounds)
	for round := 0; round < rounds; round++ {
		start := time.Now()
		row, err := svc.repo.CanvasProjectForUser("user", project.ID)
		openTimes = append(openTimes, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
		row.Title = fmt.Sprintf("200 节点基准 %d", round)
		start = time.Now()
		if err := svc.repo.UpsertCanvasProject(row); err != nil {
			t.Fatal(err)
		}
		saveTimes = append(saveTimes, time.Since(start))
	}
	t.Logf("nodes=%d rounds=%d payload_bytes=%d open_avg=%s open_max=%s save_avg=%s save_max=%s",
		nodeCount, rounds, len(raw), shortplayAverage(openTimes), shortplayMax(openTimes),
		shortplayAverage(saveTimes), shortplayMax(saveTimes))
}

func shortplayAverage(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total / time.Duration(len(values))
}

func shortplayMax(values []time.Duration) time.Duration {
	var result time.Duration
	for _, value := range values {
		if value > result {
			result = value
		}
	}
	return result
}
