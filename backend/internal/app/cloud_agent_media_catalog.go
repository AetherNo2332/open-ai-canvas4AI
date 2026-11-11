package app

import "yingce/backend/internal/canvas/contract"

// The Agent uses the same public catalog as the composer, never a second routing policy.
func (s *Service) cloudAgentModelList(userID string, intent *ModelRequestIntent) (any, error) {
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, err
	}
	catalog, err := s.ModelCatalog(access, intent)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, m := range catalog.Models {
		if m.Available && cloudAgentGenerationModeSupported(normalizeCapability(m.Capability)) {
			if !cloudAgentVideoCatalogMatches(intent, m.CapabilitySpec.Operations, "") {
				continue
			}
			items = append(items, s.cloudAgentModelListItem(userID, map[string]any{
				"name": m.Name, "capability": m.Capability,
				"selection":  map[string]any{"logicalModelId": m.ID},
				"priceLabel": m.PriceLabel, "priceTiers": m.PriceTiers, "options": m.CapabilitySpec,
				"profiles": m.CapabilityProfiles, "defaults": m.DefaultOptions,
			}, cloudAgentSelection{LogicalModelID: m.ID}))
		}
	}
	for _, channel := range catalog.Channels {
		for _, m := range channel.Models {
			if m.Available && cloudAgentGenerationModeSupported(normalizeCapability(m.Capability)) {
				if intent != nil && intent.Capability == "video" {
					video, _ := m.CapabilityConfig["video"].(map[string]any)
					if !cloudAgentVideoCatalogMatches(intent, stringSlice(video["operations"]), stringValue(video["defaultOperation"])) {
						continue
					}
				}
				items = append(items, s.cloudAgentModelListItem(userID, map[string]any{
					"name": m.DisplayName, "capability": m.Capability,
					"selection":  map[string]any{"channelId": channel.ID, "channelModelKey": m.ModelKey},
					"priceLabel": m.PriceLabel, "priceTiers": m.PriceTiers, "options": m.CapabilityConfig, "defaults": m.DefaultOptions,
				}, cloudAgentSelection{ChannelID: channel.ID, ChannelModelKey: m.ModelKey}))
			}
		}
	}
	return map[string]any{"source": catalog.Source, "models": items, "intent": intent}, nil
}

type cloudAgentMediaArgs struct {
	ImageResolution            string                      `json:"imageResolution,omitempty"`
	ImageParameters            string                      `json:"imageParameters,omitempty"`
	VideoWatermark             *bool                       `json:"videoWatermark,omitempty"`
	VideoArkPrivateAssetUpload *bool                       `json:"videoArkPrivateAssetUpload,omitempty"`
	VideoStartFrameNodeID      string                      `json:"videoStartFrameNodeId,omitempty"`
	VideoEndFrameNodeID        string                      `json:"videoEndFrameNodeId,omitempty"`
	Intent                     string                      `json:"intent,omitempty"`
	StoryboardNodeID           string                      `json:"storyboardNodeId,omitempty"`
	StoryboardRowID            string                      `json:"storyboardRowId,omitempty"`
	ResolvedBindings           []contract.ReferenceBinding `json:"-"`
	Prepared                   *cloudAgentPreparedMedia    `json:"-"`
	DraftRunID                 string                      `json:"-"`
	CharacterVersions          map[string]string           `json:"-"`
	CharacterLabels            []string                    `json:"-"`
	Mode                       string                      `json:"mode"`
	Prompt                     string                      `json:"prompt"`
	SelectionID                string                      `json:"selectionId"`
	LogicalModelID             string                      `json:"logicalModelId"`
	ChannelID                  string                      `json:"channelId"`
	ChannelModelKey            string                      `json:"channelModelKey"`
	Duration                   int                         `json:"durationSeconds"`
	Size                       string                      `json:"size"`
	Quality                    string                      `json:"quality"`
	VideoGenerateAudio         *bool                       `json:"videoGenerateAudio,omitempty"`
	SnapshotHash               string                      `json:"snapshotHash"`
	NodeID                     string                      `json:"nodeId"`
	Title                      string                      `json:"title"`
	SourceNodeID               string                      `json:"sourceNodeId"`
	ReferenceNodeIDs           []string                    `json:"referenceNodeIds"`
	ReferenceTransientIDs      []string                    `json:"referenceTransientIds,omitempty"`
}
