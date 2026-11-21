package jianying

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestReadmeKeepsDraftInstructionsAndSkippedSegments(t *testing.T) {
	draft := Draft{
		Name: "斗破-第02集",
		Root: `C:\Users\editor\drafts`,
		Captions: []Caption{
			{Text: "台词", StartUs: 0, DurationUs: 1000},
		},
	}
	got := Readme(draft, []string{"第 3 段：视频尚未生成"})
	for _, want := range []string{
		"影策短剧 · 剪映草稿",
		"确保文件夹「斗破-第02集」直接位于：\n   C:\\Users\\editor\\drafts",
		"解压后的完整路径应为：C:\\Users\\editor\\drafts\\斗破-第02集",
		"台词字幕按分镜时长估算",
		"\n以下剧情段没有导出：\n- 第 3 段：视频尚未生成\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("readme missing %q:\n%s", want, got)
		}
	}
}

func TestReadmeOmitsOptionalSections(t *testing.T) {
	got := Readme(Draft{Name: "无字幕", Root: "/tmp/drafts"}, nil)
	if strings.Contains(got, "台词字幕按分镜时长估算") || strings.Contains(got, "以下剧情段没有导出") {
		t.Fatalf("optional sections must stay absent:\n%s", got)
	}
	if !strings.Contains(got, "解压后的完整路径应为：/tmp/drafts/无字幕") {
		t.Fatalf("portable draft path missing:\n%s", got)
	}
}

func TestNormalizeRootRejectsInvalidInput(t *testing.T) {
	for _, test := range []struct {
		root string
		want string
	}{
		{"", "请填写剪映草稿目录"},
		{"relative/path", "剪映草稿目录必须是绝对路径"},
		{"/", "剪映草稿目录不能是磁盘根目录"},
		{"C:\\", "剪映草稿目录不能是磁盘根目录"},
	} {
		if _, err := NormalizeRoot(test.root); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("NormalizeRoot(%q) error = %v, want %q", test.root, err, test.want)
		}
	}
	got, err := NormalizeRoot(` C:\Users\editor\drafts\ `)
	if err != nil || got != `C:\Users\editor\drafts` {
		t.Fatalf("NormalizeRoot valid path = %q, %v", got, err)
	}
}

func TestCoverJPEGConvertsAndRejectsInvalidInput(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	cover, err := CoverJPEG(&source, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(cover) < 2 || cover[0] != 0xff || cover[1] != 0xd8 {
		t.Fatalf("cover is not JPEG: %x", cover)
	}
	if _, err := CoverJPEG(strings.NewReader("not an image"), 64); err == nil {
		t.Fatal("invalid image must fail")
	}
}
