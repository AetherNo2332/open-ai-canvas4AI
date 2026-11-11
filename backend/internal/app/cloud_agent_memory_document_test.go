package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// 快照渲染是 run 注入适配的一部分：用量、空记忆提示、分区展示与 nudge 都在这里。
// 文档写入、审批、撤销与重放行为已迁 internal/agentmemory/document。
func TestAgentMemorySnapshotShowsUsageEmptyHintAndNudge(t *testing.T) {
	empty := agentMemorySnapshotText(model.AgentMemoryDocumentModeAuto, 5000, nil, false)
	if !strings.Contains(empty, "用量 0/5000") || !strings.Contains(empty, "目前还没有记忆") || strings.Contains(empty, "提示：") {
		t.Fatalf("empty snapshot should invite recording without a nudge: %s", empty)
	}
	entries := []model.AgentMemoryEntry{{ID: "u1", Section: "user", Text: "称呼用户为长官"}, {ID: "n2", Section: "notes", Text: "竖屏优先"}}
	text := agentMemorySnapshotText(model.AgentMemoryDocumentModeReview, 5000, entries, agentMemoryNudge("以后都用竖屏"))
	for _, want := range []string{"## 关于用户", "[u1] 称呼用户为长官", "## 工作笔记", "[n2] 竖屏优先", "需确认模式", "memory_update 记下"} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot missing %q: %s", want, text)
		}
	}
	if agentMemoryNudge("帮我生成一张猫的图片") || agentMemoryNudge("@[skill:remember-pack] 写个分镜") {
		t.Fatal("ordinary requests or skill mention ids must not trigger the memory nudge")
	}
}
