package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"yingce/backend/internal/agentcontext"
	"yingce/backend/internal/model"
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
	source, err := cloudAgentPiCompactionSourceForBranch(branch, "user-3", 20_000)
	if err != nil {
		t.Fatal(err)
	}
	if source.FirstKeptEntryID != "user-1" || source.FirstKeptIndex != 2 {
		t.Fatalf("retention boundary = %q at index %d, want start of the two latest complete turns", source.FirstKeptEntryID, source.FirstKeptIndex)
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
	source, err := cloudAgentPiCompactionSourceForBranch(branch, "next-user", 20_000)
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
	if source.FirstKeptEntryID != "kept-user" || source.FirstKeptIndex != 2 || source.CompactedTurnCount != 4 {
		t.Fatalf("existing structured compaction boundary/count not preserved: %+v", source)
	}

	badBranch := append([]cloudAgentPiCompactionEntry(nil), branch...)
	for index := range badBranch {
		if badBranch[index].ID == "compact-1" {
			badBranch[index].FirstKeptEntryID = "not-on-active-branch"
		}
	}
	if _, err := cloudAgentPiCompactionSourceForBranch(badBranch, "next-user", 20_000); err == nil {
		t.Fatal("a persisted compaction boundary outside the active branch was accepted")
	}
}

// piCompactionToolRound is one Pi tool round: an assistant call and its result.
func piCompactionToolRound(t *testing.T, round int, parentID, result string) []model.CloudAgentPiEntry {
	t.Helper()
	call := fmt.Sprintf("call-%d", round)
	return []model.CloudAgentPiEntry{
		piCompactionTestEntry(t, fmt.Sprintf("assistant-%d", round), parentID, "message", map[string]any{"message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": call, "name": "canvas_get_state", "arguments": map[string]any{}}},
		}}),
		piCompactionTestEntry(t, fmt.Sprintf("result-%d", round), fmt.Sprintf("assistant-%d", round), "message", map[string]any{"message": map[string]any{
			"role": "toolResult", "toolCallId": call, "toolName": "canvas_get_state", "content": []any{map[string]any{"type": "text", "text": result}},
		}}),
	}
}

// One request followed by many tool rounds has a single complete turn. The server
// compactor must still keep only a token-bounded suffix, because Pi sends exactly the
// retained suffix after the checkpoint (production: 163 messages kept after a summary).
func TestCloudAgentPiCompactionSourceBoundsAgentRoundsByKeepTokens(t *testing.T) {
	entries := []model.CloudAgentPiEntry{
		piCompactionTestEntry(t, "request", "", "message", map[string]any{"message": piCompactionMessage("user", "补完十二个镜头")}),
	}
	parentID := "request"
	for round := 0; round < 12; round++ {
		entries = append(entries, piCompactionToolRound(t, round, parentID, strings.Repeat("canvas state ", 300))...)
		parentID = fmt.Sprintf("result-%d", round)
	}
	entries = append(entries, piCompactionTestEntry(t, "final", parentID, "message", map[string]any{"message": piCompactionMessage("assistant", "已补完全部镜头")}))
	branch, err := cloudAgentPiActiveBranch(entries, "final")
	if err != nil {
		t.Fatal(err)
	}
	unbounded, err := cloudAgentPiCompactionSourceForBranch(branch, "final", 0)
	if err != nil {
		t.Fatal(err)
	}
	if unbounded.FirstKeptEntryID != "request" {
		t.Fatalf("complete-turn boundary = %q, want the only request", unbounded.FirstKeptEntryID)
	}
	const keepTokens = 3_000
	source, err := cloudAgentPiCompactionSourceForBranch(branch, "final", keepTokens)
	if err != nil {
		t.Fatal(err)
	}
	if source.FirstKeptIndex <= 0 || source.MessageEntryIDs[source.FirstKeptIndex] != source.FirstKeptEntryID {
		t.Fatalf("token budget did not move the boundary: %+v", source)
	}
	if role := stringField(source.Messages[source.FirstKeptIndex], "role"); role != "assistant" && role != "user" {
		t.Fatalf("retention boundary starts at %q", role)
	}
	tokens := func(messages []map[string]any) int {
		total := 0
		for _, message := range messages {
			raw, _ := json.Marshal(message)
			total += estimateCloudAgentTokens(raw)
		}
		return total
	}
	// The boundary is the nearest one at or after the message that fills the budget.
	if after, from := tokens(source.Messages[source.FirstKeptIndex+1:]), tokens(source.Messages[source.FirstKeptIndex-1:]); after >= keepTokens || from < keepTokens {
		t.Fatalf("suffix after boundary %d tokens, from previous message %d tokens, budget %d", after, from, keepTokens)
	}
	open := map[string]bool{}
	for _, message := range source.Messages[source.FirstKeptIndex:] {
		for _, call := range creationMaps(message["tool_calls"]) {
			open[stringField(call, "id")] = true
		}
		if stringField(message, "role") == "tool" {
			if !open[stringField(message, "tool_call_id")] {
				t.Fatalf("retained result lost its call: %+v", message)
			}
			delete(open, stringField(message, "tool_call_id"))
		}
	}
	if len(open) != 0 {
		t.Fatalf("retained calls lost their results: %v", open)
	}
}

func TestCloudAgentPiRetentionIndexKeepsTheCallOfAnOversizedTrailingResult(t *testing.T) {
	entries := []model.CloudAgentPiEntry{
		piCompactionTestEntry(t, "request", "", "message", map[string]any{"message": piCompactionMessage("user", "读取画布")}),
	}
	entries = append(entries, piCompactionToolRound(t, 0, "request", strings.Repeat("large state ", 4_000))...)
	branch, err := cloudAgentPiActiveBranch(entries, "result-0")
	if err != nil {
		t.Fatal(err)
	}
	source, err := cloudAgentPiCompactionSourceForBranch(branch, "result-0", 1_000)
	if err != nil {
		t.Fatal(err)
	}
	if source.FirstKeptEntryID != "assistant-0" || source.FirstKeptIndex != 1 {
		t.Fatalf("boundary = %q at %d, want the assistant call before its oversized result", source.FirstKeptEntryID, source.FirstKeptIndex)
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
