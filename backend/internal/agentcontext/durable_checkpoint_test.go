package agentcontext

import (
	"strings"
	"testing"
)

func TestAgentContextJournalWindowRules(t *testing.T) {
	valid := []JournalEvent{
		{RowSequence: 3, Sequence: 3, RunID: "run", EventID: "run:3"},
		{RowSequence: 4, Sequence: 4, RunID: "run", EventID: "run:4"},
		{RowSequence: 5, Sequence: 5, RunID: "run", EventID: "run:5"},
	}
	if base, err := ValidateJournalWindow("run", 5, valid); err != nil || base != 2 {
		t.Fatalf("valid window base=%d err=%v", base, err)
	}
	for name, event := range map[string]JournalEvent{
		"row sequence":  {RowSequence: 2, Sequence: 3, RunID: "run", EventID: "run:3"},
		"body sequence": {RowSequence: 3, Sequence: 2, RunID: "run", EventID: "run:3"},
		"run id":        {RowSequence: 3, Sequence: 3, RunID: "other", EventID: "run:3"},
		"event id":      {RowSequence: 3, Sequence: 3, RunID: "run", EventID: "other:3"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateJournalWindow("run", 3, []JournalEvent{event})
			if err == nil || err.Error() != "Agent event identity is invalid" {
				t.Fatalf("identity error = %v", err)
			}
		})
	}
	if _, err := ValidateJournalWindow("run", 2, nil); err == nil || err.Error() != "Agent execution journal is incomplete" {
		t.Fatalf("empty window error = %v", err)
	}
	if _, err := ValidateJournalWindow("run", 3, valid[:2]); err == nil || !strings.Contains(err.Error(), "lastSeq=4 count=3") {
		t.Fatalf("tail mismatch error = %v", err)
	}
	duplicateTail := []JournalEvent{
		{RowSequence: 1, Sequence: 1, RunID: "run", EventID: "run:1"},
		{RowSequence: 1, Sequence: 1, RunID: "run", EventID: "run:1"},
	}
	if _, err := ValidateJournalWindow("run", 1, duplicateTail); err == nil || !strings.Contains(err.Error(), "watermark is invalid") {
		t.Fatalf("watermark error = %v", err)
	}
}

func TestAgentContextJournalSequenceRules(t *testing.T) {
	valid := []JournalEvent{
		{Sequence: 3, RunID: "run", EventID: "run:3"},
		{Sequence: 4, RunID: "run", EventID: "run:4"},
	}
	if err := ValidateJournalSequence("run", 2, valid); err != nil {
		t.Fatal(err)
	}
	valid[1].Sequence, valid[1].EventID = 5, "run:5"
	if err := ValidateJournalSequence("run", 2, valid); err == nil || err.Error() != "Agent event sequence or identity is invalid" {
		t.Fatalf("sequence error = %v", err)
	}
}

func TestAgentContextMessageCountRule(t *testing.T) {
	if err := ValidateMessageCount(2, 2); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMessageCount(1, 2); err == nil || err.Error() != "Agent execution transcript is incomplete: rows=1 count=2" {
		t.Fatalf("message count error = %v", err)
	}
}
