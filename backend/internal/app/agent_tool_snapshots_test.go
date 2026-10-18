package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
	"testing"
	"time"
)

func TestToolSnapshotComparisonStrictSubset(t *testing.T) {
	a := ToolSnapshotArtifact{SchemaVersion: "v", Tools: []map[string]any{{"type": "function", "function": map[string]any{"name": "x", "parameters": map[string]any{"type": "object", "description": "old"}}}}}
	b := ToolSnapshotArtifact{SchemaVersion: "v", Tools: []map[string]any{{"function": map[string]any{"parameters": map[string]any{"description": "old", "type": "object"}, "name": "x"}, "type": "function"}}}
	if d := compareToolArtifacts(a, b, true); len(d) != 0 {
		t.Fatalf("key ordering: %+v", d)
	}
	b.Tools[0]["function"].(map[string]any)["parameters"].(map[string]any)["description"] = "new"
	if d := compareToolArtifacts(a, b, true); len(d) != 1 || d[0].Path != "/function/parameters/description" {
		t.Fatalf("description must reject: %+v", d)
	}
}

func TestToolSnapshotComparisonMatchesWorkerParameterContract(t *testing.T) {
	makeArtifact := func(description string) ToolSnapshotArtifact {
		return ToolSnapshotArtifact{SchemaVersion: "v", Tools: []map[string]any{{"type": "function", "function": map[string]any{"name": "x", "description": description, "parameters": map[string]any{"type": "object"}}}}}
	}
	if diff := compareToolArtifacts(makeArtifact("run-specific guidance"), makeArtifact("catalog guidance"), true); len(diff) != 0 {
		t.Fatalf("worker checks parameters, not request-specific outer descriptions: %+v", diff)
	}
}

func TestToolSnapshotFrozenVersionDoesNotInventHistoricalMetadata(t *testing.T) {
	state := cloudAgentRuntime{Canonical: canonicalAgentRequest{Tools: backendToolArtifact().Tools[:1]}}
	for _, version := range []string{"", "cloud-agent-tools/v3"} {
		state.ToolSchemaVersion = version
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := frozenToolArtifact(model.CloudAgentExecution{StateJSON: string(encoded)})
		want := version
		if want == "" {
			want = "unknown"
		}
		if err != nil || snapshot.SchemaVersion != want {
			t.Fatalf("frozen version: got %q want %q: %v", snapshot.SchemaVersion, want, err)
		}
	}
}

func TestToolSnapshotClaimSkipsDriftAndHonorsDrain(t *testing.T) {
	s, db, original := piAgentTestFixture(t)
	if err := db.AutoMigrate(&model.AgentToolSnapshotContent{}, &model.AgentToolSnapshotCounter{}, &model.AgentToolSnapshot{}, &model.AgentToolRuntime{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.AgentSchedulerSetting{}, &model.AgentToolSnapshotAlert{}, &model.AdminAuditEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(original).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	catalog := backendToolArtifact()
	single := ToolSnapshotArtifact{SchemaVersion: catalog.SchemaVersion, Tools: catalog.Tools[:1]}
	goodState := cloudAgentRuntime{Canonical: canonicalAgentRequest{Tools: single.Tools}}
	goodJSON, _ := json.Marshal(goodState)
	var badState cloudAgentRuntime
	_ = json.Unmarshal(goodJSON, &badState)
	badState.Canonical.Tools[0]["function"].(map[string]any)["parameters"].(map[string]any)["description"] = "old parameters"
	badJSON, _ := json.Marshal(badState)
	for i, id := range []string{"bad-first", "good-next"} {
		encoded := badJSON
		if i == 1 {
			encoded = goodJSON
		}
		run := model.CloudAgentExecution{ID: id, UserID: "user", CanvasID: "snapshot-canvas", ConversationID: id, Engine: "pi", Status: "queued", Revision: 1, StateJSON: string(encoded), CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.CloudAgentPiSession{ID: id, UserID: "user", ConversationID: id, ActiveRunID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	registered, err := s.RegisterToolSnapshot(ToolSnapshotRegistration{InstanceID: "process", BootID: "boot", LoadGeneration: 1, Artifact: catalog})
	if err != nil {
		t.Fatal(err)
	}
	identity := ToolSnapshotIdentity{InstanceID: "process", BootID: "boot", SnapshotID: registered.SnapshotID, LoadGeneration: 1}
	guard, err := s.toolClaimGuard(identity)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.repo.ClaimPiAgentFair("distinct-slot-owner", time.Now().Add(time.Minute), model.DefaultAgentSchedulerSetting(), guard)
	if err != nil || claimed == nil || claimed.ID != "good-next" {
		t.Fatalf("incompatible head blocked valid subset: %+v %v", claimed, err)
	}
	var bad model.CloudAgentExecution
	if err = db.First(&bad, "id = ?", "bad-first").Error; err != nil {
		t.Fatal(err)
	}
	if bad.Status != "queued" || bad.WaitKind != "tool_snapshot" || bad.LeaseOwner != "" || bad.StateJSON != string(badJSON) {
		t.Fatalf("incompatible run mutated or leased: %+v", bad)
	}
	runtime, err := s.repo.ToolRuntime("process")
	if err != nil {
		t.Fatal(err)
	}
	audit, _ := newAdminAuditEvent(&model.User{ID: "admin"}, "test.drain", "worker", "process", "upgrade", nil)
	if err = s.repo.DrainToolRuntime("process", runtime.Revision, true, audit); err != nil {
		t.Fatal(err)
	}
	allowed, err := guard(s.repo, model.CloudAgentExecution{StateJSON: string(goodJSON)})
	if allowed || err != repository.ErrToolRuntimeUnavailable {
		t.Fatalf("draining worker accepted: %v %v", allowed, err)
	}
	identity.LoadGeneration = 2
	wrong, err := s.toolClaimGuard(identity)
	if err == nil {
		_, err = wrong(s.repo, model.CloudAgentExecution{StateJSON: string(goodJSON)})
	}
	if err == nil {
		t.Fatal("wrong loaded generation accepted")
	}
}

func TestToolSnapshotInvalidLoadsAndIdentity(t *testing.T) {
	s, db, _ := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentToolSnapshotContent{}, &model.AgentToolSnapshotCounter{}, &model.AgentToolSnapshot{}, &model.AgentToolRuntime{}, &model.AgentAdmissionCounter{}, &model.AgentToolSnapshotAlert{}, &model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	in := ToolSnapshotRegistration{InstanceID: "broken", BootID: "boot", LoadGeneration: 1, Artifact: ToolSnapshotArtifact{SchemaVersion: "unavailable", Tools: []map[string]any{}}, LoadError: "artifact unavailable"}
	result, err := s.RegisterToolSnapshot(in)
	if err != nil || result.SnapshotID == "" {
		t.Fatalf("invalid loaded state must register for visibility: %v", err)
	}
	h, err := s.AdminToolSnapshotHealth(&model.User{ID: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive})
	if err != nil || len(h.Workers) != 1 || h.Workers[0].Status != "invalid" {
		t.Fatalf("invalid falsely healthy: %+v %v", h, err)
	}
	if _, err = s.toolClaimGuard(ToolSnapshotIdentity{InstanceID: "broken", BootID: "other", SnapshotID: result.SnapshotID}); err == nil {
		t.Fatal("foreign boot accepted")
	}
	t.Setenv("CANVAS_AGENT_REQUIRE_TOOL_SNAPSHOT", "true")
	if _, err = s.toolClaimGuard(ToolSnapshotIdentity{}); err == nil {
		t.Fatal("strict legacy accepted")
	}
}

func TestToolSnapshotBoundsAndImageException(t *testing.T) {
	a := backendToolArtifact()
	raw, _ := json.Marshal(a)
	var b ToolSnapshotArtifact
	_ = json.Unmarshal(raw, &b)
	b.Tools = append(b.Tools, b.Tools[0])
	if _, err := normalizeToolArtifact(b); err == nil {
		t.Fatal("duplicate accepted")
	}
	_ = json.Unmarshal(raw, &b)
	b.SchemaVersion = strings.Repeat("x", 121)
	if _, err := normalizeToolArtifact(b); err == nil {
		t.Fatal("oversized version accepted")
	}
	for _, tool := range a.Tools {
		if toolName(tool) != "canvas_inspect_image" {
			continue
		}
		f := tool["function"].(map[string]any)
		parameters := f["parameters"].(map[string]any)
		p := parameters["properties"].(map[string]any)
		if _, ok := p["summary"]; !ok {
			t.Fatal("exception fixture missing summary")
		}
		server := map[string]any{}
		for k, v := range parameters {
			server[k] = v
		}
		props := map[string]any{}
		for k, v := range p {
			if k != "summary" {
				props[k] = v
			}
		}
		server["properties"] = props
		// Normalize []string required arrays to their actual JSON wire representation.
		encoded, _ := json.Marshal(parameters)
		var wire map[string]any
		_ = json.Unmarshal(encoded, &wire)
		encoded, _ = json.Marshal(server)
		_ = json.Unmarshal(encoded, &server)
		if !compatibleImageTool("canvas_inspect_image", server, wire) {
			t.Fatal("optional summary compatibility lost")
		}
		server["description"] = "drift"
		if compatibleImageTool("canvas_inspect_image", server, wire) {
			t.Fatal("other parameter drift accepted")
		}
		return
	}
	t.Fatal("image tool missing")
}

func TestToolSnapshotRegistrationDrainAndPermissions(t *testing.T) {
	s, db, _ := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentToolSnapshotContent{}, &model.AgentToolSnapshotCounter{}, &model.AgentToolSnapshot{}, &model.AgentToolRuntime{}, &model.AgentToolSnapshotAlert{}, &model.AdminAuditEvent{}, &model.AgentAdmissionCounter{}, &model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	in := ToolSnapshotRegistration{InstanceID: "proc", BootID: "boot", LoadGeneration: 1, Artifact: backendToolArtifact()}
	first, err := s.RegisterToolSnapshot(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RegisterToolSnapshot(in)
	if err != nil || first.SnapshotID != second.SnapshotID {
		t.Fatalf("immutable dedup: %v", err)
	}
	if _, err = s.AdminToolSnapshotHealth(&model.User{ID: "user"}); err == nil {
		t.Fatal("non-admin accepted")
	}
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	if _, err = s.AdminDrainToolRuntime(admin, "proc", ToolRuntimeDrain{ExpectedRevision: 1, Draining: true, Reason: "upgrade"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdminDrainToolRuntime(admin, "proc", ToolRuntimeDrain{ExpectedRevision: 1, Draining: false, Reason: "stale"}); err == nil {
		t.Fatal("stale revision accepted")
	}
}

func TestToolSnapshotComparisonSelectionAndSharedContent(t *testing.T) {
	s, db, _ := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentToolSnapshotContent{}, &model.AgentToolSnapshotCounter{}, &model.AgentToolSnapshot{}, &model.AgentToolRuntime{}); err != nil {
		t.Fatal(err)
	}
	makeRow := func(id, source, description string) model.AgentToolSnapshot {
		a := ToolSnapshotArtifact{SchemaVersion: "v", Tools: []map[string]any{{"type": "function", "function": map[string]any{"name": "x", "parameters": map[string]any{"type": "object", "description": description}}}}}
		body, err := normalizeToolArtifact(a)
		if err != nil {
			t.Fatal(err)
		}
		row := model.AgentToolSnapshot{ID: id, Source: source, InstanceID: id, SchemaVersion: "v", ToolCount: 1, Contents: body}
		if err := s.repo.SaveToolSnapshot(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	left := makeRow("backend-choice", "backend", "backend-value")
	right := makeRow("task-choice", "task", "task-value")
	duplicate := makeRow("another-worker", "worker", "backend-value")
	if left.ContentID != duplicate.ContentID {
		t.Fatal("identical definitions must share immutable content")
	}
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	comparison, err := s.AdminCompareToolSnapshots(admin, left.ID, right.ID)
	if err != nil || comparison == nil || len(comparison.Differences) != 1 {
		t.Fatalf("comparison: %+v %v", comparison, err)
	}
	diff := comparison.Differences[0]
	if diff.Before != "backend-value" || diff.After != "task-value" {
		t.Fatalf("selected left/right reversed: %+v", diff)
	}
	rows, total, err := s.repo.ListToolSnapshots(repository.ToolSnapshotListQuery{Page: 2, PageSize: 1, Tool: "x"})
	if err != nil || total != 3 || len(rows) != 1 {
		t.Fatalf("pagination: %+v %d %v", rows, total, err)
	}
	_, total, err = s.repo.ListToolSnapshots(repository.ToolSnapshotListQuery{Page: 1, PageSize: 20, Tool: "xx"})
	if err != nil || total != 0 {
		t.Fatalf("tool filter must be exact: %d %v", total, err)
	}
}

func TestToolSnapshotHeartbeatRequiresExactGeneration(t *testing.T) {
	s, db, _ := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentToolRuntime{}); err != nil {
		t.Fatal(err)
	}
	row := model.AgentToolRuntime{InstanceID: "worker", BootID: "boot", SnapshotID: "snapshot", LoadGeneration: 1}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.HeartbeatToolRuntime("worker", "boot", "snapshot", 0, "loaded"); err == nil {
		t.Fatal("generation zero bypassed identity")
	}
	if err := s.repo.HeartbeatToolRuntime("worker", "boot", "snapshot", 1, "loaded"); err != nil {
		t.Fatal(err)
	}
}

func TestToolSnapshotHistoryScopesCurrentBackend(t *testing.T) {
	s, db, _ := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentToolSnapshotContent{}, &model.AgentToolSnapshotCounter{}, &model.AgentToolSnapshot{}, &model.AgentToolRuntime{}, &model.AgentAdmissionCounter{}, &model.AgentToolSnapshotAlert{}, &model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	registered, err := s.RegisterToolSnapshot(ToolSnapshotRegistration{InstanceID: "broken", BootID: "boot", LoadGeneration: 1, Artifact: ToolSnapshotArtifact{SchemaVersion: "unavailable", Tools: []map[string]any{}}, LoadError: "artifact unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour)
	event := model.AgentToolSnapshotAlert{ID: "old-event", BackendSnapshotID: "old-backend", WorkerSnapshotID: registered.SnapshotID, InstanceID: "broken", Code: "tool_snapshot_invalid", FirstSeenAt: old, LastSeenAt: old}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	health, err := s.AdminToolSnapshotHealth(&model.User{ID: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range health.History {
		if item.BackendSnapshotID == "old-backend" {
			found = true
			if !item.HistoricalOnly {
				t.Fatal("old backend record presented as current status")
			}
		}
	}
	if !found {
		t.Fatal("historical record lost")
	}
	for _, alert := range health.Alerts {
		if alert.Code == "tool_snapshot_invalid" && (alert.FirstSeenAt == nil || alert.FirstSeenAt.Before(time.Now().Add(-time.Hour))) {
			t.Fatalf("old backend overwrote current alert timestamp: %+v", alert)
		}
	}
}
