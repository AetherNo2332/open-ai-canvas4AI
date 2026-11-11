package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"yingce/backend/internal/model"
)

// ReprocessDramaSegmentVideo 重新处理一段已生成、但后处理失败的视频：只重放原任务结果，不重新生成、不重复扣费。
func (s *Service) ReprocessDramaSegmentVideo(userID, projectID, episodeID string, index int) (map[string]any, error) {
	project, p, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	episode, err := s.repo.DramaEpisode(project.ID, episodeID)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.DramaSegments(episode.ID)
	if err != nil {
		return nil, err
	}
	var segment *model.DramaSegment
	for i := range rows {
		if rows[i].SegmentIndex == index {
			segment = &rows[i]
			break
		}
	}
	if segment == nil {
		return nil, BadAuthRequest("剧情段不存在")
	}
	if segment.VideoStatus != model.DramaMediaFailed || segment.VideoResourceID == "" {
		return nil, BadAuthRequest("本段没有需要重新处理的视频")
	}
	jobs, err := s.repo.RecentDramaSegmentJobs(project.ID, episode.ID)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.Kind != model.DramaJobSegmentVideo || job.ShotID != segment.ID {
			continue
		}
		// 只看最近一次视频作业：更早的结果已被后续生成取代，不能拿来重放。
		if !dramaVideoJobReplayable(job) {
			break
		}
		reopened, err := s.repo.ReopenDramaSegmentVideoJob(job.ID, segment.ID)
		if err != nil {
			return nil, err
		}
		if !reopened {
			break
		}
		return map[string]any{"reprocessing": true}, s.requestDramaWork(p)
	}
	return nil, BadAuthRequest("找不到可重新处理的视频结果，请重新生成本段视频")
}

// dramaTailDownloadTimeout 是抽尾帧前读取段视频的上限；超时转为可重新处理的失败，而不是一直“生成中”。
const dramaTailDownloadTimeout = 3 * time.Minute

func (s *Service) extractDramaTailFrame(userID, resourceID string) (string, error) {
	if s.dramaTailFrameHook != nil {
		return s.dramaTailFrameHook(userID, resourceID)
	}
	var tail string
	err := s.withDramaVideoFile(userID, resourceID, func(resource *model.Resource, input string) error {
		var saveErr error
		tail, saveErr = s.saveDramaTailFrame(userID, resource, input)
		return saveErr
	})
	return tail, err
}

// dramaVideoFacts 是对一段视频的本地探测结果：尾帧资源、实际时长与是否含音轨（探测不到时为 nil）。
type dramaVideoFacts struct {
	TailResourceID string
	DurationMs     int64
	HasAudio       *bool
}

// inspectDramaVideo 下载一次视频，抽取尾帧并探测时长与音轨；用于把画布上的视频采用为剧情段。
func (s *Service) inspectDramaVideo(userID string, resource *model.Resource) (dramaVideoFacts, error) {
	facts := dramaVideoFacts{DurationMs: resource.DurationMs}
	if s.dramaTailFrameHook != nil {
		tail, err := s.dramaTailFrameHook(userID, resource.ID)
		facts.TailResourceID = tail
		return facts, err
	}
	err := s.withDramaVideoFile(userID, resource.ID, func(opened *model.Resource, input string) error {
		tail, err := s.saveDramaTailFrame(userID, opened, input)
		if err != nil {
			return err
		}
		facts.TailResourceID = tail
		if probed := probeMediaDurationMs(input); probed > 0 {
			facts.DurationMs = probed
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if hasAudio, probeErr := probeHasAudioStream(ctx, input); probeErr == nil {
			facts.HasAudio = &hasAudio
		}
		return nil
	})
	return facts, err
}

// withDramaVideoFile 把视频资源下载到临时文件后交给 fn；读取对象存储有超时保护。
func (s *Service) withDramaVideoFile(userID, resourceID string, fn func(resource *model.Resource, input string) error) error {
	resource, body, err := s.OpenResource(userID, resourceID)
	if err != nil {
		return err
	}
	defer body.Close()
	// 读取对象存储可能卡住：超时后关闭读取流让 io.Copy 返回，避免一段视频阻塞整个调度循环。
	var stalled atomic.Bool
	watchdog := time.AfterFunc(dramaTailDownloadTimeout, func() {
		stalled.Store(true)
		_ = body.Close()
	})
	defer watchdog.Stop()
	if !strings.HasPrefix(resource.MimeType, "video/") {
		return errors.New("尾帧来源不是视频")
	}
	dir, err := os.MkdirTemp("", "yingce-drama-tail-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "input.mp4")
	file, err := os.Create(input)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, body)
	watchdog.Stop()
	closeErr := file.Close()
	if copyErr != nil {
		if stalled.Load() {
			return fmt.Errorf("读取段视频超时（%d 秒），可稍后点“重新处理”", int(dramaTailDownloadTimeout.Seconds()))
		}
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return fn(resource, input)
}

// saveDramaTailFrame 用 ffmpeg 取本地视频的最后一帧并保存为图片资源。
func (s *Service) saveDramaTailFrame(userID string, resource *model.Resource, input string) (string, error) {
	ffmpegBin, err := renderFfmpegBinary()
	if err != nil {
		return "", fmt.Errorf("抽取尾帧失败：%w", err)
	}
	dir, err := os.MkdirTemp("", "yingce-drama-tail-out-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "tail.png")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Decode every remaining frame and update the same output, so the saved image is the actual final frame.
	cmd := exec.CommandContext(ctx, ffmpegBin, "-hide_banner", "-loglevel", "error", "-sseof", "-1", "-i", input, "-an", "-update", "1", "-y", output)
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("抽取尾帧失败：%s", clipText(detail, 400))
	}
	image, err := os.Open(output)
	if err != nil {
		return "", err
	}
	defer image.Close()
	info, err := image.Stat()
	if err != nil {
		return "", err
	}
	saved, err := s.UploadResourceFile(userID, "tail.png", info.Size(), "image", resource.Width, resource.Height, 0, image, "drama-tail:"+resource.ID)
	if err != nil {
		return "", err
	}
	return saved.ID, nil
}
