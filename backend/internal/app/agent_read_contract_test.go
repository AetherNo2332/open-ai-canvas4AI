package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// TestAgentReadIsSkillOnlyAcrossBusinesses 守住 read 的唯一职责：只有本轮拿到 Skill
// 授权时才出现在目录里，任何业务都不能靠它读取自己的正文。
func TestAgentReadIsSkillOnlyAcrossBusinesses(t *testing.T) {
	library := func(_ *Service, _ string, _ CloudAgentRequest) (*model.CanvasProject, error) {
		return nil, nil
	}
	for _, item := range []struct {
		business   string
		definition AgentDefinition
	}{
		{"canvas", AgentDefinition{ID: "canvas.read-only", Version: 1, BusinessType: "canvas", IsDefault: true, BusinessTools: []string{}}},
		{"drama", AgentDefinition{ID: "drama.read-only", Version: 1, BusinessType: "drama", IsDefault: true, BusinessTools: []string{}}},
		{"library", AgentDefinition{ID: "library.read-only", Version: 1, BusinessType: "library", IsDefault: true, BusinessTools: []string{}, authorize: library}},
	} {
		t.Run(item.business, func(t *testing.T) {
			registry, err := newAgentDefinitionRegistry([]AgentDefinition{item.definition})
			if err != nil {
				t.Fatal(err)
			}
			definition := registry.latest[item.definition.ID]
			request := CloudAgentRequest{PermissionMode: "read_only", Scope: &AgentBusinessScope{Type: item.business, ID: "scope"}}
			for _, admission := range []struct {
				hasSkills bool
				want      bool
			}{{false, false}, {true, true}} {
				capabilities, err := CompileAgentCapabilities(definition, AgentCapabilityAdmission{HasSkills: admission.hasSkills})
				if err != nil {
					t.Fatal(err)
				}
				if _, present := capabilities.Tools["read"]; present != admission.want || capabilities.SkillRead != admission.want {
					t.Fatalf("hasSkills=%v disclosed read=%v skillRead=%v", admission.hasSkills, present, capabilities.SkillRead)
				}
				_, tools, err := compileAgentRunCapabilityDirectory(definition, request, admission.hasSkills)
				if err != nil {
					t.Fatal(err)
				}
				disclosed := containsString(cloudAgentToolNames(tools), "read")
				if disclosed != admission.want {
					t.Fatalf("hasSkills=%v run catalog read=%v: %v", admission.hasSkills, disclosed, cloudAgentToolNames(tools))
				}
			}
		})
	}
}

// TestAgentBusinessReadCannotUseReadTool 逐个提交业务扩展时期的四种命名空间路径：
// 即使没有能力快照（旧执行回退路径），Go 通道也只能返回可纠正的拒绝回执。
func TestAgentBusinessReadCannotUseReadTool(t *testing.T) {
	s, _, _ := cloudAgentVisionFixture(t)
	state := &cloudAgentRuntime{Request: agentTestRequest()}
	state.Request.VisionEnabled = true
	if cloudAgentRunAllowsTool(state, "read") {
		t.Fatal("read was allowed without a Skill admission")
	}
	for _, path := range []string{
		"canvas:text/shot-1",
		"canvas:image/cat",
		"drama:episode/episode-1",
		"library:document/chapter-1",
	} {
		t.Run(path, func(t *testing.T) {
			call := piAgentTestCall("read-"+path, "read", `{"path":"`+path+`"}`)
			outcome := s.runCloudAgentCheckpointTool(&cloudAgentToolCheckpoint{
				run: &model.CloudAgentExecution{UserID: "user", ID: "run"}, state: state, call: call, allowed: true,
			}, nil, (*repository.Repository)(nil))
			if outcome.finished || outcome.result != nil || outcome.toolErr == nil {
				t.Fatalf("wrong-channel read produced a result: %+v", outcome)
			}
			var appErr *kernel.AppError
			if !errors.As(outcome.toolErr, &appErr) {
				t.Fatalf("rejection is not a business receipt: %v", outcome.toolErr)
			}
			message := cloudAgentSafeToolError(outcome.toolErr)
			for _, want := range []string{"Skill", "canvas_read_content", "canvas_inspect_image", "drama_read_episode"} {
				if !strings.Contains(message, want) {
					t.Fatalf("rejection lost the correction hint %q: %s", want, message)
				}
			}
		})
	}
}

// TestAgentToolCatalogMatchesFrontendNames 把离线工具全集、前端已知工具名和按业务
// 过滤后的目录钉在一起：新增或改名工具时三处必须同步。
func TestAgentToolCatalogMatchesFrontendNames(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "lib", "canvas", "agent-tool-presentation.ts"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	start := strings.Index(source, "export const AGENT_TOOL_NAMES = [")
	end := strings.Index(source[start:], "] as const;")
	if start < 0 || end < 0 {
		t.Fatal("web AGENT_TOOL_NAMES list not found")
	}
	frontend := map[string]bool{}
	for _, line := range strings.Split(source[start:start+end], "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
		if len(line) >= 2 && line[0] == '"' && line[len(line)-1] == '"' {
			frontend[strings.Trim(line, `"`)] = true
		}
	}
	if len(frontend) == 0 {
		t.Fatal("frontend tool name list is empty")
	}
	supported := CloudAgentSupportedToolNames()
	supportedSet := map[string]bool{}
	for _, name := range supported {
		if supportedSet[name] {
			t.Fatalf("offline catalog repeats %q", name)
		}
		supportedSet[name] = true
	}
	for name := range frontend {
		if !supportedSet[name] {
			t.Fatalf("frontend knows %q but the offline catalog does not", name)
		}
	}
	// 每个业务过滤后的目录必须带自己的专用读取工具，且没有别的业务的写入工具。
	service := &Service{}
	for _, item := range []struct {
		business string
		request  CloudAgentRequest
		want     []string
		absent   []string
	}{
		{"canvas", CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}, VisionEnabled: true},
			[]string{"canvas_read_content", "canvas_inspect_image"}, []string{"drama_read_episode", "read"}},
		{"drama", CloudAgentRequest{PermissionMode: "read_only", Scope: &AgentBusinessScope{Type: "drama", ID: "catalog-drama"}},
			[]string{"drama_read_episode"}, []string{"canvas_read_content", "canvas_inspect_image", "read"}},
	} {
		definition, err := service.ResolveAgentDefinition(item.business, "")
		if err != nil {
			t.Fatal(err)
		}
		_, tools, err := compileAgentRunCapabilityDirectory(definition, item.request, false)
		if err != nil {
			t.Fatal(err)
		}
		names := cloudAgentToolNames(tools)
		for _, want := range item.want {
			if !containsString(names, want) {
				t.Fatalf("%s catalog lost %q: %v", item.business, want, names)
			}
		}
		for _, absent := range item.absent {
			if containsString(names, absent) {
				t.Fatalf("%s catalog disclosed %q without authorization: %v", item.business, absent, names)
			}
		}
	}
}

func TestAgentRegisteredToolNamesFollowSnakeCase(t *testing.T) {
	for name, want := range map[string]bool{
		"library_read_document":  true,
		"canvas_read_content":    true,
		"drama_read_episode":     true,
		"read2":                  true,
		"Library_Read_Document":  false,
		"library-read-document":  false,
		"library__read":          false,
		"library_read_":          false,
		"2library_read":          false,
		"_library_read":          false,
		"library read":           false,
		"agent_tools_control":    false,
		strings.Repeat("a", 64):  true,
		strings.Repeat("a", 65):  false,
		strings.Repeat("a_", 33): false,
	} {
		tool := fixtureAgentTool(name, "library", "命名校验用工具")
		tools := map[string]agentToolRegistration{}
		err := registerAgentTool(tools, tool)
		if got := err == nil; got != want {
			t.Fatalf("registerAgentTool(%q) accepted=%v want %v (%v)", name, got, want, err)
		}
	}
}
