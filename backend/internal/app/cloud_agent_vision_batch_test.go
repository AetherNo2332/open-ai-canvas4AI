package app

import "testing"

func TestCloudAgentVisionBatchPlanUsesImageAndCostLimits(t *testing.T) {
	inspections := []cloudAgentImageInspection{
		{Receipt: map[string]any{"nodeId": "a", "bytes": int64(1), "width": 1000, "height": 1000}},
		{Receipt: map[string]any{"nodeId": "b", "bytes": int64(1), "width": 1000, "height": 1000}},
		{Receipt: map[string]any{"nodeId": "c", "bytes": int64(1), "width": 1000, "height": 1000}},
	}
	batches := planCloudAgentVisionBatches(inspections, cloudAgentVisionBatchBudget{MaxImages: 2, MaxCost: 4})
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 1 {
		t.Fatalf("unexpected batches: %#v", batches)
	}
}

func TestCloudAgentVisionBatchPlanDeduplicatesNodeAndSHA(t *testing.T) {
	inspections := []cloudAgentImageInspection{
		{ResourceSHA: "same", Receipt: map[string]any{"nodeId": "a"}},
		{ResourceSHA: "same", Receipt: map[string]any{"nodeId": "a"}},
		{ResourceSHA: "changed", Receipt: map[string]any{"nodeId": "a"}},
	}
	batches := planCloudAgentVisionBatches(inspections, cloudAgentVisionBatchBudget{MaxImages: 4, MaxCost: 100})
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("expected same node/SHA deduplication: %#v", batches)
	}
}
