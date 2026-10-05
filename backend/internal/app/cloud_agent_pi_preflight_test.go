package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
	"testing"
)

func TestPiPreflightBudgetIdentityAndDigest(t *testing.T) {
	for _, window := range []int{4096, 8192, 64000, 128000, 1000000} {
		b := cloudAgentContextBudgetFor(window, 512, "test")
		if b.InputBudgetTokens+b.ReservedOutputTokens+b.OverheadTokens != window || b.CompactionReserveTokens != window-b.CompactAtTokens {
			t.Fatalf("budget does not partition window: %+v", b)
		}
		if b.Digest == "" || b.Version == "" || b.sealed().Digest != b.Digest || b.KeepRecentTokens > b.InputBudgetTokens || b.SummaryOutputTokens > 512 {
			t.Fatalf("invalid server plan: %+v", b)
		}
	}
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 64000, 8192)
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	_, state := reloadPiRun(t, s, run.ID)
	pressure := s.cloudAgentContextPressure(state.Canonical, state.Request.Prompt, state.Request)
	if snapshot.ModelLimits.Digest != pressure.BudgetPlan.Digest {
		t.Fatal("snapshot and pressure use different plans")
	}
}

func TestPiPreflightInclusiveAdmissionWithoutBilling(t *testing.T) {
	for _, delta := range []int{-1, 0, 1} {
		t.Run(string(rune('b'+delta)), func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			declareTestChannelWindow(t, db, 64000, 8192)
			_, state := reloadPiRun(t, s, run.ID)
			budget := s.cloudAgentContextBudgetForRequest(state.Request)
			pressure := s.cloudAgentContextPressure(state.Canonical, state.Request.Prompt, state.Request)
			state.TokenAnchor = &cloudAgentTokenAnchor{TaskID: "pi-root-task", Accepted: true, InputTokens: int64(budget.CompactAtTokens + delta), EstimatedTokens: pressure.EstimatedInputTokens, ContextWindowTokens: 64000}
			if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				return cloudAgentSave(current, state)
			}); err != nil {
				t.Fatal(err)
			}
			decision, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical})
			if err != nil {
				t.Fatal(err)
			}
			want := "model"
			if delta >= 0 {
				want = "compact"
			}
			if decision.Decision != want {
				t.Fatalf("decision=%s want=%s", decision.Decision, want)
			}
			if delta >= 0 {
				step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical})
				if err != nil || step.Decision != "compact" || step.TaskID != "" {
					t.Fatalf("ordinary admission bypassed gate: %+v %v", step, err)
				}
			}
			var tasks, orders int64
			db.Model(&model.Task{}).Where("operation = ?", cloudAgentStepOperation).Count(&tasks)
			db.Model(&model.BillingOrder{}).Count(&orders)
			if tasks != 0 || orders != 0 {
				t.Fatalf("preflight created tasks=%d orders=%d", tasks, orders)
			}
		})
	}
}

func TestPiPreflightRejectsSchemaAndExpiresStaleAnchor(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 64000, 8192)
	_, state := reloadPiRun(t, s, run.ID)
	invalid := state.Canonical
	invalid.SystemPrompt = "discarded server policy"
	if _, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: invalid}); err == nil {
		t.Fatal("preflight bypassed policy")
	}
	state.TokenAnchor = &cloudAgentTokenAnchor{TaskID: "pi-root-task", Accepted: true, InputTokens: 100000, EstimatedTokens: 1, ContextWindowTokens: 128000}
	saveCloudAgentCompactionState(t, s, run, state)
	view, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical})
	if err != nil || view.Decision != "model" {
		t.Fatalf("stale anchor changed admission: %+v %v", view, err)
	}
}

func TestPiNativePreparationRejectsInventedMaterials(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	revision, leaf := seedPiCompactionBranch(t, db, run)
	raw := json.RawMessage(`{"firstKeptEntryId":"compact-assistant-1","tokensBefore":24000,"isSplitTurn":false,"messagesToSummarize":[{"role":"user","content":"invented"}],"turnPrefixMessages":[],"fileOps":{"read":[],"written":[],"edited":[]}}`)
	if _, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{SessionRevision: revision, ActiveLeafID: leaf, Reason: "manual", TokensBefore: 24000, Preparation: raw}); err == nil || !strings.Contains(err.Error(), "持久会话") {
		t.Fatalf("invalid materials accepted: %v", err)
	}
}
