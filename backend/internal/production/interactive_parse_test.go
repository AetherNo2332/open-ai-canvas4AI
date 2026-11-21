package production

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const interactiveValidRaw = `{
  "title": "客栈分岔",
  "rows": [
    {"index": 1, "text": "李四在客栈角落喝酒，王五推门进来。", "characters": ["李四", "王五"], "scene": "客栈大堂", "next": [{"target": 2, "option": "看信", "suggested": false}, {"target": 3, "option": "不看信", "suggested": false}], "ending": false, "suggested": false},
    {"index": 2, "text": "李四接过信，看完起身离开。", "characters": ["李四"], "scene": "客栈大堂", "next": [{"target": 4, "option": "继续", "suggested": false}], "ending": false, "suggested": false},
    {"index": 3, "text": "李四把信推回，继续喝酒。", "characters": ["李四", "王五"], "scene": "客栈大堂", "next": [{"target": 5, "option": "继续", "suggested": false}], "ending": false, "suggested": false},
    {"index": 4, "text": "李四按地址找到仇人，决斗。", "characters": ["李四"], "scene": "城外荒庙", "next": [], "ending": true, "suggested": false},
    {"index": 5, "text": "李四醉倒客栈，错过时机。", "characters": ["李四"], "scene": "客栈大堂", "next": [], "ending": true, "suggested": false}
  ]
}`

func TestParseInteractiveScriptValidTwoDestinations(t *testing.T) {
	script, issues, err := ParseInteractiveScript(interactiveValidRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("valid script must have no issues, got %+v", issues)
	}
	if len(script.Rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(script.Rows))
	}
	if script.Rows[0].Next[0].Target != 2 || script.Rows[0].Next[1].Target != 3 {
		t.Fatalf("two destinations must be preserved: %+v", script.Rows[0].Next)
	}
	if !script.Rows[3].Ending || len(script.Rows[3].Next) != 0 {
		t.Fatalf("ending row must be marked and have no next: %+v", script.Rows[3])
	}
	if len(script.Cast.Characters) != 2 || script.Cast.Characters[0] != "李四" || script.Cast.Characters[1] != "王五" {
		t.Fatalf("cast characters must dedupe in first-seen order: %+v", script.Cast.Characters)
	}
	if len(script.Cast.Scenes) != 2 || script.Cast.Scenes[0] != "客栈大堂" || script.Cast.Scenes[1] != "城外荒庙" {
		t.Fatalf("cast scenes must dedupe in first-seen order: %+v", script.Cast.Scenes)
	}
}

func TestParseInteractiveScriptToleratesMarkdownFences(t *testing.T) {
	raw := "好的，拆分结果如下：\n```json\n" + interactiveValidRaw + "\n```"
	if _, issues, err := ParseInteractiveScript(raw); err != nil || len(issues) != 0 {
		t.Fatalf("markdown fences must be tolerated, err=%v issues=%+v", err, issues)
	}
}

func TestParseInteractiveScriptMissingTextLocatesRow(t *testing.T) {
	raw := strings.Replace(interactiveValidRaw, `"text": "李四接过信，看完起身离开。"`, `"text": ""`, 1)
	script, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("want exactly one issue, got %+v", issues)
	}
	if issues[0].Row != 2 || issues[0].Code != "missing_text" {
		t.Fatalf("missing text must be located at row 2: %+v", issues[0])
	}
	if len(script.Cast.Characters) != 0 {
		t.Fatalf("invalid table must not expose cast: %+v", script.Cast)
	}
}

func TestParseInteractiveScriptDuplicateIndex(t *testing.T) {
	raw := strings.Replace(interactiveValidRaw, `"index": 3,`, `"index": 2,`, 1)
	_, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, "duplicate_index") {
		t.Fatalf("duplicate index must be reported: %+v", issues)
	}
}

func TestParseInteractiveScriptTargetMissing(t *testing.T) {
	raw := strings.Replace(interactiveValidRaw, `{"target": 4, "option": "继续", "suggested": false}`, `{"target": 9, "option": "继续", "suggested": false}`, 1)
	_, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Row != 2 || issues[0].Code != "target_missing" {
		t.Fatalf("missing target must be located at the row owning the edge: %+v", issues)
	}
}

func TestParseInteractiveScriptEndingHasNext(t *testing.T) {
	raw := strings.Replace(interactiveValidRaw, `"next": [], "ending": true, "suggested": false},
    {"index": 5`, `"next": [{"target": 5, "option": "继续", "suggested": false}], "ending": true, "suggested": false},
    {"index": 5`, 1)
	_, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, "ending_has_next") {
		t.Fatalf("ending row with next must be reported: %+v", issues)
	}
}

func TestParseInteractiveScriptDeadEnd(t *testing.T) {
	raw := strings.Replace(interactiveValidRaw, `"next": [{"target": 5, "option": "继续", "suggested": false}], "ending": false`, `"next": [], "ending": false`, 1)
	_, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, "dead_end") {
		t.Fatalf("non-ending row without next must be reported: %+v", issues)
	}
}

func TestParseInteractiveScriptMissingEnding(t *testing.T) {
	raw := strings.Replace(interactiveValidRaw, `"ending": true`, `"ending": false`, -1)
	_, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, "missing_ending") {
		t.Fatalf("table without any ending must be reported: %+v", issues)
	}
}

func TestParseInteractiveScriptMissingOption(t *testing.T) {
	raw := strings.Replace(interactiveValidRaw, `"option": "继续", "suggested": false}], "ending": false, "suggested": false},
    {"index": 4`, `"option": "", "suggested": false}], "ending": false, "suggested": false},
    {"index": 4`, 1)
	_, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, "missing_option") {
		t.Fatalf("edge without option must be reported: %+v", issues)
	}
}

func TestParseInteractiveScriptNormalizesWhitespaceAndDuplicates(t *testing.T) {
	raw := `{"title":" 标题 ","rows":[
		{"index":1,"text":" 正文 ","characters":[" 李四 ","李四","王五"],"scene":" 客栈 ","next":[{"target":2,"option":" 去 ","suggested":false}],"ending":false,"suggested":false},
		{"index":2,"text":"结局","characters":["李四"],"scene":"客栈","next":[],"ending":true,"suggested":false}]}`
	script, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("normalization must not produce issues: %+v", issues)
	}
	row := script.Rows[0]
	if row.Text != "正文" || row.Scene != "客栈" {
		t.Fatalf("fields must be trimmed: %+v", row)
	}
	if len(row.Characters) != 2 {
		t.Fatalf("characters must dedupe: %+v", row.Characters)
	}
	if row.Next[0].Option != "去" {
		t.Fatalf("edge option must be trimmed: %+v", row.Next[0])
	}
}

func TestParseInteractiveScriptRejectsBrokenJSON(t *testing.T) {
	if _, _, err := ParseInteractiveScript("没有 JSON"); err == nil {
		t.Fatal("broken JSON must return error")
	}
}

func TestInteractiveSplitPromptEmbedsValidJSONTemplate(t *testing.T) {
	prompt := InteractiveSplitPrompt("测试作品", "保留原著结尾", "第一幕\n李四喝酒。")
	start := strings.Index(prompt, `{"title":`)
	if start < 0 {
		t.Fatal("prompt must embed the output JSON template")
	}
	end := strings.LastIndex(prompt, "}")
	var probe map[string]any
	if err := json.Unmarshal([]byte(prompt[start:end+1]), &probe); err != nil {
		t.Fatalf("embedded JSON template must be valid: %v", err)
	}
	if !strings.Contains(prompt, "已有分支保留原选项") && !strings.Contains(prompt, "原文已有的分支必须保留") {
		t.Fatal("prompt must instruct preserving existing branches")
	}
	if !strings.Contains(prompt, "建议") {
		t.Fatal("prompt must mark new branches as suggestions")
	}
}

func TestParseInteractiveScriptRowOverflowRejected(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"title":"超长表","rows":[`)
	for i := 1; i <= InteractiveMaxRows+1; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		next := fmt.Sprintf(`[{"target":%d,"option":"继续","suggested":false}]`, i+1)
		ending := "false"
		if i == InteractiveMaxRows+1 {
			next = "[]"
			ending = "true"
		}
		fmt.Fprintf(&b, `{"index":%d,"text":"第%d段","characters":[],"scene":"客栈","next":%s,"ending":%s,"suggested":false}`, i, i, next, ending)
	}
	b.WriteString("]}")
	_, issues, err := ParseInteractiveScript(b.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Code != "too_many_rows" || issues[0].Row != 0 {
		t.Fatalf("over-limit table must be rejected as a whole, not truncated: %+v", issues)
	}
}

func TestParseInteractiveScriptHugeIndexDoesNotHang(t *testing.T) {
	raw := `{"title":"坏行号","rows":[
		{"index":1,"text":"开场","characters":["李四"],"scene":"客栈","next":[{"target":2,"option":"继续","suggested":false}],"ending":false,"suggested":false},
		{"index":1000000000,"text":"结局","characters":["李四"],"scene":"客栈","next":[],"ending":true,"suggested":false}]}`
	start := time.Now()
	_, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("validation must not scan to an arbitrary row index, took %s", elapsed)
	}
	if !hasIssue(issues, "index_out_of_range") {
		t.Fatalf("out-of-range row index must be reported: %+v", issues)
	}
}

func TestParseInteractiveScriptReportsCapsWithoutTruncation(t *testing.T) {
	names := make([]string, 0, InteractiveMaxCharacters+1)
	for i := 0; i <= InteractiveMaxCharacters; i++ {
		names = append(names, fmt.Sprintf("角色%d", i))
	}
	edges := make([]string, 0, InteractiveMaxEdges+1)
	for i := 0; i <= InteractiveMaxEdges; i++ {
		edges = append(edges, fmt.Sprintf(`{"target":2,"option":"去%d","suggested":false}`, i))
	}
	raw := fmt.Sprintf(`{"title":"超限","rows":[
		{"index":1,"text":"开场","characters":["%s"],"scene":"客栈","next":[%s],"ending":false,"suggested":false},
		{"index":2,"text":"结局","characters":[],"scene":"客栈","next":[],"ending":true,"suggested":false}]}`,
		strings.Join(names, `","`), strings.Join(edges, ","))
	script, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, "too_many_characters") || !hasIssue(issues, "too_many_edges") {
		t.Fatalf("over-limit fields must be reported: %+v", issues)
	}
	if len(script.Rows[0].Characters) != InteractiveMaxCharacters+1 || len(script.Rows[0].Next) != InteractiveMaxEdges+1 {
		t.Fatalf("over-limit fields must be preserved for the editor, not trimmed: %+v", script.Rows[0])
	}
}

func TestParseInteractiveScriptDedupesNamesLikeCastIndex(t *testing.T) {
	raw := `{"title":"归一","rows":[
		{"index":1,"text":"开场","characters":["李 四","李四","李四！","LiSi"],"scene":"客栈","next":[{"target":2,"option":"继续","suggested":false}],"ending":false,"suggested":false},
		{"index":2,"text":"结局","characters":[],"scene":"城 外 荒 庙","next":[],"ending":true,"suggested":false}]}`
	script, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("normalization must not produce issues: %+v", issues)
	}
	if len(script.Rows[0].Characters) != 2 {
		t.Fatalf("names must dedupe with CastIndex rules: %+v", script.Rows[0].Characters)
	}
	if len(script.Cast.Scenes) != 2 {
		t.Fatalf("scenes must dedupe with CastIndex rules: %+v", script.Cast.Scenes)
	}
}

func TestParseInteractiveScriptPreservesSuggestedFlags(t *testing.T) {
	raw := `{"title":"建议分支","rows":[
		{"index":1,"text":"李四在客栈等人。","characters":["李四"],"scene":"客栈","next":[{"target":2,"option":"留下等","suggested":false},{"target":3,"option":"出门找","suggested":true}],"ending":false,"suggested":false},
		{"index":2,"text":"李四等到了王五。","characters":["李四","王五"],"scene":"客栈","next":[{"target":4,"option":"继续","suggested":false}],"ending":false,"suggested":false},
		{"index":3,"text":"（建议新增）李四在街上撞见旧识。","characters":["李四"],"scene":"长街","next":[{"target":4,"option":"继续","suggested":true}],"ending":false,"suggested":true},
		{"index":4,"text":"故事结束。","characters":[],"scene":"客栈","next":[],"ending":true,"suggested":false}]}`
	script, issues, err := ParseInteractiveScript(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("suggested flags must not fail validation: %+v", issues)
	}
	if !script.Rows[0].Next[1].Suggested || script.Rows[0].Next[0].Suggested {
		t.Fatalf("edge suggested flags must be preserved: %+v", script.Rows[0].Next)
	}
	if !script.Rows[2].Suggested {
		t.Fatalf("row suggested flag must be preserved: %+v", script.Rows[2])
	}
}

func hasIssue(issues []InteractiveIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
