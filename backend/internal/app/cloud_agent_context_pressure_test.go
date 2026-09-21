package app

import "testing"

func TestEstimateCloudAgentTokensTreatsCJKConservatively(t *testing.T) {
	if got := estimateCloudAgentTokens([]byte("abcd中文")); got != 3 {
		t.Fatalf("estimate = %d, want 3", got)
	}
}

func TestEstimateCloudAgentTokensRoundsASCIIUp(t *testing.T) {
	if got := estimateCloudAgentTokens([]byte("hello")); got != 2 {
		t.Fatalf("estimate = %d, want 2", got)
	}
}

func TestCloudAgentContextPressureSeparatesNextRequestEstimateFromPreviousProviderMeasurement(t *testing.T) {
	pressure := cloudAgentContextPressure{EstimatedInputTokens: 20000, UsableInputTokens: 100000}
	state := &cloudAgentRuntime{TokenAnchor: &cloudAgentTokenAnchor{
		Step: 2, InputTokens: 10500, EstimatedTokens: 19500, Accepted: true,
	}}
	payload := cloudAgentContextPressurePayload(pressure, state)
	if payload["readingScope"] != "next_request" || payload["estimateMethod"] != "local_v1" {
		t.Fatalf("next request scope missing: %+v", payload)
	}
	if payload["providerMeasurementScope"] != "previous_request" {
		t.Fatalf("provider scope = %v", payload["providerMeasurementScope"])
	}
	if payload["estimatedInputTokens"] != 20000 || payload["projectedTokens"] != 11000 {
		t.Fatalf("estimate/projected readings were mixed: %+v", payload)
	}
}
