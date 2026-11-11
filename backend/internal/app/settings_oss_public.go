package app

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"yingce/backend/internal/model"
)

func (s *Service) publicUserOSSSetting(setting *model.UserOSSSetting, value ossSettingValue, ownerID string, allowUserS3 bool) (PublicOSSSetting, error) {
	result := PublicOSSSetting{
		Enabled:            value.Enabled,
		Provider:           value.Provider,
		Region:             value.Region,
		Endpoint:           value.Endpoint,
		CDNBaseURL:         value.CDNBaseURL,
		Bucket:             value.Bucket,
		AccessKeyID:        value.AccessKeyID,
		HasAccessKeySecret: strings.TrimSpace(value.AccessKeySecret) != "",
		PublicBaseURL:      value.PublicBaseURL,
		PathPrefix:         value.PathPrefix,
		S3Preset:           value.S3Preset,
		PathStyle:          value.PathStyle,
		HasSessionToken:    value.SessionToken != "",
		StorageLocationID:  value.StorageLocationID,
		AllowUserS3:        allowUserS3,
		CDNAuthMode:        value.Delivery.CDNAuthMode,
		RequireCDN:         value.Delivery.RequireCDN,
		AllowPrivateProxy:  value.Delivery.AllowPrivateProxy,
	}
	if value.Provider == s3Provider && !allowUserS3 {
		result.Enabled = false
	}
	if setting != nil {
		result.UpdatedBy = setting.UserID
		result.CreatedAt = setting.CreatedAt
		result.UpdatedAt = setting.UpdatedAt
	}
	if err := s.populateStorageLocationPublic(&result, "user", ownerID, value.StorageLocationID); err != nil {
		return PublicOSSSetting{}, err
	}
	return result, nil
}

func storageLocationDigest(value ossSettingValue) string {
	value = normalizeOSSSetting(value)
	payload := strings.Join([]string{value.Provider, value.Endpoint, value.Bucket, value.Region, strconv.FormatBool(value.PathStyle), value.PathPrefix}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func storageTestDigest(value ossSettingValue) string {
	payload := strings.Join([]string{storageLocationDigest(value), value.AccessKeyID, value.AccessKeySecret, value.SessionToken}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
