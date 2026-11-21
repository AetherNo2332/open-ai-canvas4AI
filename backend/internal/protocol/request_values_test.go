package protocol

import "testing"

func TestBuildImageReferencesPreservesVideoRolesAndOrder(t *testing.T) {
	images := []MediaReference{
		{ID: "start", URL: "https://media.example/start.png", Role: "first_frame"},
		{ID: "plain", URL: "https://media.example/plain.png"},
		{ID: "end", DataURL: "data:image/png;base64,ZW5k"},
	}
	result := BuildImageReferences("video", images, &MediaReference{URL: "https://media.example/mask.png"}, VideoFrameHints{
		StartFrameID: "start",
		EndFrameID:   "end",
	})
	if len(result) != 3 {
		t.Fatalf("references = %#v", result)
	}
	if result[0].Role != "first_frame" || result[1].Role != "reference_image" || result[2].Role != "last_frame" {
		t.Fatalf("video roles = %q, %q, %q", result[0].Role, result[1].Role, result[2].Role)
	}
	if result[0].Order != 0 || result[1].Order != 1 || result[2].Order != 2 {
		t.Fatalf("orders = %#v", result)
	}
}

func TestBuildImageReferencesAppendsEditMaskAndDropsEmptyMedia(t *testing.T) {
	images := []MediaReference{
		{URL: "https://media.example/source.png"},
		{ID: "empty"},
	}
	result := BuildImageReferences("image", images, &MediaReference{DataURL: "data:image/png;base64,bWFzaw=="}, VideoFrameHints{})
	if len(result) != 2 {
		t.Fatalf("references = %#v", result)
	}
	if result[0].Role != "edit_source" || result[1].Role != "mask" || result[1].Order != 1 {
		t.Fatalf("image roles = %#v", result)
	}
}

func TestNormalizeImageParameterValuesOmitsSize(t *testing.T) {
	values := map[string]any{"size": "1024x1024", "quality": "high", "enabled": false}
	normalized := NormalizeImageParameterValues(values)
	if _, ok := normalized["size"]; ok {
		t.Fatal("size was retained")
	}
	if normalized["quality"] != "high" || normalized["enabled"] != false {
		t.Fatalf("normalized values = %#v", normalized)
	}
	if values["size"] != "1024x1024" {
		t.Fatal("input was mutated")
	}
}
