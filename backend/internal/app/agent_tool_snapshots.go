package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/buildinfo"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type ToolSnapshotArtifact struct {
	SchemaVersion string           `json:"schemaVersion"`
	Tools         []map[string]any `json:"tools"`
}
type ToolSnapshotRegistration struct {
	InstanceID     string               `json:"instanceId"`
	BootID         string               `json:"bootId"`
	BuildCommit    string               `json:"buildCommit"`
	LoadGeneration int64                `json:"loadGeneration"`
	Artifact       ToolSnapshotArtifact `json:"artifact"`
	LoadError      string               `json:"loadError"`
}
type ToolSnapshotIdentity struct {
	InstanceID     string `json:"instanceId"`
	BootID         string `json:"bootId"`
	SnapshotID     string `json:"snapshotId"`
	LoadGeneration int64  `json:"loadGeneration"`
}
type ToolSnapshotRegistered struct {
	SnapshotID string `json:"snapshotId"`
}
type ToolSnapshotDifference struct {
	ToolName string `json:"toolName"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
}
type ToolSnapshotComparison struct {
	Differences       []ToolSnapshotDifference `json:"differences"`
	Compatible        bool                     `json:"compatible"`
	ComparatorVersion string                   `json:"comparatorVersion"`
	CheckedAt         time.Time                `json:"checkedAt"`
}
type ToolRuntimeStatus struct {
	model.AgentToolRuntime
	Status         string                   `json:"status"`
	Online         bool                     `json:"online"`
	Differences    []ToolSnapshotDifference `json:"differences"`
	AffectedRuns   int                      `json:"affectedRuns"`
	UnfinishedRuns int                      `json:"unfinishedRuns"`
}
type ToolSnapshotAlert struct {
	BackendSnapshotID string     `json:"backendSnapshotId,omitempty"`
	WorkerSnapshotID  string     `json:"workerSnapshotId,omitempty"`
	HistoricalOnly    bool       `json:"historicalOnly,omitempty"`
	Code              string     `json:"code"`
	Severity          string     `json:"severity"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	WorkerIDs         []string   `json:"workerIds"`
	ToolNames         []string   `json:"toolNames"`
	FirstSeenAt       *time.Time `json:"firstSeenAt,omitempty"`
	LastSeenAt        *time.Time `json:"lastSeenAt,omitempty"`
	ResolvedAt        *time.Time `json:"resolvedAt,omitempty"`
}
type ToolSnapshotHealth struct {
	GeneratedAt  time.Time               `json:"generatedAt"`
	Backend      model.AgentToolSnapshot `json:"backend"`
	Workers      []ToolRuntimeStatus     `json:"workers"`
	Alerts       []ToolSnapshotAlert     `json:"alerts"`
	AffectedRuns int                     `json:"affectedRuns"`
	History      []ToolSnapshotAlert     `json:"history"`
	Truncated    bool                    `json:"truncated"`
}
type ToolSnapshotDetail struct {
	model.AgentToolSnapshot
	Artifact   ToolSnapshotArtifact `json:"artifact"`
	References map[string]any       `json:"references"`
}
type ToolRuntimeDrain struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Draining         bool   `json:"draining"`
	Reason           string `json:"reason"`
}

type ToolSnapshotListQuery = repository.ToolSnapshotListQuery
type ToolSnapshotList struct {
	Snapshots []model.AgentToolSnapshot `json:"snapshots"`
	Total     int64                     `json:"total"`
	Page      int                       `json:"page"`
	PageSize  int                       `json:"pageSize"`
}

func (s *Service) AdminListToolSnapshots(actor *model.User, p ToolSnapshotListQuery) (*ToolSnapshotList, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 25
	}
	if p.Page > 100000 || p.PageSize > 100 || len(p.Tool) > 160 || len(p.Version) > 120 || len(p.RunID) > 80 {
		return nil, BadAuthRequest("Invalid snapshot filters")
	}
	if p.Source != "" && p.Source != "backend" && p.Source != "worker" && p.Source != "task" {
		return nil, BadAuthRequest("Invalid snapshot source")
	}
	if _, err := s.currentBackendToolSnapshot(); err != nil {
		return nil, err
	}
	if p.RunID != "" {
		run, err := s.repo.ToolSnapshotRun(p.RunID)
		if err != nil {
			return nil, err
		}
		a, err := frozenToolArtifact(*run)
		if err != nil {
			return nil, kernel.NewAppError(404, "Task frozen tools unavailable")
		}
		if _, err = s.persistFrozenToolSnapshot(s.repo, *run, a); err != nil {
			return nil, err
		}
	}
	rows, total, err := s.repo.ListToolSnapshots(p)
	return &ToolSnapshotList{rows, total, p.Page, p.PageSize}, err
}

var backendToolInstanceID = "backend-" + uuid.NewString()
var toolSnapshotRechecks = struct {
	sync.Mutex
	last map[string]time.Time
}{last: map[string]time.Time{}}

// This is the same catalog used by the generated worker artifact contract test.
func backendToolArtifact() ToolSnapshotArtifact {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, VisionEnabled: true, HasMemories: true, WebSearchEnabled: true, SkillIDs: []string{"schema-artifact-skill"}}
	req.Budget.MaxCredits = 1
	artifact := ToolSnapshotArtifact{SchemaVersion: cloudAgentToolSchemaVersion, Tools: append(cloudAgentTools(req), nativeSkillReadToolSchema())}
	for _, request := range []CloudAgentRequest{{SubagentEnabled: true, PermissionMode: "read_only", ContextScope: []string{"canvas"}}, {PermissionMode: "read_only", ContextScope: []string{"canvas"}, subagent: &SubagentRuntime{Depth: 1}}} {
		for _, tool := range cloudAgentTools(request) {
			name := stringField(tool["function"].(map[string]any), "name")
			if strings.Contains(name, "subagent") || name == "send_parent_message" {
				artifact.Tools = append(artifact.Tools, tool)
			}
		}
	}
	return artifact
}
func normalizeToolArtifact(a ToolSnapshotArtifact) (string, error) {
	if a.SchemaVersion == "" || len(a.SchemaVersion) > 120 || len(a.Tools) == 0 || len(a.Tools) > 256 {
		return "", fmt.Errorf("invalid tool artifact")
	}
	seen := map[string]bool{}
	for _, tool := range a.Tools {
		f, ok := tool["function"].(map[string]any)
		if !ok || tool["type"] != "function" {
			return "", fmt.Errorf("invalid tool function")
		}
		name, _ := f["name"].(string)
		p, ok := f["parameters"].(map[string]any)
		if !ok || p["type"] != "object" || name == "" || len(name) > 160 || seen[name] {
			return "", fmt.Errorf("invalid or duplicate tool name")
		}
		seen[name] = true
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	if len(b) > 2<<20 {
		return "", fmt.Errorf("artifact too large")
	}
	var v any
	if err = json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	if !toolValueDepth(v, 0) {
		return "", fmt.Errorf("artifact too deep")
	}
	// Tools form a named set; object keys and tool order do not affect identity.
	sort.Slice(a.Tools, func(i, j int) bool { return toolName(a.Tools[i]) < toolName(a.Tools[j]) })
	b, err = json.Marshal(a)
	return string(b), err
}
func toolValueDepth(v any, d int) bool {
	if d > 48 {
		return false
	}
	switch x := v.(type) {
	case map[string]any:
		for _, item := range x {
			if !toolValueDepth(item, d+1) {
				return false
			}
		}
	case []any:
		for _, item := range x {
			if !toolValueDepth(item, d+1) {
				return false
			}
		}
	}
	return true
}
func toolName(t map[string]any) string {
	f, _ := t["function"].(map[string]any)
	name, _ := f["name"].(string)
	return name
}
func toolJSONEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func compatibleImageTool(name string, a, b any) bool {
	if name != "canvas_inspect_image" {
		return false
	}
	left, ok := a.(map[string]any)
	if !ok {
		return false
	}
	right, ok := b.(map[string]any)
	if !ok {
		return false
	}
	lp, ok := left["properties"].(map[string]any)
	if !ok {
		return false
	}
	rp, ok := right["properties"].(map[string]any)
	if !ok {
		return false
	}
	s, ok := rp["summary"].(map[string]any)
	if !ok || s["type"] != "object" {
		return false
	}
	required, ok := right["required"].([]any)
	if !ok {
		if values, exists := right["required"].([]string); exists {
			required = make([]any, len(values))
			for i, v := range values {
				required[i] = v
			}
		} else {
			return false
		}
	}
	for _, x := range required {
		if x == "summary" {
			return false
		}
	}
	if _, ok := lp["summary"]; ok {
		return false
	}
	clone := map[string]any{}
	for k, v := range rp {
		if k != "summary" {
			clone[k] = v
		}
	}
	r := map[string]any{}
	for k, v := range right {
		r[k] = v
	}
	r["properties"] = clone
	return toolJSONEqual(left, r)
}
func compareToolArtifacts(a, b ToolSnapshotArtifact, subset bool) []ToolSnapshotDifference {
	out := []ToolSnapshotDifference{}
	if !subset && a.SchemaVersion != b.SchemaVersion {
		out = append(out, ToolSnapshotDifference{ToolName: "", Path: "/schemaVersion", Kind: "changed", Before: a.SchemaVersion, After: b.SchemaVersion})
	}
	right := map[string]map[string]any{}
	left := map[string]bool{}
	for _, t := range b.Tools {
		right[toolName(t)] = t
	}
	for _, t := range a.Tools {
		if len(out) >= 256 {
			break
		}
		name := toolName(t)
		left[name] = true
		other, ok := right[name]
		if !ok {
			out = append(out, ToolSnapshotDifference{ToolName: name, Path: "/", Kind: "missing"})
			continue
		}
		f, _ := t["function"].(map[string]any)
		g, _ := other["function"].(map[string]any)
		if compatibleImageTool(name, f["parameters"], g["parameters"]) {
			continue
		}
		// The worker checks parameter schemas; outer descriptions may contain
		// request-specific guidance. Parameter descriptions remain strict.
		diffToolValues(name, "/function/parameters", f["parameters"], g["parameters"], &out)
	}
	if !subset {
		for _, t := range b.Tools {
			if !left[toolName(t)] && len(out) < 256 {
				out = append(out, ToolSnapshotDifference{ToolName: toolName(t), Path: "/", Kind: "added"})
			}
		}
	}
	return out
}
func diffToolValues(name, path string, a, b any, out *[]ToolSnapshotDifference) {
	if len(*out) >= 256 || toolJSONEqual(a, b) {
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := []string{}
		for k := range am {
			keys = append(keys, k)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			av, ae := am[k]
			bv, be := bm[k]
			p := path + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			if !ae || !be {
				kind := "changed"
				if !ae {
					kind = "added"
				}
				if !be {
					kind = "missing"
				}
				if len(*out) < 256 {
					*out = append(*out, ToolSnapshotDifference{name, p, kind, boundedToolValue(av), boundedToolValue(bv)})
				}
			} else {
				diffToolValues(name, p, av, bv, out)
			}
		}
		return
	}
	*out = append(*out, ToolSnapshotDifference{name, path, "changed", boundedToolValue(a), boundedToolValue(b)})
}
func boundedToolValue(v any) any {
	b, _ := json.Marshal(v)
	if len(b) > 2048 {
		return string(b[:2048]) + "…"
	}
	return v
}
func (s *Service) currentBackendToolSnapshot() (*model.AgentToolSnapshot, error) {
	s.toolSnapshotMu.Lock()
	defer s.toolSnapshotMu.Unlock()
	if s.toolBackendSnapshot != nil {
		return s.toolBackendSnapshot, nil
	}
	a := backendToolArtifact()
	contents, err := normalizeToolArtifact(a)
	if err != nil {
		return nil, err
	}
	row := &model.AgentToolSnapshot{ID: uuid.NewString(), Source: "backend", InstanceID: backendToolInstanceID, BuildCommit: buildinfo.Current().Commit, SchemaVersion: a.SchemaVersion, ToolCount: len(a.Tools), Contents: contents, CreatedAt: time.Now()}
	if err := s.repo.SaveToolSnapshot(row); err != nil {
		return nil, err
	}
	s.toolBackendSnapshot = row
	return row, nil
}
func (s *Service) RegisterToolSnapshot(in ToolSnapshotRegistration) (*ToolSnapshotRegistered, error) {
	if strings.TrimSpace(in.InstanceID) == "" || len(in.InstanceID) > 80 || strings.TrimSpace(in.BootID) == "" || len(in.BootID) > 80 || len(in.BuildCommit) > 120 || in.LoadGeneration < 1 || len(in.LoadError) > 1000 {
		return nil, BadAuthRequest("Invalid tool snapshot registration")
	}
	contents, err := normalizeToolArtifact(in.Artifact)
	if err != nil && in.LoadError == "" {
		return nil, BadAuthRequest(err.Error())
	}
	// Invalid loads are recorded without inventing a valid catalog.
	row := &model.AgentToolSnapshot{ID: uuid.NewString(), Source: "worker", InstanceID: in.InstanceID, BuildCommit: in.BuildCommit, SchemaVersion: in.Artifact.SchemaVersion, ToolCount: len(in.Artifact.Tools), Contents: contents, CreatedAt: time.Now()}
	if err != nil {
		row.Contents = "{}"
		row.SchemaVersion = ""
		row.ToolCount = 0
	}
	if err = s.repo.SaveToolSnapshot(row); err != nil {
		return nil, err
	}
	runtime := &model.AgentToolRuntime{InstanceID: in.InstanceID, BootID: in.BootID, SnapshotID: row.ID, LoadGeneration: in.LoadGeneration, LoadError: in.LoadError, LastHeartbeatAt: time.Now()}
	if err = s.repo.RegisterToolRuntime(runtime); err != nil {
		return nil, kernel.NewAppError(409, "Tool registration conflicts with the current process")
	}
	return &ToolSnapshotRegistered{row.ID}, nil
}
func (s *Service) AdminToolSnapshots(actor *model.User) ([]model.AgentToolSnapshot, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if _, err := s.currentBackendToolSnapshot(); err != nil {
		return nil, err
	}
	return s.repo.ToolSnapshots()
}
func (s *Service) AdminToolSnapshotDetail(actor *model.User, id string) (*ToolSnapshotDetail, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	row, err := s.repo.ToolSnapshot(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, kernel.NewAppError(404, "工具快照不存在")
		}
		return nil, err
	}
	var a ToolSnapshotArtifact
	if err = json.Unmarshal([]byte(row.Contents), &a); err != nil {
		return nil, err
	}
	workers, err := s.repo.ToolRuntimes()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, w := range workers {
		if w.SnapshotID == id {
			ids = append(ids, w.InstanceID)
		}
	}
	runs := []map[string]string{}
	if row.Source == "task" {
		runs = append(runs, map[string]string{"runId": row.InstanceID})
	}
	return &ToolSnapshotDetail{*row, a, map[string]any{"workerIds": ids, "workers": ids, "runs": runs}}, nil
}
func (s *Service) AdminCompareToolSnapshots(actor *model.User, left, right string) (*ToolSnapshotComparison, error) {
	a, err := s.AdminToolSnapshotDetail(actor, left)
	if err != nil {
		return nil, err
	}
	b, err := s.AdminToolSnapshotDetail(actor, right)
	if err != nil {
		return nil, err
	}
	leftArtifact, rightArtifact := a.Artifact, b.Artifact
	subset := a.Source == "task" || b.Source == "task"
	if a.Source != "task" && b.Source == "task" {
		leftArtifact, rightArtifact = rightArtifact, leftArtifact
	}
	d := compareToolArtifacts(leftArtifact, rightArtifact, subset)
	if a.Source != "task" && b.Source == "task" {
		for i := range d {
			d[i].Before, d[i].After = d[i].After, d[i].Before
			if d[i].Kind == "added" {
				d[i].Kind = "missing"
			} else if d[i].Kind == "missing" {
				d[i].Kind = "added"
			}
		}
	}
	return &ToolSnapshotComparison{Differences: d, Compatible: len(d) == 0 && len(a.Artifact.Tools) > 0 && len(b.Artifact.Tools) > 0, ComparatorVersion: "strict-tools/v1", CheckedAt: time.Now()}, nil
}
func frozenToolArtifact(run model.CloudAgentExecution) (ToolSnapshotArtifact, error) {
	var state cloudAgentRuntime
	if err := json.Unmarshal([]byte(run.StateJSON), &state); err != nil {
		return ToolSnapshotArtifact{}, err
	}
	version := state.ToolSchemaVersion
	if version == "" {
		version = "unknown"
	}
	a := ToolSnapshotArtifact{SchemaVersion: version, Tools: state.Canonical.Tools}
	if len(a.Tools) == 0 {
		return a, fmt.Errorf("frozen tools unavailable")
	}
	return a, nil
}
func (s *Service) AdminToolSnapshotHealth(actor *model.User) (*ToolSnapshotHealth, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	backend, err := s.currentBackendToolSnapshot()
	if err != nil {
		return nil, err
	}
	runtimes, err := s.repo.ToolRuntimes()
	if err != nil {
		return nil, err
	}
	runs, err := s.repo.ActiveToolRuns()
	if err != nil {
		return nil, err
	}
	health := &ToolSnapshotHealth{GeneratedAt: time.Now(), Backend: *backend, Workers: []ToolRuntimeStatus{}, Alerts: []ToolSnapshotAlert{}, History: []ToolSnapshotAlert{}}
	if len(runs) > 500 {
		runs = runs[:500]
		health.Truncated = true
		health.Alerts = append(health.Alerts, ToolSnapshotAlert{Code: "tool_snapshot_impact_truncated", Severity: "warning", Title: "任务影响检测未完成", Description: "当前仅检查前500个活动任务；影响数量为已检查范围的下限", WorkerIDs: []string{}, ToolNames: []string{}})
	}
	catalog := backendToolArtifact()
	loaded := map[string]ToolSnapshotArtifact{}
	for _, r := range runtimes {
		w := ToolRuntimeStatus{AgentToolRuntime: r, Status: "unknown", Online: r.LastHeartbeatAt.After(time.Now().Add(-45 * time.Second)), Differences: []ToolSnapshotDifference{}}
		row, e := s.repo.ToolSnapshot(r.SnapshotID)
		var a ToolSnapshotArtifact
		if e == nil {
			e = json.Unmarshal([]byte(row.Contents), &a)
		}
		if e != nil || len(a.Tools) == 0 || r.LoadError != "" {
			w.Status = "invalid"
		} else {
			loaded[r.InstanceID] = a
			w.Differences = compareToolArtifacts(catalog, a, false)
			w.Status = "matched"
			if len(w.Differences) > 0 {
				w.Status = "mismatch"
			}
		}
		if !w.Online {
			w.Status = "offline"
		}
		health.Workers = append(health.Workers, w)
	}
	// Legacy capacity reporters remain visible as unknown, never healthy.
	capacities, err := s.repo.AgentRuntimeInstances()
	if err != nil {
		return nil, err
	}
	for _, c := range capacities {
		found := false
		for _, w := range health.Workers {
			if w.InstanceID == c.ID {
				found = true
			}
		}
		if !found {
			online := c.UpdatedAt.After(time.Now().Add(-45 * time.Second))
			status := "unknown"
			if !online {
				status = "offline"
			}
			health.Workers = append(health.Workers, ToolRuntimeStatus{AgentToolRuntime: model.AgentToolRuntime{InstanceID: c.ID, LastHeartbeatAt: c.UpdatedAt}, Status: status, Online: online, Differences: []ToolSnapshotDifference{}})
		}
	}
	for _, run := range runs {
		frozen, e := frozenToolArtifact(run)
		if e == nil {
			if _, e = s.persistFrozenToolSnapshot(s.repo, run, frozen); e != nil {
				return nil, e
			}
		}
		backendOK := e == nil && len(compareToolArtifacts(frozen, catalog, true)) == 0
		available := false
		leased := run.LeaseExpiresAt != nil && run.LeaseExpiresAt.After(time.Now())
		for i := range health.Workers {
			w := &health.Workers[i]
			if leased && run.ToolRuntimeInstanceID != "" && run.ToolRuntimeInstanceID != w.InstanceID {
				continue
			}
			if run.ToolRuntimeInstanceID == w.InstanceID {
				w.UnfinishedRuns++
			}
			a := loaded[w.InstanceID]
			ok := e == nil && len(a.Tools) > 0 && len(compareToolArtifacts(frozen, a, true)) == 0
			if w.Online && (leased || !w.ManualDraining) && ok && backendOK {
				available = true
			}
			if w.Online && (!ok || !backendOK) {
				w.AffectedRuns++
			}
		}
		if !available {
			health.AffectedRuns++
		}
	}
	for _, w := range health.Workers {
		if w.Online && (w.ArtifactFileState == "changed" || w.ArtifactFileState == "unavailable") {
			health.Alerts = append(health.Alerts, ToolSnapshotAlert{Code: "tool_snapshot_artifact_file_" + w.ArtifactFileState, Severity: "warning", Title: "工具制品磁盘状态变化", Description: "进程仍使用已注册的内存快照；磁盘变化尚未加载", WorkerIDs: []string{w.InstanceID}, ToolNames: []string{}})
		}
		if w.Status == "matched" || w.Status == "offline" {
			continue
		}
		names := []string{}
		seen := map[string]bool{}
		for _, d := range w.Differences {
			if !seen[d.ToolName] {
				names = append(names, d.ToolName)
				seen[d.ToolName] = true
			}
		}
		severity := "warning"
		if w.Status == "invalid" || health.AffectedRuns > 0 {
			severity = "error"
		}
		health.Alerts = append(health.Alerts, ToolSnapshotAlert{Code: "tool_snapshot_" + w.Status, Severity: severity, Title: "工具快照状态异常", Description: "Worker loaded tools require inspection", WorkerIDs: []string{w.InstanceID}, ToolNames: names})
	}
	active := []model.AgentToolSnapshotAlert{}
	for _, alert := range health.Alerts {
		for _, id := range alert.WorkerIDs {
			snapshot := ""
			for _, w := range health.Workers {
				if w.InstanceID == id {
					snapshot = w.SnapshotID
				}
			}
			active = append(active, model.AgentToolSnapshotAlert{ID: uuid.NewString(), WorkerSnapshotID: snapshot, InstanceID: id, Code: alert.Code})
		}
	}
	history, err := s.repo.UpdateToolSnapshotAlerts(backend.ID, active)
	if err != nil {
		return nil, err
	}
	for _, event := range history {
		a := ToolSnapshotAlert{BackendSnapshotID: event.BackendSnapshotID, WorkerSnapshotID: event.WorkerSnapshotID, HistoricalOnly: event.BackendSnapshotID != backend.ID, Code: event.Code, Severity: "warning", Title: "工具快照检测事件", Description: "工具快照状态变化", WorkerIDs: []string{event.InstanceID}, ToolNames: []string{}, FirstSeenAt: &event.FirstSeenAt, LastSeenAt: &event.LastSeenAt, ResolvedAt: event.ResolvedAt}
		health.History = append(health.History, a)
		if event.ResolvedAt == nil && event.BackendSnapshotID == backend.ID {
			for i := range health.Alerts {
				if health.Alerts[i].Code == event.Code && len(health.Alerts[i].WorkerIDs) > 0 && health.Alerts[i].WorkerIDs[0] == event.InstanceID {
					matchesSnapshot := false
					for _, w := range health.Workers {
						if w.InstanceID == event.InstanceID && w.SnapshotID == event.WorkerSnapshotID {
							matchesSnapshot = true
							break
						}
					}
					if !matchesSnapshot {
						continue
					}
					health.Alerts[i].BackendSnapshotID = event.BackendSnapshotID
					health.Alerts[i].WorkerSnapshotID = event.WorkerSnapshotID
					health.Alerts[i].FirstSeenAt = &event.FirstSeenAt
					health.Alerts[i].LastSeenAt = &event.LastSeenAt
				}
			}
		}
	}
	return health, nil
}

func (s *Service) AdminRecheckToolSnapshots(actor *model.User) (*ToolSnapshotHealth, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	toolSnapshotRechecks.Lock()
	last := toolSnapshotRechecks.last[actor.ID]
	if time.Since(last) < 2*time.Second {
		toolSnapshotRechecks.Unlock()
		return nil, kernel.NewAppError(429, "Please wait before checking again")
	}
	toolSnapshotRechecks.last[actor.ID] = time.Now()
	toolSnapshotRechecks.Unlock()
	health, err := s.AdminToolSnapshotHealth(actor)
	if err != nil {
		return nil, err
	}
	audit, err := newAdminAuditEvent(actor, "agent_tool_snapshots.recheck", "agent_tool_snapshot", health.Backend.ID, "重新检测工具快照", map[string]any{"affectedRuns": health.AffectedRuns, "alerts": len(health.Alerts)})
	if err != nil {
		return nil, err
	}
	if err = s.repo.AppendAdminAudit(audit); err != nil {
		return nil, err
	}
	return health, nil
}
func (s *Service) AdminDrainToolRuntime(actor *model.User, id string, in ToolRuntimeDrain) (*ToolRuntimeStatus, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if in.ExpectedRevision < 1 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 {
		return nil, BadAuthRequest("Drain reason and current revision required")
	}
	before, err := s.repo.ToolRuntime(id)
	if err != nil {
		return nil, err
	}
	audit, err := newAdminAuditEvent(actor, "agent_tool_runtime.drain", "agent_tool_runtime", id, in.Reason, map[string]any{"before": before, "requested": in})
	if err != nil {
		return nil, err
	}
	if err = s.repo.DrainToolRuntime(id, in.ExpectedRevision, in.Draining, audit); err != nil {
		if errors.Is(err, repository.ErrCreationConflict) {
			return nil, kernel.NewAppError(409, "Worker state changed; refresh and retry")
		}
		return nil, err
	}
	health, err := s.AdminToolSnapshotHealth(actor)
	if err != nil {
		return nil, err
	}
	for _, w := range health.Workers {
		if w.InstanceID == id {
			return &w, nil
		}
	}
	return nil, fmt.Errorf("worker unavailable")
}

func (s *Service) toolClaimGuard(identity ToolSnapshotIdentity) (func(*repository.Repository, model.CloudAgentExecution) (bool, error), error) {
	if identity.InstanceID == "" && identity.BootID == "" && identity.SnapshotID == "" {
		if strings.EqualFold(os.Getenv("CANVAS_AGENT_REQUIRE_TOOL_SNAPSHOT"), "true") {
			return nil, toolRegistrationRequired()
		}
		return nil, nil
	}
	if identity.InstanceID == "" || identity.BootID == "" || identity.SnapshotID == "" || identity.LoadGeneration < 1 {
		return nil, BadAuthRequest("Incomplete worker snapshot identity")
	}
	runtime, err := s.repo.ToolRuntime(identity.InstanceID)
	if err != nil || runtime.BootID != identity.BootID || runtime.SnapshotID != identity.SnapshotID || runtime.LoadGeneration != identity.LoadGeneration {
		return nil, toolRegistrationRequired()
	}
	catalog := backendToolArtifact()
	return func(repo *repository.Repository, run model.CloudAgentExecution) (bool, error) {
		r, err := repo.LockToolRuntime(identity.InstanceID)
		if err != nil || r.BootID != identity.BootID || r.SnapshotID != identity.SnapshotID || identity.LoadGeneration != 0 && identity.LoadGeneration != r.LoadGeneration {
			return false, toolRegistrationRequired()
		}
		if r.ManualDraining || r.LoadError != "" || r.LastHeartbeatAt.Before(time.Now().Add(-45*time.Second)) {
			return false, repository.ErrToolRuntimeUnavailable
		}
		row, err := repo.ToolSnapshot(r.SnapshotID)
		if err != nil {
			return false, err
		}
		var artifact ToolSnapshotArtifact
		if json.Unmarshal([]byte(row.Contents), &artifact) != nil || len(artifact.Tools) == 0 {
			return false, nil
		}
		frozen, err := frozenToolArtifact(run)
		if err != nil {
			return false, nil
		}
		_, err = s.persistFrozenToolSnapshot(repo, run, frozen)
		if err != nil {
			return false, err
		}
		compatible := len(compareToolArtifacts(frozen, catalog, true)) == 0 && len(compareToolArtifacts(frozen, artifact, true)) == 0
		return compatible, nil
	}, nil
}

func (s *Service) persistFrozenToolSnapshot(repo *repository.Repository, run model.CloudAgentExecution, a ToolSnapshotArtifact) (*model.AgentToolSnapshot, error) {
	if row, err := repo.FrozenToolSnapshot(run.ID); err == nil {
		return row, nil
	}
	contents, err := normalizeToolArtifact(a)
	if err != nil {
		return nil, err
	}
	row := &model.AgentToolSnapshot{ID: uuid.NewString(), Source: "task", InstanceID: run.ID, SchemaVersion: a.SchemaVersion, ToolCount: len(a.Tools), Contents: contents, CreatedAt: time.Now()}
	return row, repo.SaveToolSnapshot(row)
}

func toolRegistrationRequired() error {
	e := kernel.NewAppError(409, "Worker must register loaded tool snapshot")
	e.Reason = "tool_snapshot_registration_required"
	return e
}
