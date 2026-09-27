package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
)

func piCompactionTestEntry(t *testing.T, id, parentID, kind string, body map[string]any) model.CloudAgentPiEntry {
	t.Helper()
	parent := any(nil)
	if parentID != "" {
		parent = parentID
	}
	entry := map[string]any{"type": kind, "id": id, "parentId": parent, "timestamp": "2026-09-28T00:00:00Z"}
	for key, value := range body {
		entry[key] = value
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return model.CloudAgentPiEntry{EntryID: id, ParentID: parentID, EntryJSON: string(raw)}
}

func piCompactionMessage(role, content string) map[string]any {
	return map[string]any{"role": role, "content": content}
}

func TestCloudAgentPiCompactionSourceUsesActiveBranchAndCompleteTurnTail(t *testing.T) {
	entries := []model.CloudAgentPiEntry{
		piCompactionTestEntry(t, "user-0", "", "message", map[string]any{"message": piCompactionMessage("user", "old turn")}),
		piCompactionTestEntry(t, "assistant-0", "user-0", "message", map[string]any{"message": piCompactionMessage("assistant", "old answer")}),
		piCompactionTestEntry(t, "user-1", "assistant-0", "message", map[string]any{"message": piCompactionMessage("user", "keep turn one")}),
		piCompactionTestEntry(t, "assistant-call", "user-1", "message", map[string]any{"message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "call-1", "name": "canvas_get_state", "arguments": map[string]any{}}},
		}}),
		piCompactionTestEntry(t, "tool-result", "assistant-call", "message", map[string]any{"message": map[string]any{
			"role": "toolResult", "toolCallId": "call-1", "toolName": "canvas_get_state", "content": []any{map[string]any{"type": "text", "text": "canvas state"}},
		}}),
		piCompactionTestEntry(t, "assistant-1", "tool-result", "message", map[string]any{"message": piCompactionMessage("assistant", "turn one answer")}),
		piCompactionTestEntry(t, "user-2", "assistant-1", "message", map[string]any{"message": piCompactionMessage("user", "keep turn two")}),
		piCompactionTestEntry(t, "assistant-2", "user-2", "message", map[string]any{"message": piCompactionMessage("assistant", "turn two answer")}),
		piCompactionTestEntry(t, "user-3", "assistant-2", "message", map[string]any{"message": piCompactionMessage("user", "current unanswered request")}),
		// This sibling branch must not appear in the compaction source.
		piCompactionTestEntry(t, "sibling-user", "assistant-0", "message", map[string]any{"message": piCompactionMessage("user", "abandoned branch")}),
	}
	branch, err := cloudAgentPiActiveBranch(entries, "user-3")
	if err != nil {
		t.Fatal(err)
	}
	source, err := cloudAgentPiCompactionSourceForBranch(branch, "user-3")
	if err != nil {
		t.Fatal(err)
	}
	if source.FirstKeptEntryID != "user-1" {
		t.Fatalf("retention boundary = %q, want start of the two latest complete turns", source.FirstKeptEntryID)
	}
	encoded, _ := json.Marshal(source.Messages)
	for _, want := range []string{"keep turn one", "turn one answer", "keep turn two", "current unanswered request", "canvas state"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("active branch context omitted %q: %s", want, encoded)
		}
	}
	if !strings.Contains(string(encoded), "old turn") || strings.Contains(string(encoded), "abandoned branch") {
		t.Fatalf("compaction source must summarize full active history and exclude sibling branches: %s", encoded)
	}
	if source.SourceDigest == "" || source.SourceBytes != len(encoded) || source.CompactedTurnCount != 4 {
		t.Fatalf("source metadata is inconsistent: %+v bytes=%d", source, len(encoded))
	}
}

func TestCloudAgentPiCompactionSourceReusesStructuredCheckpointAndRejectsBadBoundary(t *testing.T) {
	frame, err := agentcontext.Frame(agentcontext.Checkpoint{Version: agentcontext.Version, HistorySummary: "durable previous facts", CompactedTurnCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	entries := []model.CloudAgentPiEntry{
		piCompactionTestEntry(t, "old-user", "", "message", map[string]any{"message": piCompactionMessage("user", "already summarized request")}),
		piCompactionTestEntry(t, "old-assistant", "old-user", "message", map[string]any{"message": piCompactionMessage("assistant", "already summarized answer")}),
		piCompactionTestEntry(t, "kept-user", "old-assistant", "message", map[string]any{"message": piCompactionMessage("user", "recent request")}),
		piCompactionTestEntry(t, "kept-assistant", "kept-user", "message", map[string]any{"message": piCompactionMessage("assistant", "recent answer")}),
		piCompactionTestEntry(t, "compact-1", "kept-assistant", "compaction", map[string]any{
			"summary": frame, "firstKeptEntryId": "kept-user", "tokensBefore": 20000,
		}),
		piCompactionTestEntry(t, "next-user", "compact-1", "message", map[string]any{"message": piCompactionMessage("user", "new unanswered request")}),
	}
	branch, err := cloudAgentPiActiveBranch(entries, "next-user")
	if err != nil {
		t.Fatal(err)
	}
	source, err := cloudAgentPiCompactionSourceForBranch(branch, "next-user")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(source.Messages)
	for _, want := range []string{"durable previous facts", agentcontext.Acknowledgement, "recent request", "recent answer", "new unanswered request"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("compaction source omitted %q: %s", want, encoded)
		}
	}
	if strings.Contains(string(encoded), "already summarized") {
		t.Fatalf("previously compacted history reappeared: %s", encoded)
	}
	if source.FirstKeptEntryID != "kept-user" || source.CompactedTurnCount != 4 {
		t.Fatalf("existing structured compaction boundary/count not preserved: %+v", source)
	}

	badBranch := append([]cloudAgentPiCompactionEntry(nil), branch...)
	for index := range badBranch {
		if badBranch[index].ID == "compact-1" {
			badBranch[index].FirstKeptEntryID = "not-on-active-branch"
		}
	}
	if _, err := cloudAgentPiCompactionSourceForBranch(badBranch, "next-user"); err == nil {
		t.Fatal("a persisted compaction boundary outside the active branch was accepted")
	}
}

func TestCloudAgentPiActiveBranchRejectsMissingParentsAndCycles(t *testing.T) {
	for name, entries := range map[string][]model.CloudAgentPiEntry{
		"missing parent": {piCompactionTestEntry(t, "leaf", "missing", "message", map[string]any{"message": piCompactionMessage("user", "x")})},
		"cycle": {
			piCompactionTestEntry(t, "a", "b", "message", map[string]any{"message": piCompactionMessage("user", "x")}),
			piCompactionTestEntry(t, "b", "a", "message", map[string]any{"message": piCompactionMessage("assistant", "y")}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			leaf := "leaf"
			if name == "cycle" {
				leaf = "a"
			}
			if _, err := cloudAgentPiActiveBranch(entries, leaf); err == nil {
				t.Fatal("invalid Pi branch was accepted")
			}
		})
	}
}
