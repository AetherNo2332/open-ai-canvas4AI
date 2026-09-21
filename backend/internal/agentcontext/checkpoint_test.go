package agentcontext

import (
	"strings"
	"testing"
)

func TestCheckpointRoundTripPreservesPlanningFields(t *testing.T) {
	raw := `{"version":1,"historySummary":"用户要继续第三幕","scriptDesign":"主角在雨夜车站发现循环线索，下一场保持蓝色冷调和左手伤口连续性","operationHistory":["创建分镜节点 storyboard-1"],"pendingTasks":["task-1 仍在运行，先查询"],"currentWork":"第三幕转折镜头","nextStep":"补齐镜头 7 的对白","decisions":["采用非线性叙事"],"constraints":["不能更换主角外观"],"userPreferences":["对白克制"],"compactedTurnCount":8}`
	checkpoint, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	framed, err := Frame(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"雨夜车站", "task-1", "非线性叙事", "对白克制"} {
		if !strings.Contains(framed, fact) {
			t.Fatalf("checkpoint lost %q: %s", fact, framed)
		}
	}
}

func TestCheckpointRejectsUnknownFields(t *testing.T) {
	_, err := Parse(`{"version":1,"historySummary":"","scriptDesign":"","operationHistory":[],"pendingTasks":[],"currentWork":"","nextStep":"","decisions":[],"constraints":[],"userPreferences":[],"compactedTurnCount":1,"authorization":"auto"}`)
	if err == nil {
		t.Fatal("unknown checkpoint field accepted")
	}
}

func TestShouldCompactUsesHistoryAndSizeBudgets(t *testing.T) {
	if ShouldCompact(ThresholdHistoryMessages-1, ThresholdBytes-1) {
		t.Fatal("small history compacted")
	}
	if !ShouldCompact(ThresholdHistoryMessages, 1) || !ShouldCompact(1, ThresholdBytes) {
		t.Fatal("threshold did not trigger compaction")
	}
}
