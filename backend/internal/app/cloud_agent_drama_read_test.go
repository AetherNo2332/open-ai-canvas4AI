package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func dramaReadFixture(t *testing.T) (*Service, *gorm.DB, AgentBusinessScope) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	scope := AgentBusinessScope{Type: "drama", ID: "drama-project"}
	for _, row := range []any{
		&model.Project{ID: scope.ID, UserID: "user", Name: "短剧项目", Status: model.ProjectStatusActive},
		&model.DramaProduction{ProjectID: scope.ID, UserID: "user", Status: "paused", Stage: "script"},
		&model.DramaEpisode{ID: "episode-1", ProjectID: scope.ID, Number: 1, Title: "第一集", Synopsis: "EPISODE_SYNOPSIS"},
		&model.DramaShot{ID: "shot-1", ProjectID: scope.ID, EpisodeID: "episode-1", Position: 0, Scene: "SCENE_ONE", Dialogue: "DIALOGUE_ONE"},
		&model.DramaShot{ID: "shot-2", ProjectID: scope.ID, EpisodeID: "episode-1", Position: 1, Scene: "SCENE_TWO", Dialogue: "DIALOGUE_TWO"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s, db, scope
}

func dramaReadState(scope AgentBusinessScope) cloudAgentRuntime {
	return cloudAgentRuntime{Request: CloudAgentRequest{Scope: &scope, PermissionMode: "read_only"}}
}

func dramaReadCall(t *testing.T, arguments string) cloudAgentCall {
	t.Helper()
	return piAgentTestCall("drama-read", "drama_read_episode", arguments)
}

func TestDramaReadEpisodeTool(t *testing.T) {
	s, _, scope := dramaReadFixture(t)
	state := dramaReadState(scope)
	result, err := s.cloudAgentReadDramaEpisode("user", &state, dramaReadCall(t, `{"episodeId":"episode-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID != scope.ID || result.EpisodeID != "episode-1" || result.Adopted {
		t.Fatalf("unexpected receipt identity: %+v", result)
	}
	if result.ContentType != "text/plain" || result.Visibility != "authorized" {
		t.Fatalf("unexpected receipt classification: %+v", result)
	}
	for _, want := range []string{"EPISODE_SYNOPSIS", "SCENE_ONE", "DIALOGUE_TWO"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("episode content missing %q: %s", want, result.Content)
		}
	}
	// 缺省分页读取整份正文：offset 0、到末尾、无后续。
	if result.Offset != 0 || result.NextOffset != result.TotalRunes || result.HasMore {
		t.Fatalf("default page is not the full body: %+v", result)
	}
	t.Run("tool is disclosed for the drama business only", func(t *testing.T) {
		definition, err := s.ResolveAgentDefinition("drama", "")
		if err != nil {
			t.Fatal(err)
		}
		// 无 Skill 授权的真实运行目录：只有具名短剧工具，没有通用的 Skill read。
		_, tools, err := compileAgentRunCapabilityDirectory(definition, cloudAgentRequestForBusiness(scope), false)
		if err != nil {
			t.Fatal(err)
		}
		names := cloudAgentToolNames(tools)
		if !containsString(names, "drama_read_episode") {
			t.Fatalf("drama catalog lost drama_read_episode: %v", names)
		}
		if containsString(names, "canvas_read_content") || containsString(names, "read") {
			t.Fatalf("drama catalog leaked canvas or Skill read: %v", names)
		}
		canvas := agentTestRequest()
		if containsString(cloudAgentToolNames(cloudAgentTools(canvas)), "drama_read_episode") {
			t.Fatal("canvas catalog exposed the drama read tool")
		}
	})
	t.Run("unknown and missing arguments are rejected", func(t *testing.T) {
		for name, arguments := range map[string]string{
			"unknown field": `{"episodeId":"episode-1","readPath":"drama:episode/episode-1"}`,
			"missing id":    `{}`,
			"empty id":      `{"episodeId":"  "}`,
			"wrong type":    `{"episodeId":1}`,
		} {
			_, err := s.cloudAgentReadDramaEpisode("user", &state, dramaReadCall(t, arguments))
			var argumentErr *cloudAgentArgumentError
			if !errors.As(err, &argumentErr) {
				t.Fatalf("%s: want a correctable argument error, got %v", name, err)
			}
		}
	})
	t.Run("canvas run cannot read a drama episode", func(t *testing.T) {
		canvasState := cloudAgentRuntime{Request: agentTestRequest()}
		if _, err := s.cloudAgentReadDramaEpisode("user", &canvasState, dramaReadCall(t, `{"episodeId":"episode-1"}`)); err == nil {
			t.Fatal("canvas run executed the drama read tool")
		}
	})
}

func TestDramaReadEpisodeScopeAndPagination(t *testing.T) {
	s, db, scope := dramaReadFixture(t)
	if err := db.Create(&model.Project{ID: "other-project", UserID: "user", Name: "另一部", Status: model.ProjectStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.DramaProduction{ProjectID: "other-project", UserID: "user", Status: "paused", Stage: "script"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.DramaEpisode{ID: "episode-other", ProjectID: "other-project", Number: 1, Title: "别的集", Synopsis: "OTHER_PROJECT_TEXT"}).Error; err != nil {
		t.Fatal(err)
	}
	fullState := dramaReadState(scope)
	full, err := s.cloudAgentReadDramaEpisode("user", &fullState, dramaReadCall(t, `{"episodeId":"episode-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("cross-project and cross-user calls are refused", func(t *testing.T) {
		state := dramaReadState(scope)
		if _, err := s.cloudAgentReadDramaEpisode("user", &state, dramaReadCall(t, `{"episodeId":"episode-other"}`)); err == nil {
			t.Fatal("read an episode from another project")
		}
		if _, err := s.cloudAgentReadDramaEpisode("other", &state, dramaReadCall(t, `{"episodeId":"episode-1"}`)); err == nil {
			t.Fatal("read an episode as another user")
		}
	})
	t.Run("revoked project permission is refused", func(t *testing.T) {
		if err := db.Where("project_id = ?", scope.ID).Delete(&model.DramaProduction{}).Error; err != nil {
			t.Fatal(err)
		}
		state := dramaReadState(scope)
		if _, err := s.cloudAgentReadDramaEpisode("user", &state, dramaReadCall(t, `{"episodeId":"episode-1"}`)); err == nil {
			t.Fatal("read survived revoked drama permission")
		}
		if err := db.Create(&model.DramaProduction{ProjectID: scope.ID, UserID: "user", Status: "paused", Stage: "script"}).Error; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("explicit illegal paging is rejected", func(t *testing.T) {
		state := dramaReadState(scope)
		for name, arguments := range map[string]string{
			"zero limit":      `{"episodeId":"episode-1","limit":0}`,
			"negative limit":  `{"episodeId":"episode-1","limit":-1}`,
			"over max limit":  `{"episodeId":"episode-1","limit":12001}`,
			"negative offset": `{"episodeId":"episode-1","offset":-1}`,
		} {
			if _, err := s.cloudAgentReadDramaEpisode("user", &state, dramaReadCall(t, arguments)); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		}
		if full.TotalRunes <= 0 {
			t.Fatal("fixture produced no content")
		}
	})
	t.Run("unicode paging reproduces the full body", func(t *testing.T) {
		state := dramaReadState(scope)
		var assembled strings.Builder
		offset, pages := 0, 0
		for {
			call := dramaReadCall(t, `{"episodeId":"episode-1","offset":`+strconv.Itoa(offset)+`,"limit":7}`)
			page, err := s.cloudAgentReadDramaEpisode("user", &state, call)
			if err != nil {
				t.Fatal(err)
			}
			if page.Offset != offset || page.TotalRunes != full.TotalRunes {
				t.Fatalf("page cursor drifted: %+v", page)
			}
			assembled.WriteString(page.Content)
			pages++
			if !page.HasMore {
				if page.NextOffset != page.TotalRunes {
					t.Fatalf("last page did not reach the end: %+v", page)
				}
				break
			}
			if page.NextOffset != offset+len([]rune(page.Content)) || page.NextOffset <= offset {
				t.Fatalf("next offset is not the page end: %+v", page)
			}
			offset = page.NextOffset
			if pages > 200 {
				t.Fatal("paging did not terminate")
			}
		}
		if assembled.String() != full.Content || pages < 2 {
			t.Fatalf("paged content differs from the full body (pages=%d)", pages)
		}
	})
	t.Run("offset past the end returns the trailing empty page", func(t *testing.T) {
		state := dramaReadState(scope)
		page, err := s.cloudAgentReadDramaEpisode("user", &state, dramaReadCall(t, `{"episodeId":"episode-1","offset":999999}`))
		if err != nil {
			t.Fatal(err)
		}
		if page.Content != "" || page.Offset != page.TotalRunes || page.NextOffset != page.TotalRunes || page.HasMore {
			t.Fatalf("expected the trailing empty page: %+v", page)
		}
	})
}

func TestDramaReadEpisodeBudgetAndReplay(t *testing.T) {
	s, db, scope := dramaReadFixture(t)
	t.Run("shared read budget stops the loop", func(t *testing.T) {
		state := dramaReadState(scope)
		state.CallLimits = cloudAgentCallLimits{ReadCallsPerRun: 1}
		if _, err := cloudAgentReadToolCached(s.repo, "user", &state, dramaReadCall(t, `{"episodeId":"episode-1"}`), s); err != nil {
			t.Fatal(err)
		}
		_, err := cloudAgentReadToolCached(s.repo, "user", &state, dramaReadCall(t, `{"episodeId":"episode-1"}`), s)
		var loopErr *cloudAgentReadLoopError
		if !errors.As(err, &loopErr) || !loopErr.Budget || state.ReadToolCalls != 1 {
			t.Fatalf("drama read ignored the shared read budget: count=%d err=%v", state.ReadToolCalls, err)
		}
	})
	t.Run("durable receipt replay does not read again", func(t *testing.T) {
		req := agentTestRequest()
		req.Scope, req.CanvasID, req.ContextScope = &scope, "", nil
		req.IdempotencyKey = "drama-read-replay"
		if _, err := s.CreateCloudAgentRun("user", req, ""); err != nil {
			t.Fatal(err)
		}
		run, state := startPiAgentFirstStep(t, s, agentLatestRunID(t, db))
		call := dramaReadCall(t, `{"episodeId":"episode-1"}`)
		taskID := state.ActiveTaskID
		batch, _ := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{call}})
		if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{
			"status": model.TaskStatusSucceeded, "result_json": string(batch),
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state.ActiveTaskID, state.LastStepTaskID = "", taskID
			return cloudAgentSave(current, state)
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}); err != nil {
			t.Fatal(err)
		}
		first, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, call.ID)
		if err != nil || first.IsError || !strings.Contains(string(first.Result), "EPISODE_SYNOPSIS") {
			t.Fatalf("drama tool produced no episode content: %+v %v", first, err)
		}
		_, after := reloadPiRun(t, s, run.ID)
		if after.ReadToolCalls != 1 {
			t.Fatalf("drama read was not counted exactly once: %d", after.ReadToolCalls)
		}
		// 同一 callID 重放返回已持久化的回执，不重新执行，也不重复计数。
		replayed, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, call.ID)
		if err != nil || string(replayed.Result) != string(first.Result) {
			t.Fatalf("durable receipt replay changed: %+v %v", replayed, err)
		}
		_, replayState := reloadPiRun(t, s, run.ID)
		if replayState.ReadToolCalls != 1 {
			t.Fatalf("replay consumed the read budget: %d", replayState.ReadToolCalls)
		}
	})
}

func cloudAgentRequestForBusiness(scope AgentBusinessScope) CloudAgentRequest {
	return CloudAgentRequest{PermissionMode: "read_only", Scope: &scope}
}

func ptrRuntime(state cloudAgentRuntime) *cloudAgentRuntime { return &state }

func agentLatestRunID(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var run model.CloudAgentExecution
	if err := db.Order("created_at desc").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	return run.ID
}
