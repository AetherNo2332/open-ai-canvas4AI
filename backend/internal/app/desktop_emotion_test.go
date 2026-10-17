package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestDesktopEmotionResultStoredOnceWithoutRenderer(t *testing.T) {
	s, db := newMediaRecoveryTestService(t)
	db.Create(&model.User{ID: "emotion-owner", Status: model.UserStatusActive})
	makePNG := func(c color.NRGBA) []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				img.SetNRGBA(x, y, c)
			}
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	sourceBytes, generatedBytes := makePNG(color.NRGBA{200, 100, 30, 255}), makePNG(color.NRGBA{30, 100, 200, 255})
	source, err := s.UploadResourceFile("emotion-owner", "source.png", int64(len(sourceBytes)), "image", 64, 64, 0, bytes.NewReader(sourceBytes))
	if err != nil {
		t.Fatal(err)
	}
	generated, err := s.UploadResourceFile("emotion-owner", "generated.png", int64(len(generatedBytes)), "image", 64, 64, 0, bytes.NewReader(generatedBytes))
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(map[string]any{"images": []any{map[string]any{"storageKey": "resource:" + generated.ID, "dataUrl": resourceFileURL(generated.ID)}}})
	task := model.Task{ID: "emotion-task", UserID: "emotion-owner", Status: model.TaskStatusSucceeded, ResultJSON: string(result)}
	contextJSON, _ := json.Marshal(tasks.EmotionContext{SourceResourceID: source.ID, EditRegion: tasks.EmotionRegion{X: 16, Y: 16, Width: 32, Height: 32}, FaceBox: tasks.EmotionRegion{X: 24, Y: 24, Width: 16, Height: 16}})
	binding := model.CanvasTaskBinding{TaskID: task.ID, OwnerID: task.UserID, Mode: "image", ResultContextJSON: string(contextJSON)}
	first, err := s.prepareDesktopCanvasOutput(context.Background(), task, binding)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.prepareDesktopCanvasOutput(context.Background(), task, binding)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResultJSON != second.ResultJSON {
		t.Fatal("transformed resource duplicated")
	}
	id, _ := taskOutputResource(first.ResultJSON, "canvas_image")
	if id == generated.ID || id == "" {
		t.Fatal("whole generated image used")
	}
	_, reader, err := s.OpenResource(task.UserID, id)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	img, _, err := image.Decode(reader)
	if err != nil {
		t.Fatal(err)
	}
	if color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA).R != 200 || color.NRGBAModel.Convert(img.At(32, 32)).(color.NRGBA).B < 150 {
		t.Fatal("local blend lost source or result")
	}
	var count int64
	db.Model(&model.Resource{}).Count(&count)
	if count != 3 {
		t.Fatalf("resources=%d", count)
	}
}
