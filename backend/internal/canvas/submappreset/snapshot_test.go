package submappreset

import (
	"fmt"
	"strings"
	"testing"
)

func validSnapshot() Snapshot {
	return Snapshot{
		Version: SupportedVersion,
		ID:      "a1b2c3d4e5f60718293a4b5c6d7e8f90",
		Title:   "我的镜头组",
		Nodes: []Node{
			{LocalID: "text-1", Type: "text", Title: "提示词",
				Metadata: map[string]any{"generationSpec": map[string]any{"prompt": "正文也在"}}},
			{LocalID: "image-1", Type: "image", Title: "首帧",
				Metadata: map[string]any{
					"generationSpec":       map[string]any{"prompt": "@文本1"},
					"referenceTextNodeIds": []any{"text-1"},
				}},
			{LocalID: "video-1", Type: "video", Title: "视频",
				Metadata: map[string]any{
					"videoStartFrameNodeId": "image-1",
					"referenceTextNodeIds":  []any{"text-1"},
				}},
		},
	}
}

func TestValidateAcceptsGenericCanvasNodes(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Nodes = append(snapshot.Nodes,
		Node{LocalID: "audio-1", Type: "audio", Title: "旁白"},
		Node{LocalID: "image-2", Type: "image", Title: "第二张图",
			Metadata: map[string]any{"referenceNodeIds": []any{"image-1"}}},
	)
	snapshot.Nodes[0].Metadata["generationSpec"] = map[string]any{"prompt": "这段正文不是 @文本N 槽位"}
	if err := Validate(snapshot); err != nil {
		t.Fatalf("通用规则应接受 audio / text 正文 / 多图，实际拒绝: %v", err)
	}
}

func TestValidateRejectsUnsupportedType(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Nodes[0].Type = "nonexistent-type"
	if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), "不是当前画布支持") {
		t.Fatalf("应拒绝未注册类型，得到: %v", err)
	}
}

func TestValidateRejectsDanglingReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Snapshot)
		want string
	}{
		{"数组引用", func(s *Snapshot) {
			s.Nodes[1].Metadata["referenceTextNodeIds"] = []any{"ghost"}
		}, "referenceTextNodeIds"},
		{"标量引用", func(s *Snapshot) {
			s.Nodes[2].Metadata["videoStartFrameNodeId"] = "ghost"
		}, "videoStartFrameNodeId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := validSnapshot()
			tc.edit(&snapshot)
			if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("应拒绝悬空引用，得到: %v", err)
			}
		})
	}
}

func TestValidateRejectsMalformedReferenceValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"数组元素不是字符串", "referenceTextNodeIds", []any{"text-1", 42}},
		{"标量不是字符串", "videoStartFrameNodeId", 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := validSnapshot()
			snapshot.Nodes[1].Metadata[tc.field] = tc.value
			if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("应拒绝畸形引用字段，得到: %v", err)
			}
		})
	}
}

func TestValidateRejectsDanglingAndMalformedBindings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"生成绑定悬空", "generationSpec.referenceBindings", []any{map[string]any{"nodeId": "ghost", "mediaType": "text", "role": "source-text"}}},
		{"输入绑定悬空", "inputBindings", []any{map[string]any{"nodeId": "ghost", "mediaType": "text", "role": "source-text"}}},
		{"生成绑定不是数组", "generationSpec.referenceBindings", "not-an-array"},
		{"输入绑定元素不是对象", "inputBindings", []any{"not-an-object"}},
		{"生成绑定 nodeId 不是字符串", "generationSpec.referenceBindings", []any{map[string]any{"nodeId": 42}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := validSnapshot()
			switch tc.field {
			case "generationSpec.referenceBindings":
				snapshot.Nodes[2].Metadata["generationSpec"] = map[string]any{"referenceBindings": tc.value}
			default:
				snapshot.Nodes[2].Metadata["inputBindings"] = tc.value
			}
			if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("应拒绝畸形或悬空绑定，得到: %v", err)
			}
		})
	}
}

func TestValidateRejectsDuplicateID(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Nodes[1].LocalID = "text-1"
	if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("应拒绝重复 localId，得到: %v", err)
	}
}

func TestValidateRejectsWrongVersion(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Version = 2
	if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("应拒绝错误版本，得到: %v", err)
	}
}

func TestValidateRejectsEmptyAndTooMany(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Nodes = nil
	if err := Validate(snapshot); err == nil {
		t.Fatal("应拒绝空节点快照")
	}
	snapshot = validSnapshot()
	for i := 0; i <= MaxNodes; i++ {
		snapshot.Nodes = append(snapshot.Nodes, Node{LocalID: "x", Type: "text", Title: "x"})
	}
	if err := Validate(snapshot); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("应拒绝超上限，得到: %v", err)
	}
}

func TestValidateBoundaryNodeCountAndNameLength(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Title = strings.Repeat("名", MaxNameRunes)
	snapshot.Nodes = make([]Node, MaxNodes)
	for index := range snapshot.Nodes {
		snapshot.Nodes[index] = Node{LocalID: fmt.Sprintf("text-%d", index+1), Type: "text", Title: "文本"}
	}
	if err := Validate(snapshot); err != nil {
		t.Fatalf("上限内应通过: %v", err)
	}

	overName := snapshot
	overName.Title = strings.Repeat("名", MaxNameRunes+1)
	if err := Validate(overName); err == nil || !strings.Contains(err.Error(), "名称") {
		t.Fatalf("应拒绝超长名称，得到: %v", err)
	}

	overNodes := snapshot
	overNodes.Nodes = append(append([]Node(nil), snapshot.Nodes...), Node{LocalID: "audio-1", Type: "audio", Title: "旁白"})
	if err := Validate(overNodes); err == nil || !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("应拒绝超量节点，得到: %v", err)
	}
}
