package app

import (
	"context"
	"errors"
	"os"

	"yingce/backend/internal/model"
)

func (s *Service) ensureTaskMediaDownload(ctx context.Context, task *model.Task, config *providerConfig, checkpoint *mediaCheckpoint, item *mediaCheckpointItem) (string, error) {
	path := s.mediaTempPath(item.TempName)
	info, err := os.Lstat(path)
	if path != "" && err == nil && info.Mode().IsRegular() {
		return path, nil
	}
	if item.Reference.URL == "" {
		return "", &mediaRecoveryError{stage: "download", cause: errors.New("暂存结果已过期且上游没有可重新下载的地址")}
	}
	if err := s.saveMediaCheckpoint(task, checkpoint, "download"); err != nil {
		return "", err
	}
	*config, err = s.resolveProviderConfig(*config)
	if err != nil {
		return "", &mediaRecoveryError{stage: "download", cause: err}
	}
	item.TempName, item.MIMEType, err = s.downloadTaskMedia(ctx, *config, item.Reference.URL, checkpoint.Mode)
	if err != nil {
		return "", &mediaRecoveryError{stage: "download", retryable: retryableMediaRecovery(err), cause: err}
	}
	path = s.mediaTempPath(item.TempName)
	if err := s.saveMediaCheckpoint(task, checkpoint, "download"); err != nil {
		return "", err
	}
	return path, nil
}
