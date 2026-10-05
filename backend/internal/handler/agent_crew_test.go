package handler

import (
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/service"
	"testing"
)

func TestAgentCrewHTTPStrictRevisionAndOwnership(t *testing.T) {
	call := workspaceHTTP(t)
	input := `{"name":"剧组","description":"","status":"enabled","members":[{"name":"导演","role":"coordinator","modelConfig":{"model":"text-model"},"permissionMode":"propose","focusNodeIds":[],"budget":{"maxCredits":10,"maxSteps":20},"enabled":true,"position":0}]}`
	if w := call("POST", "foreign/crews", input, true); w.Code != 404 {
		t.Fatalf("foreign create: %d %s", w.Code, w.Body)
	}
	w := call("POST", "own/crews", input, true)
	var response struct {
		Data service.CrewView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || len(response.Data.Members) != 1 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	crew := response.Data
	path := "/api/agent/crews/" + crew.ID
	for _, body := range []string{`{}`, `{"revision":0,"name":"新剧组","status":"enabled","unknown":true}`, `{"revision":0,"name":"新剧组","status":"enabled"} {}`} {
		if w := call("PATCH", path, body, true); w.Code != 400 {
			t.Fatalf("strict update: %d %s", w.Code, w.Body)
		}
	}
	w = call("PATCH", path, `{"revision":0,"name":"新剧组","status":"enabled"}`, true)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	if w := call("DELETE", path, `{"revision":0}`, true); w.Code != 409 {
		t.Fatalf("stale delete: %d %s", w.Code, w.Body)
	}
	memberPath := "/api/agent/crew-members/" + crew.Members[0].ID
	if w := call("DELETE", memberPath, `{"revision":1}`, true); w.Code != 400 {
		t.Fatalf("coordinator delete: %d %s", w.Code, w.Body)
	}
	if w := call("PUT", memberPath+"/skills", `{"revision":1,"skills":[]}`, true); w.Code != 200 {
		t.Fatalf("skill CAS: %d %s", w.Code, w.Body)
	}
	if w := call("PATCH", memberPath, `{"revision":2,"name":"无效","role":"coordinator","modelConfig":{"model":"text-model","apiKey":"forbidden"},"permissionMode":"propose","budget":{"maxCredits":10},"enabled":true,"position":0}`, true); w.Code != 400 {
		t.Fatalf("unknown nested credential: %d %s", w.Code, w.Body)
	}
	if w := call("DELETE", path, `{"revision":2}`, true); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := call("PUT", memberPath+"/skills", fmt.Sprintf(`{"revision":%d,"skills":[]}`, 3), true); w.Code != 404 {
		t.Fatalf("deleted member: %d %s", w.Code, w.Body)
	}
}
