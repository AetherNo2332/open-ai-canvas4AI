package app

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
)

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
