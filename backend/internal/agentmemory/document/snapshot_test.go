package document

import (
	"strings"
	"testing"
	"unicode/utf8"

	"yingce/backend/internal/model"
)

func TestAgentMemorySnapshotRules(t *testing.T) {
	empty := SnapshotText(model.AgentMemoryDocumentModeAuto, 5000, nil, false)
	if !strings.Contains(empty, "用量 0/5000") || !strings.Contains(empty, "目前还没有记忆") || strings.Contains(empty, "提示：") {
		t.Fatalf("empty snapshot should invite recording without a nudge: %s", empty)
	}
	entries := []model.AgentMemoryEntry{
		{ID: "n2", Section: model.AgentMemorySectionNotes, Text: "竖屏优先"},
		{ID: "u1", Section: model.AgentMemorySectionUser, Text: "称呼用户为长官"},
	}
	text := SnapshotText(model.AgentMemoryDocumentModeReview, 5000, entries, true)
	for _, want := range []string{"## 关于用户", "[u1] 称呼用户为长官", "## 工作笔记", "[n2] 竖屏优先", "需确认模式", "memory_update 记下"} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot missing %q: %s", want, text)
		}
	}
	if strings.Index(text, "## 关于用户") > strings.Index(text, "## 工作笔记") {
		t.Fatalf("snapshot sections are out of order: %s", text)
	}
}

func TestAgentMemoryNudgeRules(t *testing.T) {
	for _, prompt := range []string{
		"以后都用竖屏",
		"下次记得先读分镜",
		"我喜欢低饱和色调",
		"From now on keep the dialogue restrained",
	} {
		if !ShouldNudge(prompt) {
			t.Fatalf("nudge rule missed %q", prompt)
		}
	}
	for _, prompt := range []string{
		"帮我生成一张猫的图片",
		"@[skill:remember-pack] 写个分镜",
		"please inspect this frame",
	} {
		if ShouldNudge(prompt) {
			t.Fatalf("nudge rule unexpectedly matched %q", prompt)
		}
	}
}

func TestAgentMemorySnapshotKeepsLegacyTruncation(t *testing.T) {
	text := SnapshotText(model.AgentMemoryDocumentModeAuto, 20000, []model.AgentMemoryEntry{
		{ID: "n1", Section: model.AgentMemorySectionNotes, Text: strings.Repeat("x", SnapshotMaxRunes)},
	}, false)
	if !strings.HasSuffix(text, "...") || strings.Contains(text, "…") {
		t.Fatalf("long snapshot truncation changed: suffix=%q", text[len(text)-8:])
	}
	if got := utf8.RuneCountInString(text); got != SnapshotMaxRunes+3 {
		t.Fatalf("long snapshot length = %d, want %d", got, SnapshotMaxRunes+3)
	}
}
