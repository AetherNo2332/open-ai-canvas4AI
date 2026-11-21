package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

func seedDramaJianyingProject(t *testing.T, db *gorm.DB) (string, string) {
	t.Helper()
	const userID = "user-jianying"
	const projectID = "project-jianying"
	const episodeID = "episode-jianying"
	if err := db.Create(&model.Project{ID: projectID, UserID: userID, Name: "斗破", Type: "drama", AspectRatio: "9:16"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.DramaProduction{ProjectID: projectID, UserID: userID, SettingsJSON: `{"aspectRatio":"9:16"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.DramaEpisode{ID: episodeID, ProjectID: projectID, Number: 2, Title: "退婚"}).Error; err != nil {
		t.Fatal(err)
	}
	return projectID, episodeID
}

func seedDramaJianyingVideo(t *testing.T, db *gorm.DB, dataDir string, episodeID string) {
	t.Helper()
	const userID = "user-jianying"
	videoData := []byte("fake-mp4")
	objectKey := "drama/video.mp4"
	path := filepath.Join(dataDir, "resources", filepath.FromSlash(objectKey))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, videoData, 0o644); err != nil {
		t.Fatal(err)
	}
	resource := &model.Resource{
		ID: "resource-video", UserID: userID, Kind: "video", Status: model.ResourceStatusReady,
		Provider: "local", ObjectKey: objectKey, MimeType: "video/mp4",
		Size: int64(len(videoData)), Width: 1080, Height: 1920, DurationMs: 8000,
	}
	if err := db.Create(resource).Error; err != nil {
		t.Fatal(err)
	}
	shots := []model.DramaShot{
		{ID: "shot-1", ProjectID: "project-jianying", EpisodeID: episodeID, Position: 0, SegmentIndex: 0, SegmentTitle: "初遇", Dialogue: "第一句", DurationMs: 2000},
		{ID: "shot-2", ProjectID: "project-jianying", EpisodeID: episodeID, Position: 1, SegmentIndex: 0, SegmentTitle: "初遇", Dialogue: "第二句", DurationMs: 4000},
		{ID: "shot-3", ProjectID: "project-jianying", EpisodeID: episodeID, Position: 2, SegmentIndex: 2, SegmentTitle: "未拍", Dialogue: "第三句", DurationMs: 3000},
	}
	if err := db.Create(&shots).Error; err != nil {
		t.Fatal(err)
	}
	segments := []model.DramaSegment{
		{ID: "segment-1", ProjectID: "project-jianying", EpisodeID: episodeID, SegmentIndex: 0, VideoStatus: model.DramaMediaReady, VideoResourceID: resource.ID},
		{ID: "segment-2", ProjectID: "project-jianying", EpisodeID: episodeID, SegmentIndex: 1, VideoStatus: model.DramaMediaReady, VideoResourceID: resource.ID},
		{ID: "segment-3", ProjectID: "project-jianying", EpisodeID: episodeID, SegmentIndex: 2},
	}
	if err := db.Create(&segments).Error; err != nil {
		t.Fatal(err)
	}
}

func dramaJianyingZipEntry(t *testing.T, data []byte, suffix string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if !strings.HasSuffix(file.Name, suffix) {
			continue
		}
		body, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(body)
		closeErr := body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		return content
	}
	t.Fatalf("zip has no entry ending in %q", suffix)
	return nil
}

func TestDramaJianyingExportUsesDomainRulesAndSkipsMissingVideo(t *testing.T) {
	svc, db := newTimelineTaskTestService(t)
	svc.dataDir = t.TempDir()
	projectID, episodeID := seedDramaJianyingProject(t, db)
	seedDramaJianyingVideo(t, db, svc.dataDir, episodeID)

	archive, err := svc.PrepareDramaJianyingDraft("user-jianying", projectID, episodeID, DramaJianyingDraftRequest{
		DraftRoot: t.TempDir(),
		Captions:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(archive.FileName, ".zip") {
		t.Fatalf("archive file name = %q", archive.FileName)
	}
	var bundled bytes.Buffer
	if err := svc.WriteDramaArchive(context.Background(), "user-jianying", archive, &bundled); err != nil {
		t.Fatal(err)
	}
	if video := dramaJianyingZipEntry(t, bundled.Bytes(), "/materials/01-初遇.mp4"); string(video) != "fake-mp4" {
		t.Fatalf("archived video = %q", video)
	}
	readme := string(dramaJianyingZipEntry(t, bundled.Bytes(), "/导入说明.txt"))
	if !strings.Contains(readme, "第 3 段：视频尚未生成") || strings.Contains(readme, "第 2 段：") {
		t.Fatalf("readme skip list changed:\n%s", readme)
	}
	srt := string(dramaJianyingZipEntry(t, bundled.Bytes(), "/台词字幕.srt"))
	for _, want := range []string{
		"00:00:00,000 --> 00:00:02,666",
		"00:00:02,666 --> 00:00:08,000",
	} {
		if !strings.Contains(srt, want) {
			t.Fatalf("caption boundary %q missing:\n%s", want, srt)
		}
	}
	content := dramaJianyingZipEntry(t, bundled.Bytes(), "/draft_content.json")
	var draft struct {
		Canvas struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"canvas_config"`
		Tracks []map[string]any `json:"tracks"`
	}
	if err := json.Unmarshal(content, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Canvas.Width != 1080 || draft.Canvas.Height != 1920 || len(draft.Tracks) != 2 {
		t.Fatalf("draft format changed: %+v", draft)
	}
}

func TestDramaJianyingExportRejectsInvalidInputs(t *testing.T) {
	svc, db := newTimelineTaskTestService(t)
	svc.dataDir = t.TempDir()
	projectID, episodeID := seedDramaJianyingProject(t, db)

	for _, test := range []struct {
		root string
		want string
	}{
		{"", "请填写剪映草稿目录"},
		{"relative/path", "剪映草稿目录必须是绝对路径"},
	} {
		_, err := svc.PrepareDramaJianyingDraft("user-jianying", projectID, episodeID, DramaJianyingDraftRequest{DraftRoot: test.root})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("root %q error = %v, want %q", test.root, err, test.want)
		}
	}

	resource := &model.Resource{
		ID: "resource-no-shots", UserID: "user-jianying", Kind: "video", Status: model.ResourceStatusReady,
		Provider: "local", MimeType: "video/mp4", Size: 1, DurationMs: 1000,
	}
	if err := db.Create(resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.DramaSegment{
		ID: "segment-no-shots", ProjectID: projectID, EpisodeID: episodeID, SegmentIndex: 0,
		VideoStatus: model.DramaMediaReady, VideoResourceID: resource.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := svc.PrepareDramaJianyingDraft("user-jianying", projectID, episodeID, DramaJianyingDraftRequest{DraftRoot: t.TempDir()})
	if err == nil || err.Error() != "本集还没有可导出的剧情段视频，请先生成段视频" {
		t.Fatalf("segment without shots error = %v", err)
	}
}
