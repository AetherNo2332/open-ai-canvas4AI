package app

import (
	"encoding/json"
	"testing"
)

func TestVisionProjectionBoundsEntireRequestAndPreservesSource(t *testing.T) {
	var inspections []cloudAgentImageInspection
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		inspections = append(inspections, cloudAgentImageInspection{Receipt: map[string]any{"nodeId": id, "sha256": id}, ResourceSHA: id, ImageURL: "resource:" + id})
	}
	canonical := canonicalAgentRequest{Messages: []map[string]any{{"role": "user", "content": cloudAgentImageContentParts(inspections...)}}}
	before, _ := json.Marshal(canonical)
	projected, batch := projectCloudAgentVisionRequest(canonical, cloudAgentVisionBatchBudget{MaxImages: 2, MaxCost: 16})
	if len(batch) != 2 || cloudAgentMessageImagePartCount(projected.Messages[0]) != 2 {
		t.Fatalf("whole request not bounded: %+v", projected)
	}
	after, _ := json.Marshal(canonical)
	if string(before) != string(after) {
		t.Fatal("durable source changed")
	}
	projected.Messages = append(projected.Messages, map[string]any{"role": "tool", "content": `{"nodeId":"a","sha256":"a","imageAttached":false,"visionCache":{"short":"cat"}}`})
	next, batch := projectCloudAgentVisionRequest(canonical, cloudAgentVisionBatchBudget{MaxImages: 2, MaxCost: 16}, map[string]string{"a": "a", "b": "b"})
	if len(batch) != 2 || cloudAgentMessageImagePartCount(next.Messages[0]) != 2 {
		t.Fatalf("summarized image was resent: %+v", batch)
	}
}

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
