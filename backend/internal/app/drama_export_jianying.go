package app

import (
	"bytes"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"time"
	"yingce/backend/internal/jianying"
	"yingce/backend/internal/model"
)

type DramaJianyingDraftRequest struct {
	DraftRoot string
	Captions  bool
}

// PrepareDramaJianyingDraft 把一集已生成的剧情段视频装配为剪映草稿：主轨按成片顺序首尾相接，
// 可选附带按镜头时长估算的台词字幕轨。未生成视频的段跳过并写进导入说明。
func (s *Service) PrepareDramaJianyingDraft(userID, projectID, episodeID string, req DramaJianyingDraftRequest) (*DramaArchive, error) {
	root, err := jianying.NormalizeRoot(req.DraftRoot)
	if err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	episode, err := s.repo.DramaEpisode(project.ID, strings.TrimSpace(episodeID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("剧集不存在")
	}
	if err != nil {
		return nil, err
	}
	segments, err := s.repo.DramaSegments(episode.ID)
	if err != nil {
		return nil, err
	}
	shots, err := s.repo.DramaShots(episode.ID)
	if err != nil {
		return nil, err
	}
	settings := parseDramaSettings(productionRow.SettingsJSON)
	width, height := dramaRenderSize(settings.AspectRatio)
	now := time.Now()
	name := jianying.SafeName(fmt.Sprintf("%s-第%02d集-%s", project.Name, episode.Number, now.In(dramaExportLocation).Format("0102-1504")), "影策短剧草稿", 80)
	draft := jianying.Draft{Name: name, Root: root, Width: width, Height: height, FPS: 30, Now: now}
	archive := newDramaArchive(name + ".zip")
	shotsBySegment := dramaShotsBySegment(shots)
	var skipped []string
	var coverResourceID string
	var cursor int64
	for _, segment := range segments {
		segmentShots := shotsBySegment[segment.SegmentIndex]
		if len(segmentShots) == 0 {
			continue
		}
		label := fmt.Sprintf("第 %d 段", segment.SegmentIndex+1)
		if segment.VideoResourceID == "" || (segment.VideoStatus != model.DramaMediaReady && segment.VideoStatus != model.DramaMediaStale) {
			skipped = append(skipped, label+"：视频尚未生成")
			continue
		}
		resource := s.dramaExportResource(userID, segment.VideoResourceID, "video")
		if resource == nil {
			skipped = append(skipped, label+"：视频文件不可用")
			continue
		}
		durationMs := resource.DurationMs
		if durationMs <= 0 {
			durationMs = int64(segment.VideoSeconds) * 1000
		}
		if durationMs <= 0 {
			skipped = append(skipped, label+"：视频时长未知")
			continue
		}
		fileName := fmt.Sprintf("%02d-%s%s", segment.SegmentIndex+1, jianying.SafeName(dramaSegmentTitle(segmentShots, segment.SegmentIndex), "剧情段", 40), dramaResourceExtension(resource, ".mp4"))
		if err := archive.addResource(name+"/"+jianying.MaterialsDir+"/"+fileName, resource); err != nil {
			return nil, err
		}
		durationUs := durationMs * 1000
		draft.Clips = append(draft.Clips, jianying.Clip{FileName: fileName, Name: fileName, DurationUs: durationUs, Width: resource.Width, Height: resource.Height})
		if req.Captions {
			draft.Captions = append(draft.Captions, dramaSegmentCaptions(segmentShots, cursor, durationUs)...)
		}
		if coverResourceID == "" {
			coverResourceID = segment.TailFrameResourceID
		}
		cursor += durationUs
	}
	return s.finishDramaJianyingArchive(userID, name, coverResourceID, draft, archive, skipped)
}

// dramaSegmentCaptions 按镜头时长在段内的占比分配台词时间，并按段视频实际时长等比缩放；
// 边界用整数换算，相邻字幕不会重叠。只是估算，精确对齐建议在剪映里用「识别字幕」。
func dramaSegmentCaptions(shots []model.DramaShot, startUs int64, durationUs int64) []jianying.Caption {
	var total int64
	for _, shot := range shots {
		total += max(shot.DurationMs, 0)
	}
	equal := total <= 0
	if equal {
		total = int64(len(shots))
	}
	var captions []jianying.Caption
	var elapsed int64
	for _, shot := range shots {
		weight := max(shot.DurationMs, 0)
		if equal {
			weight = 1
		}
		begin := startUs + durationUs*elapsed/total
		elapsed += weight
		end := startUs + durationUs*elapsed/total
		if text := strings.TrimSpace(shot.Dialogue); text != "" && end > begin {
			captions = append(captions, jianying.Caption{Text: text, StartUs: begin, DurationUs: end - begin})
		}
	}
	return captions
}

// dramaDraftCover 把首段尾帧转成 JPEG 封面；任何一步失败都不影响导出，剪映会自己生成封面。
func (s *Service) dramaDraftCover(userID string, resourceID string) []byte {
	resource := s.dramaExportResource(userID, resourceID, "image")
	if resource == nil || resource.Size > dramaCoverMaxBytes {
		return nil
	}
	_, body, err := s.OpenResource(userID, resource.ID)
	if err != nil {
		return nil
	}
	defer body.Close()
	decoded, _, err := image.Decode(io.LimitReader(body, dramaCoverMaxBytes))
	if err != nil {
		return nil
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, decoded, &jpeg.Options{Quality: 85}); err != nil {
		return nil
	}
	return buffer.Bytes()
}

func dramaJianyingReadme(draft jianying.Draft, skipped []string) string {
	var builder strings.Builder
	builder.WriteString("影策短剧 · 剪映草稿\n\n")
	builder.WriteString("使用方法：\n")
	builder.WriteString("1. 关闭剪映专业版。\n")
	fmt.Fprintf(&builder, "2. 把压缩包解压到剪映草稿目录，确保文件夹「%s」直接位于：\n   %s\n", draft.Name, draft.Root)
	fmt.Fprintf(&builder, "   解压后的完整路径应为：%s\n", draft.FolderPath())
	builder.WriteString("3. 打开剪映，在首页草稿列表里找到同名草稿。\n\n")
	builder.WriteString("注意：素材按上面的完整路径写入草稿；解压到别处或改了文件夹名，剪映会提示素材丢失，可在剪映里重新链接 materials 目录下的视频。\n")
	if len(draft.Captions) > 0 {
		builder.WriteString("台词字幕按分镜时长估算，与配音可能有偏差；需要精确对齐时可删除字幕轨，用剪映「识别字幕」重新生成。台词字幕.srt 可单独导入。\n")
	}
	if len(skipped) > 0 {
		builder.WriteString("\n以下剧情段没有导出：\n")
		for _, item := range skipped {
			builder.WriteString("- " + item + "\n")
		}
	}
	return builder.String()
}

// ===== 资产包 =====

// DramaAssetPackageRequest 选择要打包的剧集；Media 打包设定图、声音样本、段视频与成片，Prompts 打包提示词。

func (s *Service) finishDramaJianyingArchive(userID, name, coverResourceID string, draft jianying.Draft, archive *DramaArchive, skipped []string) (*DramaArchive, error) {
	if len(draft.Clips) == 0 {
		return nil, BadAuthRequest("本集还没有可导出的剧情段视频，请先生成段视频")
	}
	if cover := s.dramaDraftCover(userID, coverResourceID); cover != nil {
		draft.HasCover = true
		archive.addData(name+"/"+jianying.CoverFile, cover)
	}
	content, meta, err := jianying.Build(draft)
	if err != nil {
		return nil, err
	}
	// Windows 版读取 draft_content.json，macOS 版读取 draft_info.json；两份内容相同。
	archive.addData(name+"/draft_content.json", content)
	archive.addData(name+"/draft_info.json", content)
	archive.addData(name+"/draft_meta_info.json", meta)
	if len(draft.Captions) > 0 {
		archive.addData(name+"/台词字幕.srt", []byte(jianying.SRT(draft.Captions)))
	}
	archive.addData(name+"/导入说明.txt", []byte(dramaJianyingReadme(draft, skipped)))
	return archive, nil
}
