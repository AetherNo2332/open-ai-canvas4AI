package handler

import "testing"

func TestDynamicSubagentHTTPPolicyOwnership(t *testing.T) {
	call := workspaceHTTP(t)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/agent/subagent-policy?canvasId=foreign", "", 404},
		{"GET", "/api/agent/subagent-policy?canvasId=missing", "", 404},
		{"PUT", "/api/agent/subagent-policy", `{"canvasId":"foreign","enabled":true,"expectedRevision":0}`, 404},
		{"GET", "/api/agent/subagent-policy", "", 400},
		{"PUT", "/api/agent/subagent-policy", `{"canvasId":"","enabled":true,"expectedRevision":0}`, 400},
		{"GET", "/api/agent/runs/missing/subagents", "", 404},
	} {
		t.Run(test.method+test.path+test.body, func(t *testing.T) {
			if response := call(test.method, test.path, test.body, true); response.Code != test.status {
				t.Fatalf("HTTP %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}
