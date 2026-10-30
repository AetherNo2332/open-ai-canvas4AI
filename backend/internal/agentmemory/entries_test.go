package agentmemory

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestAppendLessonsMapsSectionsSkipsUnsafeAndDuplicates(t *testing.T) {
	steps, _ := json.Marshal([]model.AgentLessonStep{{Tool: "a", Action: "先读"}, {Tool: "b", Action: "再写"}})
	lessons := []model.AgentLesson{
		{Situation: "回答时", Lesson: "称呼用户为长官", Source: "user_preference"},
		{Situation: "回答时", Lesson: "称呼用户为“长官”。"},
		{Situation: "分镜", StepsJSON: string(steps)},
		{Situation: "外部接口", Lesson: "api_key: sk-abcdefghijklmnopqrstuvwx"},
	}
	existing := []model.AgentMemoryEntry{{ID: "n7", Section: model.AgentMemorySectionNotes, Text: "已有"}}
	added, next := AppendLessons(existing, NextSeq(3, existing), lessons, DefaultCapacity, time.Now())
	if len(added) != 2 || added[0].ID != "u8" || added[0].Section != model.AgentMemorySectionUser || added[1].ID != "n9" || added[1].Text != "分镜：先读 → 再写" || next != 10 {
		t.Fatalf("unexpected import: %+v next=%d", added, next)
	}
	for _, entry := range added {
		if entry.Source != LessonImportSource {
			t.Fatalf("source = %q", entry.Source)
		}
	}
	long, _ := AppendLessons(nil, 1, []model.AgentLesson{{Situation: "长", Lesson: strings.Repeat("字", 900)}}, DefaultCapacity, time.Now())
	if len(long) != 1 || len([]rune(long[0].Text)) > EntryMaxRunes {
		t.Fatalf("long lesson not truncated: %+v", long)
	}
}
