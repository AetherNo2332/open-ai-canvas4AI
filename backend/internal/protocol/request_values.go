package protocol

import "strings"

// MediaInput is the host-neutral media shape accepted by request assembly.
// Hosts remain responsible for resolving storage references and public URLs
// before handing the normalized reference to an adapter.
type MediaInput struct {
	ID         string
	URL        string
	DataURL    string
	Role       string
	MIMEType   string
	Name       string
	StorageKey string
	Bytes      int64
	Width      int
	Height     int
	DurationMs int64
}

// VideoFrameHints carries the placement metadata used to assign first/last
// frame roles to video reference images.
type VideoFrameHints struct {
	Operation    string
	StartFrameID string
	EndFrameID   string
}

// NormalizeImageParameterValues removes the duplicated size field. Size is
// encoded through the request's aspect ratio so the route-specific pixel
// conversion remains authoritative.
func NormalizeImageParameterValues(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	result := make(map[string]any, len(values))
	for name, value := range values {
		if name == "size" {
			continue
		}
		result[name] = value
	}
	return result
}

// NormalizeMediaInput trims media metadata and creates the stable fields used
// by manifests. Empty media references are dropped by the list builders below.
func NormalizeMediaInput(value MediaInput, kind string, order int) MediaReference {
	return MediaReference{
		ID:       strings.TrimSpace(value.ID),
		URL:      strings.TrimSpace(value.URL),
		DataURL:  strings.TrimSpace(value.DataURL),
		Kind:     kind,
		Role:     strings.TrimSpace(value.Role),
		MIMEType: strings.TrimSpace(value.MIMEType),
		Name:     strings.TrimSpace(value.Name),
		Order:    order,
		Metadata: map[string]any{
			"bytes":      value.Bytes,
			"width":      value.Width,
			"height":     value.Height,
			"durationMs": value.DurationMs,
			"storageKey": strings.TrimSpace(value.StorageKey),
		},
	}
}

// NormalizeMediaReferences assigns the protocol role for a media kind and
// removes references that have no URL or inline data.
func NormalizeMediaReferences(values []MediaReference, kind string) []MediaReference {
	result := make([]MediaReference, 0, len(values))
	for index, value := range values {
		item := normalizeMediaReference(value, kind, index)
		switch kind {
		case "video":
			item.Role = "reference_video"
		case "audio":
			item.Role = "reference_audio"
		}
		if item.URL != "" || item.DataURL != "" {
			result = append(result, item)
		}
	}
	return result
}

// BuildImageReferences applies the image/video role rules. Video masks are
// intentionally ignored, matching the existing manifest contract.
func BuildImageReferences(mode string, images []MediaReference, mask *MediaReference, hints VideoFrameHints) []MediaReference {
	if mode == "video" {
		fallbackRole := ""
		if strings.TrimSpace(hints.StartFrameID) != "" || strings.TrimSpace(hints.EndFrameID) != "" {
			fallbackRole = "reference_image"
		}
		result := make([]MediaReference, 0, len(images))
		for index, value := range images {
			item := normalizeMediaReference(value, "image", index)
			item.Role = videoImageRole(item, hints, fallbackRole)
			if item.URL != "" || item.DataURL != "" {
				result = append(result, item)
			}
		}
		return result
	}

	result := make([]MediaReference, 0, len(images)+1)
	for index, value := range images {
		item := normalizeMediaReference(value, "image", index)
		item.Role = "reference_image"
		if mode == "image" {
			item.Role = "edit_source"
		}
		if item.URL != "" || item.DataURL != "" {
			result = append(result, item)
		}
	}
	if mask != nil {
		item := normalizeMediaReference(*mask, "image", len(result))
		item.Role = "mask"
		if item.URL != "" || item.DataURL != "" {
			result = append(result, item)
		}
	}
	return result
}

func normalizeMediaReference(value MediaReference, kind string, order int) MediaReference {
	value.ID = strings.TrimSpace(value.ID)
	value.URL = strings.TrimSpace(value.URL)
	value.DataURL = strings.TrimSpace(value.DataURL)
	value.Kind = kind
	value.Role = strings.TrimSpace(value.Role)
	value.MIMEType = strings.TrimSpace(value.MIMEType)
	value.Name = strings.TrimSpace(value.Name)
	value.Order = order
	if value.Metadata != nil {
		metadata := make(map[string]any, len(value.Metadata))
		for key, item := range value.Metadata {
			metadata[key] = item
		}
		if storageKey, ok := metadata["storageKey"].(string); ok {
			metadata["storageKey"] = strings.TrimSpace(storageKey)
		}
		value.Metadata = metadata
	}
	return value
}

func videoImageRole(image MediaReference, hints VideoFrameHints, fallback string) string {
	if image.Role == "first_frame" || image.Role == "last_frame" {
		return image.Role
	}
	if strings.TrimSpace(hints.Operation) == "reference_to_video" {
		return "reference_image"
	}
	if id := strings.TrimSpace(hints.StartFrameID); id != "" && image.ID == id {
		return "first_frame"
	}
	if id := strings.TrimSpace(hints.EndFrameID); id != "" && image.ID == id {
		return "last_frame"
	}
	return fallback
}
