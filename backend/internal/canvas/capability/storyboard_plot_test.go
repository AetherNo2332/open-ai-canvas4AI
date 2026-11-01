package capability

import "testing"

func TestStoryboardPlotMetadataAndPatch(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("script")
	if d.Metadata("剧情正文")["composerContent"] != "剧情正文" {
		t.Fatal("script creation discarded plot")
	}
	for _, field := range []string{"composerContent", "content"} {
		if err := d.ValidatePatch(map[string]any{field: "新剧情"}); err != nil {
			t.Fatalf("plot field %s rejected: %v", field, err)
		}
		if d.PatchFields[field].Path != "metadata.composerContent" {
			t.Fatalf("plot alias %s writes a different source", field)
		}
	}
	if err := d.ValidatePatch(map[string]any{"content": "one", "composerContent": "two"}); err == nil {
		t.Fatal("conflicting plot aliases accepted")
	}
	if err := d.ValidatePatch(map[string]any{"status": "success"}); err == nil {
		t.Fatal("undeclared metadata accepted")
	}
}
