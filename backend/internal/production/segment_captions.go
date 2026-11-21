package production

import "strings"

// SegmentDialogue 是剧情段内一个镜头的台词及其分镜时长。
type SegmentDialogue struct {
	Text       string
	DurationMs int64
}

// SegmentCaption 是按镜头时长估算出的段内字幕窗口，时间单位为微秒。
type SegmentCaption struct {
	Text       string
	StartUs    int64
	DurationUs int64
}

// SegmentCaptions 按镜头时长在段内的占比分配台词时间，并按段视频实际时长等比缩放；
// 边界用整数换算，相邻字幕不会重叠。只是估算，精确对齐应在剪辑软件里重新识别字幕。
func SegmentCaptions(dialogues []SegmentDialogue, startUs int64, durationUs int64) []SegmentCaption {
	var total int64
	for _, dialogue := range dialogues {
		total += max(dialogue.DurationMs, 0)
	}
	equal := total <= 0
	if equal {
		total = int64(len(dialogues))
	}
	var captions []SegmentCaption
	var elapsed int64
	for _, dialogue := range dialogues {
		weight := max(dialogue.DurationMs, 0)
		if equal {
			weight = 1
		}
		begin := startUs + durationUs*elapsed/total
		elapsed += weight
		end := startUs + durationUs*elapsed/total
		if text := strings.TrimSpace(dialogue.Text); text != "" && end > begin {
			captions = append(captions, SegmentCaption{Text: text, StartUs: begin, DurationUs: end - begin})
		}
	}
	return captions
}
