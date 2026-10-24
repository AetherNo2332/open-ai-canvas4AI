package assets

import (
	"encoding/binary"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// InlineMediaType is deliberately limited to passive browser media. SVG/XML,
// documents and unrecognized types always use an attachment response.
func InlineMediaType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp", "image/x-icon",
		"audio/mpeg", "audio/mp4", "audio/aac", "audio/ogg", "audio/wav", "audio/wave", "audio/x-wav", "audio/flac", "audio/x-flac", "audio/webm", "audio/midi", "audio/basic", "audio/aiff",
		"video/mp4", "video/webm", "video/ogg", "video/mpeg", "video/quicktime", "video/x-msvideo":
		return true
	}
	return false
}

// DetectedContentType never lets a client/header/extension turn unknown bytes
// into inline media. Metadata may refine only non-inline unknown content.
func DetectedContentType(prefix []byte, declared, fileName string) string {
	// Go's MP4 sniffer recognizes mp4 brands, but misses audio-only M4A.
	// Inspect the bounded ftyp box, never trust a filename or declared MIME.
	if len(prefix) >= 16 && string(prefix[4:8]) == "ftyp" {
		size := int(binary.BigEndian.Uint32(prefix[:4]))
		if size >= 16 && size <= len(prefix) && size%4 == 0 {
			switch string(prefix[8:12]) {
			case "M4A ", "M4B ", "M4P ":
				return "audio/mp4"
			}
		}
	}
	if len(prefix) >= 42 && string(prefix[:4]) == "fLaC" && prefix[4]&0x7f == 0 && prefix[5] == 0 && prefix[6] == 0 && prefix[7] == 34 {
		return "audio/flac"
	}
	detected := normalizedContentType(http.DetectContentType(prefix))
	if detected != "" && detected != "application/octet-stream" {
		return detected
	}
	for _, candidate := range []string{declared, mime.TypeByExtension(filepath.Ext(fileName))} {
		if value := normalizedContentType(candidate); value != "" && !InlineMediaType(value) {
			return value
		}
	}
	return "application/octet-stream"
}

func normalizedContentType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(mediaType)
}
