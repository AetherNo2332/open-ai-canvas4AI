package canvas

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/canvas/submappreset"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

const maxUserSubmapPresets = 200

// UserSubmapPresetRequest accepts only the localized node snapshot. The server
// owns id/version/revision and overwrites any client-provided values.
type UserSubmapPresetRequest struct {
	ManifestJSON string `json:"manifestJson"`
}

func (s *Service) ListUserSubmapPresets(userID string) ([]model.UserSubmapPreset, error) {
	return s.repo.UserSubmapPresets(userID)
}

func (s *Service) CreateUserSubmapPreset(userID string, req UserSubmapPresetRequest) (model.UserSubmapPreset, error) {
	count, err := s.repo.UserSubmapPresetCount(userID)
	if err != nil {
		return model.UserSubmapPreset{}, err
	}
	if count >= maxUserSubmapPresets {
		return model.UserSubmapPreset{}, kernel.BadAuthRequest("用户自定义 submap 预制件最多保存 200 个")
	}
	snapshot, normalized, err := normalizeUserSubmapPresetSnapshot(req.ManifestJSON, kernel.NewID())
	if err != nil {
		return model.UserSubmapPreset{}, kernel.BadAuthRequest(err.Error())
	}
	now := time.Now().UTC()
	preset := model.UserSubmapPreset{
		ID:           snapshot.ID,
		UserID:       userID,
		Name:         snapshot.Title,
		ManifestJSON: normalized,
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.CreateUserSubmapPreset(&preset); err != nil {
		return model.UserSubmapPreset{}, err
	}
	return preset, nil
}

func (s *Service) UpdateUserSubmapPreset(userID, id string, req UserSubmapPresetRequest) (model.UserSubmapPreset, error) {
	preset, err := s.repo.UserSubmapPresetForUser(userID, id)
	if err != nil {
		return model.UserSubmapPreset{}, userSubmapPresetNotFound(err)
	}
	snapshot, normalized, err := normalizeUserSubmapPresetSnapshot(req.ManifestJSON, preset.ID)
	if err != nil {
		return model.UserSubmapPreset{}, kernel.BadAuthRequest(err.Error())
	}
	preset.Name = snapshot.Title
	preset.ManifestJSON = normalized
	preset.Revision++
	preset.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateUserSubmapPreset(preset); err != nil {
		return model.UserSubmapPreset{}, userSubmapPresetNotFound(err)
	}
	return *preset, nil
}

func (s *Service) DeleteUserSubmapPreset(userID, id string) error {
	if err := s.repo.DeleteUserSubmapPreset(userID, id); err != nil {
		return userSubmapPresetNotFound(err)
	}
	return nil
}

func userSubmapPresetNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return kernel.NewAppError(http.StatusNotFound, "用户预制不存在或无权访问")
	}
	return err
}

func normalizeUserSubmapPresetSnapshot(value, id string) (submappreset.Snapshot, string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return submappreset.Snapshot{}, "", errors.New("预制快照不能为空")
	}
	if len([]byte(trimmed)) > submappreset.MaxSnapshotLen {
		return submappreset.Snapshot{}, "", errors.New("预制快照过大（上限 512KB）")
	}
	var snapshot submappreset.Snapshot
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return submappreset.Snapshot{}, "", errors.New("预制快照不是合法的 JSON 或与契约字段不符")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return submappreset.Snapshot{}, "", errors.New("预制快照只能包含一个 JSON 对象")
	}
	// version 是协议门禁，不是可由客户端改写的元数据。缺省时按当前版本补齐；
	// 显式声明其他版本必须在解析阶段整份拒绝，不能用当前结构误读未来载荷。
	if snapshot.Version != 0 && snapshot.Version != submappreset.SupportedVersion {
		return submappreset.Snapshot{}, "", errors.New("预制版本不受支持；版本不符整份拒绝")
	}
	snapshot.ID = strings.TrimSpace(id)
	snapshot.Version = submappreset.SupportedVersion
	snapshot.Title = strings.TrimSpace(snapshot.Title)
	if err := submappreset.Validate(snapshot); err != nil {
		return submappreset.Snapshot{}, "", errors.New(err.Error())
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return submappreset.Snapshot{}, "", errors.New("预制快照序列化失败")
	}
	if len(encoded) > submappreset.MaxSnapshotLen {
		return submappreset.Snapshot{}, "", errors.New("规范化后的预制快照过大")
	}
	return snapshot, string(encoded), nil
}
