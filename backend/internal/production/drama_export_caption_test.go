package production

import (
	"reflect"
	"testing"
)

func TestSegmentCaptionsScalesAndKeepsBoundaries(t *testing.T) {
	got := SegmentCaptions([]SegmentDialogue{
		{Text: "第一句", DurationMs: 1000},
		{Text: "第二句", DurationMs: 3000},
		{Text: "第三句", DurationMs: 2000},
	}, 0, 10_000)
	want := []SegmentCaption{
		{Text: "第一句", StartUs: 0, DurationUs: 1666},
		{Text: "第二句", StartUs: 1666, DurationUs: 5000},
		{Text: "第三句", StartUs: 6666, DurationUs: 3334},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("captions = %+v, want %+v", got, want)
	}
	for index := 1; index < len(got); index++ {
		previous := got[index-1]
		if previous.StartUs+previous.DurationUs != got[index].StartUs {
			t.Fatalf("caption %d overlaps or leaves a gap: %+v", index, got)
		}
	}
	if last := got[len(got)-1]; last.StartUs+last.DurationUs != 10_000 {
		t.Fatalf("last caption must end at the video end: %+v", last)
	}
}

func TestSegmentCaptionsEqualizesUnknownDurationsAndSkipsBlankText(t *testing.T) {
	got := SegmentCaptions([]SegmentDialogue{
		{Text: "  甲  ", DurationMs: 0},
		{Text: "   ", DurationMs: 0},
		{Text: "乙", DurationMs: -4},
	}, 1000, 900)
	want := []SegmentCaption{
		{Text: "甲", StartUs: 1000, DurationUs: 300},
		{Text: "乙", StartUs: 1600, DurationUs: 300},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("captions = %+v, want %+v", got, want)
	}
}

func TestSegmentCaptionsHandlesEmptyInput(t *testing.T) {
	if got := SegmentCaptions(nil, 0, 1000); len(got) != 0 {
		t.Fatalf("empty shots must have no captions: %+v", got)
	}
	if got := SegmentCaptions([]SegmentDialogue{{Text: "台词", DurationMs: 1000}}, 0, 0); len(got) != 0 {
		t.Fatalf("zero-length video must have no captions: %+v", got)
	}
	if got := SegmentCaptions([]SegmentDialogue{{Text: "  ", DurationMs: 1000}}, 0, 1000); len(got) != 0 {
		t.Fatalf("blank dialogue must have no captions: %+v", got)
	}
}
