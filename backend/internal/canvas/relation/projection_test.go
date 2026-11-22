package relation

import (
	"strings"
	"testing"
)

func pairs(edges []Edge) []string {
	out := make([]string, 0, len(edges))
	for _, edge := range edges {
		out = append(out, string(edge.Family)+":"+edge.FromNodeID+"->"+edge.ToNodeID+":"+edge.Field)
	}
	return out
}

func TestProjectPreservesFieldOrder(t *testing.T) {
	nodes := []map[string]any{
		{"id": "image-1", "type": "image", "metadata": map[string]any{}},
		{"id": "image-2", "type": "image", "metadata": map[string]any{}},
		{"id": "video-1", "type": "video", "metadata": map[string]any{
			"referenceNodeIds":      []any{"image-2", "image-1", "image-2"},
			"videoStartFrameNodeId": "image-1",
		}},
	}
	if got, want := strings.Join(pairs(Project(nodes)), ","), "R1:image-2->video-1:referenceNodeIds,R1:image-1->video-1:referenceNodeIds"; got != want {
		t.Fatalf("投影 = %q，want %q（帧与参考集重叠时只保留 R1）", got, want)
	}
}

func TestProjectConsumedDropsNonLandingTargets(t *testing.T) {
	nodes := []map[string]any{
		{"id": "image-1", "type": "image", "metadata": map[string]any{}},
		{"id": "text-1", "type": "text", "metadata": map[string]any{"referenceNodeIds": []any{"image-1"}}},
		{"id": "video-1", "type": "video", "metadata": map[string]any{"referenceNodeIds": []any{"image-1", "missing"}}},
	}
	edges := ProjectConsumed(nodes)
	if got, want := strings.Join(pairs(edges), ","), "R1:image-1->text-1:referenceNodeIds,R1:image-1->video-1:referenceNodeIds"; got != want {
		t.Fatalf("消费面投影 = %q，want %q（文本目标保留、悬空来源丢弃）", got, want)
	}
}

func TestEdgeIDsAreStable(t *testing.T) {
	cases := map[string]Edge{
		"conn:r1:video-1:image-1":                          {Family: FamilyMediaReference, FromNodeID: "image-1", ToNodeID: "video-1"},
		"conn:frame:videoStartFrameNodeId:video-1:image-1": {Family: FamilyVideoFrame, Field: "videoStartFrameNodeId", FromNodeID: "image-1", ToNodeID: "video-1"},
	}
	for want, edge := range cases {
		if got := edge.ID(); got != want {
			t.Fatalf("边身份 = %q，want %q", got, want)
		}
	}
}

func TestProjectKeepsReferenceFamiliesExclusive(t *testing.T) {
	nodes := []map[string]any{
		{"id": "text-1", "type": "text", "metadata": map[string]any{}},
		{"id": "image-1", "type": "image", "metadata": map[string]any{}},
		{"id": "video-1", "type": "video", "metadata": map[string]any{
			"referenceNodeIds":     []any{"image-1", "text-1"},
			"referenceTextNodeIds": []any{"text-1"},
		}},
	}
	edges := Project(nodes)
	if got, want := strings.Join(pairs(edges), ","), "R1:image-1->video-1:referenceNodeIds,R1:text-1->video-1:referenceNodeIds"; got != want {
		t.Fatalf("投影 = %q，want %q（同一 ID 只留 R1 一条）", got, want)
	}
	ids := make([]string, 0, len(edges))
	for _, edge := range edges {
		ids = append(ids, edge.ID())
	}
	if got, want := strings.Join(ids, ","), "conn:r1:video-1:image-1,conn:r1:video-1:text-1"; got != want {
		t.Fatalf("边身份 = %q，want %q（不得重复）", got, want)
	}
}
