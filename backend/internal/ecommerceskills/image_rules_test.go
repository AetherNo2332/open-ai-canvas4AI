package ecommerceskills

import (
	"strings"
	"testing"
)

func TestImageExecutionRulesKeepSelectionOrderAndSkipEmptyRules(t *testing.T) {
	calls := []ManagedInvocation{
		{Name: "鲜果执行", ImageRule: "只使用真实鲜果。"},
		{Name: "无规则技能"},
		{Name: "排版执行", ImageRule: "保留商品包装原文。"},
	}
	rules := ImageExecutionRules(calls)
	if !strings.Contains(rules, "本轮已选Skills图片执行约束") ||
		!strings.Contains(rules, "鲜果执行：只使用真实鲜果。\n排版执行：保留商品包装原文。") ||
		strings.Contains(rules, "无规则技能") {
		t.Fatalf("unexpected image rules: %q", rules)
	}
	if ImageExecutionRules(nil) != "" || ImageExecutionRules([]ManagedInvocation{{Name: "empty"}}) != "" {
		t.Fatal("empty image rules should not create a prompt block")
	}
}
