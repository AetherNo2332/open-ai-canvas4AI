package providercatalog

import (
	"reflect"
	"testing"
)

func TestNormalizeModelType(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "lowercase", input: " Text ", want: "text"},
		{name: "uppercase", input: "VIDEO", want: "video"},
		{name: "unknown", input: "embedding", want: ""},
		{name: "empty", input: "  ", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeModelType(test.input); got != test.want {
				t.Fatalf("NormalizeModelType(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizeModelOptions(t *testing.T) {
	input := []ModelOption{
		{Value: " 16:9 ", Label: " HD "},
		{Value: "16:9", Label: "duplicate"},
		{Value: "  ", Label: "empty"},
		{Value: "9:16", Label: " Vertical "},
	}
	want := []ModelOption{
		{Value: "16:9", Label: "HD"},
		{Value: "9:16", Label: "Vertical"},
	}
	if got := NormalizeModelOptions(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeModelOptions() = %#v, want %#v", got, want)
	}
	if got := NormalizeModelOptions(nil); got == nil || len(got) != 0 {
		t.Fatalf("NormalizeModelOptions(nil) = %#v, want non-nil empty slice", got)
	}
}

func TestNormalizeEndpointTypes(t *testing.T) {
	input := []string{" chat ", "chat", "", " image ", "image"}
	want := []string{"chat", "image"}
	if got := NormalizeEndpointTypes(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeEndpointTypes() = %#v, want %#v", got, want)
	}
	if got := NormalizeEndpointTypes(nil); got == nil || len(got) != 0 {
		t.Fatalf("NormalizeEndpointTypes(nil) = %#v, want non-nil empty slice", got)
	}
}

func TestNormalizeModelID(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "trims", input: "  gpt-test  ", want: "gpt-test"},
		{name: "gemini prefix", input: " models/gemini-test ", want: "gemini-test"},
		{name: "one prefix only", input: "models/models/gemini-test", want: "models/gemini-test"},
		{name: "empty after prefix", input: " models/ ", want: ""},
		{name: "blank", input: "  ", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeModelID(test.input); got != test.want {
				t.Fatalf("NormalizeModelID(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
