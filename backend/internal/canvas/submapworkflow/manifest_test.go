package submapworkflow

import (
	"errors"
	"testing"
)

func TestLoadStoryboardWorkflow(t *testing.T) {
	manifest, err := Load("storyboard")
	if err != nil {
		t.Fatalf("Load(storyboard) 失败：%v", err)
	}
	if manifest.Version != SupportedVersion || manifest.ID != "storyboard" {
		t.Fatalf("manifest 头不对：version=%d id=%q", manifest.Version, manifest.ID)
	}
	if len(manifest.Children) != 6 {
		t.Fatalf("children = %d，期望 6", len(manifest.Children))
	}
	if len(manifest.Connections) != 4 {
		t.Fatalf("connections = %d，期望 4", len(manifest.Connections))
	}
	// 连接顺序即槽位顺序：video-1 的文本入边必须依次是 text-1 / text-2 / text-3。
	var videoSources []string
	for _, connection := range manifest.Connections {
		if connection.To == "video-1" {
			videoSources = append(videoSources, connection.From)
		}
	}
	want := []string{"text-1", "text-2", "text-3"}
	if len(videoSources) != len(want) {
		t.Fatalf("video-1 入边 = %v，期望 %v", videoSources, want)
	}
	for index := range want {
		if videoSources[index] != want[index] {
			t.Fatalf("video-1 入边 = %v，期望 %v", videoSources, want)
		}
	}
}

func TestLoadUnknownWorkflowListsAvailable(t *testing.T) {
	_, err := Load("nope")
	if err == nil {
		t.Fatal("未知 workflow 应当拒绝")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Reason != "not_found" {
		t.Fatalf("Reason 不对：%v", err)
	}
}

func TestValidateRejectsVersionMismatch(t *testing.T) {
	manifest := validManifest()
	manifest.Version = SupportedVersion + 1
	if err := Validate(manifest); err == nil {
		t.Fatal("version 不符应当整份拒绝")
	}
}

func TestValidateRejectsDuplicateLocalID(t *testing.T) {
	duplicatedLocal := validManifest()
	duplicatedLocal.Children = append(duplicatedLocal.Children, Child{
		LocalID: "text-1", Type: "text", Title: "重复",
	})
	if err := Validate(duplicatedLocal); err == nil {
		t.Fatal("重复 localId 应当拒绝")
	}
}

func TestValidateRejectsUnresolvablePromptMention(t *testing.T) {
	manifest := validManifest()
	manifest.Children[3].Prompt = "@文本9"
	if err := Validate(manifest); err == nil {
		t.Fatal("超出连接顺序的 @文本N 应当拒绝")
	}
}

func TestValidateRejectsBadStartFrame(t *testing.T) {
	manifest := validManifest()
	manifest.Children[4].StartFrame = "text-1"
	if err := Validate(manifest); err == nil {
		t.Fatal("startFrame 指向文本节点应当拒绝")
	}
}

func TestStoryboardNoteNotConnectedByDefault(t *testing.T) {
	// 备注不连线是预制默认形态：业务语义归预制和 skill，不靠代码校验。
	manifest, err := Load("storyboard")
	if err != nil {
		t.Fatalf("Load(storyboard) 失败：%v", err)
	}
	for _, connection := range manifest.Connections {
		if connection.From == "text-4" {
			t.Fatalf("预制 storyboard 的备注节点 text-4 不应作为连线起点，收到 %v", connection)
		}
	}
}

// validManifest 是最小合法模板：三个文本 + 一个图片 + 一个视频。
func validManifest() Manifest {
	return Manifest{
		Version: SupportedVersion,
		ID:      "unit",
		Title:   "单元模板",
		Children: []Child{
			{LocalID: "text-1", Type: "text", Title: "图片提示词"},
			{LocalID: "text-2", Type: "text", Title: "视频提示词"},
			{LocalID: "text-3", Type: "text", Title: "台词"},
			{LocalID: "image-1", Type: "image", Title: "首帧", Prompt: "@文本1"},
			{LocalID: "video-1", Type: "video", Title: "视频", Prompt: "@文本1", Seconds: "4", StartFrame: "image-1"},
		},
		Connections: []Connection{
			{From: "text-1", To: "image-1"},
			{From: "text-1", To: "video-1"},
			{From: "text-2", To: "video-1"},
			{From: "text-3", To: "video-1"},
		},
	}
}
