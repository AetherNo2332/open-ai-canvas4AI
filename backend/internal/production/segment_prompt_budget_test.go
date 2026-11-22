package production

import (
	"strings"
	"testing"
)

func TestSegmentPromptBodyBudgetKeepsExactBoundaryAndUnlimitedMode(t *testing.T) {
	header := "参考图1：甲"
	if budget, err := SegmentPromptBodyBudget(308, header); err != nil || budget != minSegmentPromptBodyRunes {
		t.Fatalf("exact minimum budget = %d, %v", budget, err)
	}
	if budget, err := SegmentPromptBodyBudget(0, header); err != nil || budget != 0 {
		t.Fatalf("undeclared limit must stay unlimited: %d, %v", budget, err)
	}
	if budget, err := SegmentPromptBodyBudget(-1, header); err != nil || budget != 0 {
		t.Fatalf("negative limit must stay unlimited: %d, %v", budget, err)
	}
}

func TestSegmentPromptBodyBudgetCountsHeaderRunes(t *testing.T) {
	budget, err := SegmentPromptBodyBudget(1000, "参考图1：甲")
	if err != nil || budget != 992 {
		t.Fatalf("multibyte header budget = %d, %v", budget, err)
	}
	_, err = SegmentPromptBodyBudget(307, "参考图1：甲")
	want := "开头的参考图说明占用 6 个字符，所选视频模型的提示词上限只有 307 个字符，放不下本段正文；请减少本段参考图，或换用提示词上限更大的视频模型"
	if err == nil || err.Error() != want {
		t.Fatalf("budget error = %v, want %q", err, want)
	}
	if !strings.Contains(err.Error(), "参考图说明") {
		t.Fatal("budget error must identify the reference header")
	}
}
