package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func TestAgentCapabilityBudgetRejectsBeforeAdmission(t *testing.T) {
	service, db := agentRunFixture(t)
	declareTestChannelWindow(t, db, 4096, 1024)
	request := agentTestRequest()
	if limit := service.cloudAgentContextBudgetForRequest(request); !limit.Configured || limit.InputBudgetTokens != 1024 {
		t.Fatalf("unexpected fixture budget: %+v", limit)
	}
	_, err := service.CreateCloudAgentRun("user", request, "")
	var rejected *kernel.AppError
	if !errors.As(err, &rejected) || rejected.Reason != "agent_context_budget_exceeded" {
		t.Fatalf("expected context budget rejection before creating a run, got %v", err)
	}
	if rejected.Details["diagnosticId"] == nil || rejected.Details["toolTokens"] == nil {
		t.Fatalf("missing budget diagnostics: %+v", rejected.Details)
	}
	assertNoAgentAdmission(t, db, request)
}

func assertNoAgentAdmission(t *testing.T, db *gorm.DB, request CloudAgentRequest) {
	t.Helper()
	runID := cloudAgentID("user", request.IdempotencyKey)
	for _, entry := range []struct {
		table  any
		column string
	}{
		{&model.Task{}, "id"},
		{&model.CloudAgentExecution{}, "id"},
		{&model.BillingOrder{}, "task_id"},
	} {
		var count int64
		if err := db.Model(entry.table).Where(entry.column+" = ?", runID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rejected admission created %T: count=%d err=%v", entry.table, count, err)
		}
	}
	assertReservationInvariant(t, db, "user")
}

func TestAgentCapabilityByteBudgetBeforeAdmission(t *testing.T) {
	for _, item := range []struct {
		budget string
		count  int
		size   int
		limit  int
	}{
		{"tool_bytes", 1, 12000, 12000},
		{"directory_bytes", 7, 10000, 64000},
	} {
		t.Run(item.budget, func(t *testing.T) {
			service, db := agentRunFixture(t)
			definition := AgentDefinition{ID: "canvas.fixture", Version: 1, BusinessType: "canvas", IsDefault: true}
			var registrations []agentToolRegistration
			for index := range item.count {
				name := fmt.Sprintf("fixture_read_%d", index)
				definition.BusinessTools = append(definition.BusinessTools, name)
				registrations = append(registrations, fixtureAgentTool(name, "canvas", strings.Repeat("x", item.size)))
			}
			registry, err := newAgentRegistry([]AgentDefinition{definition}, registrations)
			if err != nil {
				t.Fatal(err)
			}
			service.agentDefinitions = registry
			request := agentTestRequest()
			_, err = service.CreateCloudAgentRun("user", request, "")
			var rejected *kernel.AppError
			if !errors.As(err, &rejected) || rejected.Reason != "agent_tool_schema_budget_exceeded" ||
				rejected.Details["budget"] != item.budget || rejected.Details["limit"] != item.limit ||
				rejected.Details["diagnosticId"] == nil {
				t.Fatalf("expected %s rejection before admission: %v", item.budget, err)
			}
			if actual, ok := rejected.Details["actual"].(int); !ok || actual <= item.limit ||
				(item.count == 1 && rejected.Details["tool"] != definition.BusinessTools[0]) {
				t.Fatalf("wrong budget diagnostics: %+v", rejected.Details)
			}
			assertNoAgentAdmission(t, db, request)
		})
	}
}

func TestAgentCapabilityRejectsUnsupportedModelBeforeAdmission(t *testing.T) {
	service, db := agentRunFixture(t)
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	supported := false
	capability.Text.ToolCallingSupported = &supported
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, capability)).Error; err != nil {
		t.Fatal(err)
	}
	request := agentTestRequest()
	_, err := service.CreateCloudAgentRun("user", request, "")
	var rejected *kernel.AppError
	if !errors.As(err, &rejected) || rejected.Reason != "agent_model_tools_unsupported" {
		t.Fatalf("expected unsupported model rejection, got %v", err)
	}
	assertNoAgentAdmission(t, db, request)
}

func fixtureAgentTool(name, scope, description string) agentToolRegistration {
	compiler := &cloudAgentToolCompiler{}
	compiler.add(name, description, map[string]any{})
	return agentToolRegistration{
		name: name, businesses: []string{scope}, version: 1, schema: compiler.tools[0],
		execute: func(service *Service, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, _ bool) error {
			return service.recordCloudAgentToolReceipt(run, state, call, map[string]any{"registered": true}, nil)
		},
	}
}

func TestAgentCapabilityThirdBusinessToolExecutesAndRecovers(t *testing.T) {
	service, db := agentRunFixture(t)
	registration := fixtureAgentTool("library_lookup", "library", "Read from a test library")
	original := AgentDefinition{
		ID: "library.default", Version: 1, BusinessType: "library", IsDefault: true, ReadOnly: true,
		BusinessTools: []string{registration.name},
		authorize: func(_ *Service, user string, req CloudAgentRequest) (*model.CanvasProject, error) {
			if user != "user" || agentBusinessScope(req).ID != "shelf" {
				return nil, kernel.Forbidden("library access denied")
			}
			return nil, nil
		},
	}
	buildRegistry := func(definitions []AgentDefinition, tools ...agentToolRegistration) *agentDefinitionRegistry {
		t.Helper()
		registry, err := newAgentRegistry(definitions, tools)
		if err != nil {
			t.Fatal(err)
		}
		return registry
	}
	service.agentDefinitions = buildRegistry([]AgentDefinition{original}, registration)
	request := agentTestRequest()
	request.CanvasID, request.ContextScope = "", nil
	request.Scope = &AgentBusinessScope{Type: "library", ID: "shelf"}
	run, err := service.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimPiAgent("worker-first")
	if err != nil || claimed == nil || claimed.RunID != run.ID {
		t.Fatalf("claim: %v", err)
	}
	if !slices.Equal(claimed.ToolCatalog, []string{"plan_update", "finish_run", "library_lookup"}) {
		t.Fatalf("third business tools: %v", claimed.ToolCatalog)
	}
	latest := original
	latest.Version, latest.BusinessTools = 2, []string{registration.name, "library_new"}
	service.agentDefinitions = buildRegistry([]AgentDefinition{original, latest}, registration, fixtureAgentTool("library_new", "library", "New tool"))
	past := time.Now().UTC().Add(-time.Minute)
	for _, item := range []struct {
		table any
		id    string
	}{{&model.CloudAgentExecution{}, run.ID}, {&model.CloudAgentPiSession{}, claimed.PiSessionID}} {
		if err := db.Model(item.table).Where("id = ?", item.id).Update("lease_expires_at", past).Error; err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := service.ClaimPiAgent("worker-second")
	if err != nil || recovered == nil || recovered.CapabilityDirectory != claimed.CapabilityDirectory ||
		!slices.Equal(recovered.ToolCatalog, claimed.ToolCatalog) {
		t.Fatalf("recovery changed original capability directory: %v", err)
	}
	execution, state := reloadPiRun(t, service, run.ID)
	injected := piAgentTestCall("injected", "library_new", `{}`)
	if result := cloudAgentPreflightBatch(state, []cloudAgentCall{injected})[0]; result.Allowed {
		t.Fatal("latest configuration expanded a frozen run")
	}
	if err := service.checkRegisteredAgentToolBeforeDispatch(execution, state, injected); err == nil {
		t.Fatal("unadvertised tool passed dispatch authorization")
	}
	_, after := agentSettleToolStep(t, service, db, run.ID, "lookup", "library_lookup", `{}`)
	receipt := piToolReceipt(&after, after.PiToolBatchTaskID, "lookup")
	if receipt == nil || receipt.IsError || !strings.Contains(string(receipt.Result), `"registered":true`) {
		t.Fatalf("registered executor did not return a receipt: %+v", receipt)
	}
}

func TestAgentCapabilityRegistrationRejectsMissingAndDuplicateHandlers(t *testing.T) {
	definition := AgentDefinition{ID: "canvas.empty", Version: 1, BusinessType: "canvas", IsDefault: true, BusinessTools: []string{}}
	valid := fixtureAgentTool("fixture", "canvas", "Read fixture")
	missing := valid
	missing.execute = nil
	for _, registrations := range [][]agentToolRegistration{{missing}, {valid, valid}} {
		if _, err := newAgentRegistry([]AgentDefinition{definition}, registrations); err == nil {
			t.Fatal("invalid tool registration accepted")
		}
	}
}

func TestAgentCapabilityEmptyBusinessToolsAdmitRun(t *testing.T) {
	service, _ := agentRunFixture(t)
	registry, err := newAgentDefinitionRegistry([]AgentDefinition{{
		ID: "canvas.empty", Version: 1, BusinessType: "canvas", IsDefault: true, BusinessTools: []string{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service.agentDefinitions = registry
	run, err := service.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimPiAgent("worker")
	if err != nil || claimed == nil || claimed.RunID != run.ID {
		t.Fatalf("claim empty business tool run: %v", err)
	}
	if !slices.Equal(claimed.ToolCatalog, []string{"plan_update", "finish_run"}) {
		t.Fatalf("empty business tool configuration inherited tools: %v", claimed.ToolCatalog)
	}
}

func TestAgentCapabilityIncompatibleDirectoryRefundsHoldingOrder(t *testing.T) {
	service, db := agentRunFixture(t)
	created, err := service.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	run, state := reloadPiRun(t, service, created.ID)
	state.Capabilities.DirectoryVersion++
	if err := cloudAgentSave(run, state); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(run).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimPiAgent("worker")
	var rejected *kernel.AppError
	if claimed != nil || !errors.As(err, &rejected) || rejected.Reason != "agent_capability_directory_unavailable" {
		t.Fatalf("incompatible directory reached the worker: %v", err)
	}
	run, _ = reloadPiRun(t, service, created.ID)
	if run.Status != "failed" || len(openOrdersFor(t, db, created.ID)) != 0 {
		t.Fatalf("incompatible run did not settle: status %s", run.Status)
	}
	assertReservationInvariant(t, db, "user")
}

func TestAgentCapabilityDirectorySizes(t *testing.T) {
	measure := func(label string, tools []map[string]any) {
		t.Helper()
		raw, err := json.Marshal(tools)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d tools, %d compact JSON bytes, %d estimated tokens", label, len(tools), len(raw), cloudAgentEstimatedTokens(raw))
	}
	measure("offline", backendToolArtifact().Tools)
	service := &Service{}
	for _, item := range []struct {
		business string
		request  CloudAgentRequest
	}{
		{"canvas", CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, VisionEnabled: true, WebSearchEnabled: true, SubagentEnabled: true}},
		{"canvas", CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}}},
		{"drama", CloudAgentRequest{PermissionMode: "read_only"}},
		{"zero", CloudAgentRequest{PermissionMode: "read_only"}},
	} {
		definition := AgentDefinition{ID: "zero", Version: 1, BusinessType: "canvas", BusinessTools: []string{}}
		if item.business != "zero" {
			var err error
			definition, err = service.ResolveAgentDefinition(item.business, "")
			if err != nil {
				t.Fatal(err)
			}
		}
		_, tools, err := compileAgentRunCapabilityDirectory(definition, item.request, false)
		if err != nil {
			t.Fatal(err)
		}
		label := item.business + "/" + item.request.PermissionMode
		measure(label, tools)
		if _, err := cloudAgentToolDirectoryCost(tools, label); err != nil {
			t.Fatal(err)
		}
	}
}
