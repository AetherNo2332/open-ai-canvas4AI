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
