package app

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
)

func (s *Service) structuredWriteQuota(tx *repository.Repository, ownerID, kind string, countDelta, deltaBytes int64) error {
	// Policy and usage are read from the locked transaction, never the root pool.
	reader := &Service{repo: tx, dataDir: s.dataDir}
	policy, err := reader.RuntimePolicy()
	if err != nil {
		return err
	}
	usage, err := tx.CanvasStructuredStorageUsage(ownerID)
	if err != nil {
		return err
	}
	if countDelta <= 0 {
		return validateStructuredStorageQuotaWithPolicy(usage, kind, false, deltaBytes, policy.Resource)
	}
	count, bytes := usage.CanvasCount+countDelta, usage.CanvasBytes+deltaBytes
	if kind == "asset" {
		count, bytes = usage.AssetCount+countDelta, usage.AssetBytes+deltaBytes
	}
	return validateStructuredReplacementQuotaWithPolicy(usage, kind, int(count), bytes, policy.Resource)
}

func (s *Service) CanvasCommandValidation(ctx context.Context, tx *repository.Repository, p identity.Principal) (commands.ValidationContext, error) {
	v := commands.ValidationContext{Principal: p, ModelInputLimits: map[string]map[string]int{}}
	catalog, err := s.ModelCatalog(nil)
	if err != nil {
		return v, err
	}
	for _, channel := range catalog.Channels {
		for _, m := range channel.Models {
			mode := normalizeCapability(m.Capability)
			if !m.Available || (mode != "image" && mode != "video") {
				continue
			}
			raw, err := json.Marshal(m.CapabilityConfig)
			if err != nil {
				return v, err
			}
			var profile ModelCapabilityConfig
			if err = json.Unmarshal(raw, &profile); err != nil {
				return v, err
			}
			limits := map[string]int{}
			if mode == "image" && profile.Image != nil {
				limits["image"] = profile.Image.References.MaxImages
			}
			if mode == "video" && profile.Video != nil {
				limits["image"], limits["video"], limits["audio"] = profile.Video.References.MaxImages, profile.Video.References.MaxVideos, profile.Video.References.MaxAudios
			}
			if v.ModelInputLimits[mode] == nil {
				v.ModelInputLimits[mode] = map[string]int{"image": 0, "video": 0, "audio": 0}
			}
			for kind, count := range limits {
				if count > v.ModelInputLimits[mode][kind] {
					v.ModelInputLimits[mode][kind] = count
				}
			}
		}
	}
	return v, nil
}
