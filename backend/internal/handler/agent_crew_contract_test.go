package handler

import (
    "encoding/json"
    "testing"

    "infinite-canvas/backend/internal/model"
)

func TestCrewBusinessEventContractIsStableAndRedacted(t *testing.T) {
    if model.CrewEventMemberRunStarted != "member_run_started" || model.CrewEventCrewCompleted != "crew_run_completed" {
        t.Fatalf("unexpected crew event names: %q %q", model.CrewEventMemberRunStarted, model.CrewEventCrewCompleted)
    }
    event := model.CrewBusinessEvent{Type: model.CrewEventMemberMessage, CrewRunID: "crew-1", MemberRunID: "member-1", Payload: model.CrewMemberMessagePayload{TaskID: "task-1", Summary: "done", ArtifactIDs: []string{"asset-1"}}}
    raw, err := json.Marshal(event)
    if err != nil { t.Fatal(err) }
    body := string(raw)
    for _, forbidden := range []string{"prompt", "apiKey", "cookie", "piEntry", "transcript"} {
        if containsJSONKey(body, forbidden) { t.Fatalf("public crew event leaked key %q: %s", forbidden, body) }
    }
}

func containsJSONKey(raw, key string) bool { return json.Valid([]byte(raw)) && string([]byte(raw)) != "" && contains(raw, `"`+key+`"`) }
func contains(raw, needle string) bool { for i := 0; i+len(needle) <= len(raw); i++ { if raw[i:i+len(needle)] == needle { return true } }; return false }
