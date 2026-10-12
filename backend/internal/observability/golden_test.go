package observability

import "testing"

func TestGoldenCaseRequiresVersionedChecksAndNoRawPrompt(t *testing.T) {
	if err := ValidateGoldenCase(GoldenCase{ID: "case-1", DatasetVersion: "v1", EvalVersion: "eval-1", DeterministicChecks: []string{"tool_succeeded"}, InputSummary: "summarized input", ExpectedResult: "expected"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGoldenCase(GoldenCase{ID: "case-1", DatasetVersion: "v1", InputSummary: "full user prompt"}); err == nil {
		t.Fatal("incomplete golden case accepted")
	}
}
