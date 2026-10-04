package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCrewEventsCursorIsolationAndPrivatePayload(t *testing.T) {
	s, crew, _ := crewRunFixture(t)
	run, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "secret-root-prompt", IdempotencyKey: "events"})
	if err != nil {
		t.Fatal(err)
	}
	member := run.Members[1]
	if err := s.DispatchCrewTask(run.ID, member.MemberID, CrewTaskInput{TaskID: "story", Title: "故事", Instructions: "secret-instructions"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteMemberRun(member.ID, MemberTaskResult{TaskID: "story", Summary: "故事摘要"}); err != nil {
		t.Fatal(err)
	}
	events, err := s.CrewRunEvents("user", run.ID, 0)
	if err != nil || len(events) != 3 {
		t.Fatalf("events: %d %v", len(events), err)
	}
	if events[0].Type != "crew_run_created" || events[1].Type != "member_message" || events[2].Type != "member_run_completed" {
		t.Fatalf("types: %+v", events)
	}
	for i, event := range events {
		if event.Sequence != int64(i+1) || event.CrewRunID != run.ID {
			t.Fatal("noncontiguous event sequence")
		}
	}
	after, err := s.CrewRunEvents("user", run.ID, 2)
	if err != nil || len(after) != 1 || after[0].Sequence != 3 {
		t.Fatal("resume cursor incorrect")
	}
	if _, err := s.CrewRunEvents("other", run.ID, 0); err == nil {
		t.Fatal("cross-account events exposed")
	}
	raw, _ := json.Marshal(events)
	for _, secret := range []string{"secret-root-prompt", "secret-instructions", "共同规则", "canonical", "piSessionEntries"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private event payload")
		}
	}
}
