package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

// silentMP3Frames 造 n 帧 MPEG-1 Layer III（44.1kHz 128kbps 单声道）静音字节：
// 真实帧头 + 全零谱数据，解码为静音，用于验证 mp3 内联结果能被保存。
func silentMP3Frames(n int) []byte {
	size := 417
	out := make([]byte, n*size)
	for i := 0; i < n; i++ {
		offset := i * size
		out[offset], out[offset+1], out[offset+2], out[offset+3] = 0xff, 0xfb, 0x90, 0xc0
	}
	return out
}

func TestMediaRecoveryStagesInlineAudioDataURL(t *testing.T) {
	s, db := newMediaRecoveryTestService(t)
	expires := time.Now().Add(time.Hour)
	input, err := json.Marshal(canvasGenerationInput{Mode: "audio", Prompt: "voice"})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: newID(), UserID: "media-user", Type: "canvas_audio", Status: model.TaskStatusRunning, InputJSON: string(input), LeaseOwner: "worker-a", LeaseExpiresAt: &expires, RouteRun: 1, CreatedAt: time.Now()}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	mp3 := silentMP3Frames(3)
	ctx := context.WithValue(withProviderAnalytics(context.Background(), s, *task), mediaExecutionTaskKey{}, *task)
	result, handled, failure := recoverProtocolMedia(ctx, providerConfig{}, "audio", []protocol.MediaReference{
		{DataURL: dataURL("audio/mpeg", mp3), MIMEType: "audio/mpeg"},
	})
	if !handled || failure != nil {
		t.Fatalf("handled=%v err=%v cause=%v", handled, failure, errors.Unwrap(failure))
	}
	audios, ok := result["audios"].([]interface{})
	if !ok || len(audios) != 1 {
		t.Fatalf("audios=%#v", result["audios"])
	}
	item, _ := audios[0].(map[string]interface{})
	if item["mimeType"] != "audio/mpeg" {
		t.Fatalf("stored audio mime = %#v", item["mimeType"])
	}
	if bytes, ok := item["bytes"].(int64); !ok || bytes != int64(len(mp3)) {
		t.Fatalf("stored audio bytes = %#v", item["bytes"])
	}
}
