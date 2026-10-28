package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"strings"
	"yingce/backend/internal/assets"
	"yingce/backend/internal/canvas/tasks"
	"yingce/backend/internal/model"
)

// Local image transformation runs before the materialization transaction. It
// accesses only owner-checked resources and stores with a stable upload identity.
func (s *Service) prepareCanvasTaskOutput(ctx context.Context, task model.Task, binding model.CanvasTaskBinding) (model.Task, error) {
	if binding.Mode != "image" || binding.ResultContextJSON == "" || task.Status != model.TaskStatusSucceeded {
		return task, nil
	}
	if task.UserID != binding.OwnerID || binding.Mode != "image" {
		return task, fmt.Errorf("invalid image result binding")
	}
	var edit tasks.EmotionContext
	if err := json.Unmarshal([]byte(binding.ResultContextJSON), &edit); err != nil {
		return task, err
	}
	uploadKey := normalizedResourceUploadKey([]string{"canvas-emotion-result:" + task.ID})
	resource, err := s.resourceForUploadKey(task.UserID, uploadKey)
	if err != nil {
		return task, err
	}
	if resource == nil || resource.Status != model.ResourceStatusReady {
		var result struct {
			Images []struct {
				StorageKey string `json:"storageKey"`
				DataURL    string `json:"dataUrl"`
			}
		}
		if err = json.Unmarshal([]byte(task.ResultJSON), &result); err != nil || len(result.Images) < 1 {
			return task, fmt.Errorf("emotion generated image missing")
		}
		id := assets.ResourceID(result.Images[0].StorageKey)
		if id == "" {
			id = assets.ResourceID(result.Images[0].DataURL)
		}
		if id == "" {
			return task, fmt.Errorf("emotion output is not a stored resource")
		}
		source, err := s.readCanvasTaskImage(ctx, task.UserID, edit.SourceResourceID)
		if err != nil {
			return task, err
		}
		generated, err := s.readCanvasTaskImage(ctx, task.UserID, id)
		if err != nil {
			return task, err
		}
		combined, err := tasks.CompositeEmotion(source, generated, edit.EditRegion, edit.FaceBox)
		if err != nil {
			return task, err
		}
		var encoded bytes.Buffer
		if err = png.Encode(&encoded, combined); err != nil {
			return task, err
		}
		if err = ctx.Err(); err != nil {
			return task, err
		}
		day, err := s.reserveGeneratedResourceQuota(task.UserID, int64(encoded.Len()))
		if err != nil {
			return task, err
		}
		saved, _, err := s.storeResourceWithWriter(task.UserID, "image", "emotion.png", "image/png", int64(encoded.Len()), combined.Bounds().Dx(), combined.Bounds().Dy(), 0, bytes.NewReader(encoded.Bytes()), uploadKey, false, s.storeTaskMediaObject)
		if err != nil {
			s.releaseUserUploadQuota(task.UserID, day, int64(encoded.Len()))
			return task, err
		}
		s.commitUserUploadQuota(task.UserID, int64(encoded.Len()))
		resource = saved
	}
	raw, err := json.Marshal(map[string]any{"mode": "image", "images": []any{map[string]any{"storageKey": "resource:" + resource.ID, "dataUrl": resourceFileURL(resource.ID), "width": resource.Width, "height": resource.Height, "bytes": resource.Size, "mimeType": resource.MimeType}}})
	if err != nil {
		return task, err
	}
	task.ResultJSON = string(raw)
	return task, nil
}
func (s *Service) readCanvasTaskImage(ctx context.Context, owner, id string) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resource, reader, err := s.OpenResource(owner, id)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if !strings.HasPrefix(resource.MimeType, "image/") || resource.Size > 64<<20 {
		return nil, fmt.Errorf("image resource too large or wrong kind")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (64<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 64<<20 {
		return nil, fmt.Errorf("image resource exceeds byte limit")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return nil, fmt.Errorf("image exceeds pixel limit")
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	return decoded, err
}
