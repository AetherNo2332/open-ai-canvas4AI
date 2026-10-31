package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// Shared account and canvas fixture for rules and read-cache tests.
func cloudAgentRulesFixture(t *testing.T) (*Service, string, string) {
	s, _, projectID, canvasID := cloudAgentRulesFixtureDB(t)
	return s, projectID, canvasID
}

func cloudAgentRulesFixtureDB(t *testing.T) (*Service, *gorm.DB, string, string) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	now := time.Now()
	for _, item := range []any{
		&model.Project{ID: "project-1", UserID: "user", Name: "Agent 项目", Status: model.ProjectStatusActive, CreatedAt: now, UpdatedAt: now},
		&model.Project{ID: "project-2", UserID: "other", Name: "其他用户项目", Status: model.ProjectStatusActive, CreatedAt: now, UpdatedAt: now},
		&model.CanvasProject{ID: "agent-canvas", UserID: "user", ProjectID: "project-1", Title: "Agent 画布", PayloadJSON: `{"nodes":[],"connections":[]}`, CreatedAt: now, UpdatedAt: now},
		&model.CanvasProject{ID: "foreign-canvas", UserID: "other", ProjectID: "project-2", Title: "其他用户画布", PayloadJSON: `{"nodes":[],"connections":[]}`, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s, db, "project-1", "agent-canvas"
}

func cloudAgentToolNamesFromTaskInput(t *testing.T, raw string) []string {
	t.Helper()
	var input struct {
		AgentRequests struct {
			Canonical struct {
				Tools []map[string]any `json:"tools"`
			} `json:"canonical"`
		} `json:"agentRequests"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(input.AgentRequests.Canonical.Tools))
	for _, tool := range input.AgentRequests.Canonical.Tools {
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func TestCloudAgentReadToolCacheReplaysReadResultsAndErrors(t *testing.T) {
	s, _, canvasID := cloudAgentRulesFixture(t)
	state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: canvasID}}
	call := cloudAgentCall{ID: "canvas-read"}
	call.Function.Name, call.Function.Arguments = "canvas_get_state", `{}`

	first, err := cloudAgentReadToolCached(s.repo, "user", state, call)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	second, err := cloudAgentReadToolCached(s.repo, "user", state, call)
	if err != nil {
		t.Fatalf("cached read failed: %v", err)
	}
	if len(first.(map[string]any)) == 0 || second.(map[string]any)["cacheReplay"] != true || second.(map[string]any)["replayCount"] != 1 {
		t.Fatalf("cached read did not return a replay receipt: first=%#v second=%#v", first, second)
	}
	if message, _ := second.(map[string]any)["message"].(string); message != "结果与上次读取相同，可直接使用；若用户刚修改过画布或纠正了你，请重新读取需要的节点" {
		t.Fatalf("replay receipt wording must allow re-reading after user changes: %q", message)
	}
	var cached map[string]any
	cacheKey := cloudAgentReadCacheKeyForState(s.repo, "user", state, call)
	if err := json.Unmarshal(state.ToolReadResults[cacheKey].Result, &cached); err != nil || len(cached) == 0 {
		t.Fatalf("full cached result was not retained for %q: bytes=%d err=%v", cacheKey, len(state.ToolReadResults[cacheKey].Result), err)
	}

	bad := call
	bad.Function.Arguments = `{"bogus":1}`
	_, firstErr := cloudAgentReadToolCached(s.repo, "user", state, bad)
	_, secondErr := cloudAgentReadToolCached(s.repo, "user", state, bad)
	if firstErr == nil || secondErr == nil || firstErr.Error() != secondErr.Error() {
		t.Fatalf("cached read error changed: first=%v second=%v", firstErr, secondErr)
	}
	if !state.ToolReadResults[cloudAgentReadCacheKeyForState(s.repo, "user", state, bad)].ArgumentError || len(state.ToolReadResults) != 2 {
		t.Fatalf("unexpected read cache entry count: %d", len(state.ToolReadResults))
	}
}

func TestCloudAgentReadToolCacheReplaysRepeatedIdenticalReads(t *testing.T) {
	s, _, canvasID := cloudAgentRulesFixture(t)
	state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: canvasID}}
	call := cloudAgentCall{ID: "canvas-read"}
	call.Function.Name, call.Function.Arguments = "canvas_get_state", `{}`

	if _, err := cloudAgentReadToolCached(s.repo, "user", state, call); err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if _, err := cloudAgentReadToolCached(s.repo, "user", state, call); err != nil {
		t.Fatalf("the first cached replay should still be allowed: %v", err)
	}
	third, err := cloudAgentReadToolCached(s.repo, "user", state, call)
	if err != nil {
		t.Fatalf("repeated cached read must remain usable: %v", err)
	}
	if replay, ok := third.(map[string]any); !ok || replay["cacheReplay"] != true || replay["replayCount"] != 2 {
		t.Fatalf("third identical read did not return a replay receipt: %#v", third)
	}
	if state.ReadToolCalls != 1 {
		t.Fatalf("cached replays consumed read budget: %d", state.ReadToolCalls)
	}
}

func TestCloudAgentCanvasWriteInvalidatesOnlyCanvasReadResults(t *testing.T) {
	state := &cloudAgentRuntime{
		ToolReadResults: map[string]cloudAgentCachedToolResult{
			`canvas_get_state:{}`: {Result: json.RawMessage(`{"nodes":[]}`)},
			`storyboard:{}`:       {Result: json.RawMessage(`{"shots":[]}`)},
			`skill_read_file:{}`:  {Result: json.RawMessage(`{"content":"skill"}`)},
			`model_list:{}`:       {Result: json.RawMessage(`{"models":[]}`)},
		},
		ToolReadReplays: map[string]int{
			`canvas_get_state:{}`: 2,
			`storyboard:{}`:       1,
			`skill_read_file:{}`:  3,
			`model_list:{}`:       4,
		},
	}

	cloudAgentInvalidateReadCache(state)
	if _, exists := state.ToolReadResults[`canvas_get_state:{}`]; exists {
		t.Fatal("canvas state cache survived a canvas write")
	}
	if _, exists := state.ToolReadResults[`storyboard:{}`]; exists {
		t.Fatal("storyboard cache survived a canvas write")
	}
	for _, key := range []string{`skill_read_file:{}`, `model_list:{}`} {
		if _, exists := state.ToolReadResults[key]; !exists {
			t.Fatalf("unrelated read cache %q was invalidated", key)
		}
	}
	if _, exists := state.ToolReadReplays[`canvas_get_state:{}`]; exists {
		t.Fatal("canvas replay count survived invalidation")
	}
	if state.ToolReadReplays[`skill_read_file:{}`] != 3 || state.ToolReadReplays[`model_list:{}`] != 4 {
		t.Fatalf("unrelated replay counts changed: %#v", state.ToolReadReplays)
	}
}

func TestCloudAgentReadToolCacheStopsCrossArgumentReadLoops(t *testing.T) {
	s, _, _, canvasID := cloudAgentRulesFixtureDB(t)
	state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: canvasID}}
	for index := 0; index < cloudAgentMaxReadToolCallsPerRun; index++ {
		call := cloudAgentCall{ID: "canvas-read"}
		call.Function.Name = "canvas_get_state"
		call.Function.Arguments = fmt.Sprintf(`{"offset":%d}`, index)
		if _, err := cloudAgentReadToolCached(s.repo, "user", state, call); err != nil {
			t.Fatalf("read %d unexpectedly failed before budget: %v", index+1, err)
		}
	}
	over := cloudAgentCall{ID: "canvas-read-over-budget"}
	over.Function.Name = "canvas_get_state"
	over.Function.Arguments = fmt.Sprintf(`{"offset":%d}`, cloudAgentMaxReadToolCallsPerRun)
	_, err := cloudAgentReadToolCached(s.repo, "user", state, over)
	var loopErr *cloudAgentReadLoopError
	if !errors.As(err, &loopErr) || !loopErr.Budget || loopErr.Count != cloudAgentMaxReadToolCallsPerRun+1 {
		t.Fatalf("expected cross-argument read budget guard, got %v", err)
	}
}

func TestCloudAgentReadCacheNormalizesObjectKeyOrder(t *testing.T) {
	s, _, canvasID := cloudAgentRulesFixture(t)
	state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: canvasID}}
	first := cloudAgentCall{ID: "canvas-read-1"}
	first.Function.Name, first.Function.Arguments = "canvas_get_state", `{"view":"catalog","unused":null}`
	second := first
	second.ID = "canvas-read-2"
	second.Function.Arguments = `{"unused":null,"view":"catalog"}`

	firstResult, firstErr := cloudAgentReadToolCached(s.repo, "user", state, first)
	if firstErr == nil || firstResult != nil {
		t.Fatalf("the first call should cache an argument error: result=%#v err=%v", firstResult, firstErr)
	}
	secondResult, secondErr := cloudAgentReadToolCached(s.repo, "user", state, second)
	if secondErr == nil || secondResult != nil || secondErr.Error() != firstErr.Error() {
		t.Fatalf("the second call should replay the cached argument error: result=%#v first=%v second=%v", secondResult, firstErr, secondErr)
	}
	if cloudAgentReadCacheKey(first) != cloudAgentReadCacheKey(second) {
		t.Fatalf("object key order changed the cache key: %q vs %q", cloudAgentReadCacheKey(first), cloudAgentReadCacheKey(second))
	}
}

func TestCloudAgentReadLoopErrorStatesFactsWithoutForbiddingReads(t *testing.T) {
	budget := (&cloudAgentReadLoopError{Budget: true, Count: 40}).Error()
	if budget != "Agent 本轮只读工具调用已达到安全上限（40 次），本轮已停止；请基于已读到的内容答复，或请用户确认后开新一轮" {
		t.Fatalf("unexpected budget guard wording: %q", budget)
	}
	repeat := (&cloudAgentReadLoopError{ToolName: "canvas_get_state"}).Error()
	if repeat != "Agent 连续多次读取同一份canvas_get_state且结果未变，本轮已停止；请基于已读到的内容继续" {
		t.Fatalf("unexpected repeat guard wording: %q", repeat)
	}
}
