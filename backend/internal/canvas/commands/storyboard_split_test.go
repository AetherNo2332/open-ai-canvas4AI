package commands

import (
	"reflect"
	"strings"
	"testing"
)

func splitBoardDoc(t *testing.T) Document {
	t.Helper()
	return fixture(t, `{"id":"canvas","nodes":[
		{"id":"board","type":"script","title":"分镜","metadata":{"storyboard":{"rows":[
			{"id":"r1","shotNumber":1,"durationSeconds":4,"plotDescription":"开门","imageNodeId":"img1"},
			{"id":"r2","shotNumber":2,"durationSeconds":4,"plotDescription":"对视","imageNodeId":"img2","videoNodeId":"vid2"},
			{"id":"r3","shotNumber":3,"durationSeconds":4,"plotDescription":"转身","locked":true,"imageNodeId":"img3"},
			{"id":"r4","shotNumber":4,"durationSeconds":4,"plotDescription":"离开"}
		],"referenceNodeIds":[]}}},
		{"id":"img1","type":"image"},{"id":"img2","type":"image"},{"id":"vid2","type":"video"},{"id":"img3","type":"image"}
	],"connections":[
		{"id":"e2","fromNodeId":"img2","toNodeId":"board","storyboardRowId":"r2"},
		{"id":"e3","fromNodeId":"img3","toNodeId":"board","storyboardRowId":"r3"}
	]}`)
}

func splitRows(t *testing.T, doc Document) []map[string]any {
	t.Helper()
	node := entity(doc, "node:board")
	return maps(node["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"])
}

func splitRowIDs(rows []map[string]any) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, text(row["id"]))
	}
	return ids
}

// 整表重拆：锁定行留在原位置不变，其余行换成新行；被替换行的媒体只解绑，节点留在画布上。
func TestStoryboardSplitWholeKeepsLockedRows(t *testing.T) {
	doc := splitBoardDoc(t)
	raw := `{"title":"新分镜","rows":[{"durationSeconds":3,"plotDescription":"新一"},{"durationSeconds":3,"plotDescription":"新二"},{"durationSeconds":3,"plotDescription":"新三"},{"durationSeconds":3,"plotDescription":"新四"}]}`
	next, result, err := ApplyStoryboardSplit(doc, "board", "task", raw, StoryboardSplitScope{})
	if err != nil {
		t.Fatal(err)
	}
	rows := splitRows(t, next)
	if got := splitRowIDs(rows); !reflect.DeepEqual(got, []string{"shot-task-1", "shot-task-2", "r3", "shot-task-3", "shot-task-4"}) {
		t.Fatalf("rows = %v", got)
	}
	if rows[2]["plotDescription"] != "转身" || rows[2]["imageNodeId"] != "img3" || rows[2]["shotNumber"] != float64(3) {
		t.Fatalf("locked row changed: %+v", rows[2])
	}
	if !reflect.DeepEqual(result.UnboundMedia, []string{"img1", "img2", "vid2"}) || !reflect.DeepEqual(result.LockedRowIDs, []string{"r3"}) {
		t.Fatalf("result = %+v", result)
	}
	if entity(next, "node:img2") == nil || entity(next, "node:vid2") == nil {
		t.Fatal("unbound media nodes must stay on the canvas")
	}
	for _, edge := range maps(next["connections"]) {
		if text(edge["storyboardRowId"]) == "r2" {
			t.Fatal("row edge of a replaced row must be removed")
		}
	}
	if entity(next, "node:board")["title"] != "新分镜" {
		t.Fatal("whole split updates the title")
	}
}

// 局部重拆、新旧行数相同：一一对应保留旧行 ID 和媒体，文字变了的行媒体标为待复核；范围外不动。
func TestStoryboardSplitRangeSameCountKeepsIdentityAndMarksReview(t *testing.T) {
	doc := splitBoardDoc(t)
	raw := `{"rows":[{"durationSeconds":4,"plotDescription":"开门"},{"durationSeconds":5,"plotDescription":"对视，停顿"}]}`
	next, result, err := ApplyStoryboardSplit(doc, "board", "task", raw, StoryboardSplitScope{RowIDs: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	rows := splitRows(t, next)
	if got := splitRowIDs(rows); !reflect.DeepEqual(got, []string{"r1", "r2", "r3", "r4"}) {
		t.Fatalf("rows = %v", got)
	}
	if rows[0]["imageNodeId"] != "img1" || rows[0]["mediaReview"] != nil {
		t.Fatalf("unchanged row must keep media without review: %+v", rows[0])
	}
	if rows[1]["imageNodeId"] != "img2" || rows[1]["videoNodeId"] != "vid2" || rows[1]["mediaReview"] != storyboardMediaReviewPending || rows[1]["plotDescription"] != "对视，停顿" {
		t.Fatalf("changed row must keep media and be marked for review: %+v", rows[1])
	}
	if !reflect.DeepEqual(result.ReviewRowIDs, []string{"r2"}) || len(result.UnboundMedia) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if rows[3]["plotDescription"] != "离开" {
		t.Fatal("rows outside the range must not change")
	}
}

// 局部重拆、行数变化：新行用新 ID 且没有媒体，旧媒体解绑但留在画布；范围外的行与连线不动。
func TestStoryboardSplitRangeDifferentCountUnbindsMedia(t *testing.T) {
	doc := splitBoardDoc(t)
	raw := `{"rows":[{"durationSeconds":2,"plotDescription":"开门"},{"durationSeconds":2,"plotDescription":"她回头"},{"durationSeconds":2,"plotDescription":"对视"}]}`
	next, result, err := ApplyStoryboardSplit(doc, "board", "task", raw, StoryboardSplitScope{RowIDs: []string{"r2", "r1"}})
	if err != nil {
		t.Fatal(err)
	}
	rows := splitRows(t, next)
	if got := splitRowIDs(rows); !reflect.DeepEqual(got, []string{"shot-task-1", "shot-task-2", "shot-task-3", "r3", "r4"}) {
		t.Fatalf("rows = %v", got)
	}
	for _, row := range rows[:3] {
		if storyboardRowHasMedia(row) {
			t.Fatalf("new rows must not inherit media: %+v", row)
		}
	}
	if !reflect.DeepEqual(result.UnboundMedia, []string{"img1", "img2", "vid2"}) || !reflect.DeepEqual(result.Rows[0].Replaced, []string{"r1", "r2"}) {
		t.Fatalf("result = %+v", result)
	}
	keptEdge := false
	for _, edge := range maps(next["connections"]) {
		if text(edge["storyboardRowId"]) == "r2" {
			t.Fatal("edge of a replaced row must be removed")
		}
		keptEdge = keptEdge || text(edge["id"]) == "e3"
	}
	if !keptEdge || rows[3]["shotNumber"] != float64(4) {
		t.Fatal("rows and edges outside the range must stay; shot numbers renumber")
	}
}

func TestStoryboardSplitScopeRejectsLockedOrGappedRows(t *testing.T) {
	doc := splitBoardDoc(t)
	for name, ids := range map[string][]string{
		"locked":  {"r2", "r3"},
		"gap":     {"r1", "r4"},
		"missing": {"nope"},
	} {
		if _, err := PlanStoryboardSplitScope(doc, "board", ids); err == nil {
			t.Fatalf("%s scope must be rejected", name)
		} else if name == "locked" && !strings.Contains(err.Error(), "锁定") {
			t.Fatalf("locked scope error must name the lock: %v", err)
		}
	}
	scope, err := PlanStoryboardSplitScope(doc, "board", []string{"r2", "r1"})
	if err != nil || !reflect.DeepEqual(scope.RowIDs, []string{"r1", "r2"}) {
		t.Fatalf("scope = %+v %v", scope, err)
	}
}
