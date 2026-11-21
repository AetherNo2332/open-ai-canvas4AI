package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"yingce/backend/internal/model"
)

func TestChannelModelCatalogOptionPreservesJSONContract(t *testing.T) {
	full, err := json.Marshal(ChannelModelCatalogOption{Value: "16:9", Label: "HD"})
	if err != nil {
		t.Fatal(err)
	}
	if string(full) != `{"value":"16:9","label":"HD"}` {
		t.Fatalf("full option JSON = %s", full)
	}
	withoutLabel, err := json.Marshal(ChannelModelCatalogOption{Value: "16:9"})
	if err != nil {
		t.Fatal(err)
	}
	if string(withoutLabel) != `{"value":"16:9"}` {
		t.Fatalf("option without label JSON = %s", withoutLabel)
	}
}

func TestFetchChannelModelCatalogSortsDeduplicatesAndNormalizes(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "false")
	t.Setenv("ENABLE_PROVIDER_PLUGINS", "false")
	t.Setenv("NO_PROXY", "127.0.0.1")
	t.Setenv("no_proxy", "127.0.0.1")

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Catalog-Test") != "enabled" {
			t.Errorf("custom header = %q", r.Header.Get("X-Catalog-Test"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "data": [
    {
      "id": " Zeta ",
      "name": "ignored-fallback",
      "display_name": " Zeta Display ",
      "model_type": "VIDEO",
      "supported_endpoint_types": [" chat ", "chat", "", " image "],
      "default_parameters": {"aspect_ratio": " 16:9 ", "duration_seconds": " 5 ", "resolution": " 720p "},
      "options": {
        "aspect_ratio": [{"value": " 16:9 ", "label": " HD "}, {"value": "16:9", "label": "duplicate"}, {"value": "  ", "label": "empty"}],
        "duration_seconds": [{"value": " 5 ", "label": " Five "}, {"value": "5", "label": "duplicate"}],
        "resolution": [{"value": " 720p ", "label": " HD "}]
      },
      "supports_images": true,
      "min_images": 1,
      "max_images": 4
    },
    {"id": "models/Alpha", "display_name": " First "},
    {"id": " Alpha ", "display_name": " duplicate "},
    {"name": "models/Gamma", "model_type": "embedding"},
    {"id": "", "name": ""},
    {"id": "Beta", "model_type": " Text "}
  ]
}`))
	}))
	defer upstream.Close()

	svc := &Service{}
	input := ChannelModelsRequest{
		BaseURL: upstream.URL + "/",
		APIKey:  "test-key",
		Headers: []OutboundHeader{{Name: "X-Catalog-Test", Value: "enabled"}},
	}
	catalog, err := svc.FetchChannelModelCatalog(context.Background(), &model.User{ID: "user-1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"Alpha", "Beta", "Gamma", "Zeta"}
	gotIDs := make([]string, 0, len(catalog))
	byID := make(map[string]ChannelModelCatalogItem, len(catalog))
	for _, item := range catalog {
		gotIDs = append(gotIDs, item.ID)
		byID[item.ID] = item
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("IDs = %#v, want %#v", gotIDs, wantIDs)
	}
	if byID["Alpha"].DisplayName != "First" {
		t.Fatalf("Alpha display name = %q", byID["Alpha"].DisplayName)
	}
	if byID["Beta"].ModelType != "text" {
		t.Fatalf("Beta model type = %q", byID["Beta"].ModelType)
	}
	if byID["Gamma"].ModelType != "" {
		t.Fatalf("Gamma model type = %q, want unknown type removed", byID["Gamma"].ModelType)
	}
	zeta := byID["Zeta"]
	if zeta.ModelType != "video" || zeta.DisplayName != "Zeta Display" {
		t.Fatalf("Zeta = %#v", zeta)
	}
	if !reflect.DeepEqual(zeta.SupportedEndpointTypes, []string{"chat", "image"}) {
		t.Fatalf("Zeta endpoint types = %#v", zeta.SupportedEndpointTypes)
	}
	if zeta.DefaultParameters.AspectRatio != "16:9" || zeta.DefaultParameters.DurationSeconds != "5" || zeta.DefaultParameters.Resolution != "720p" {
		t.Fatalf("Zeta default parameters = %#v", zeta.DefaultParameters)
	}
	wantAspectRatios := []ChannelModelCatalogOption{
		{Value: "16:9", Label: "HD"},
	}
	if !reflect.DeepEqual(zeta.Options.AspectRatio, wantAspectRatios) {
		t.Fatalf("Zeta aspect ratios = %#v, want %#v", zeta.Options.AspectRatio, wantAspectRatios)
	}
	if len(zeta.Options.DurationSeconds) != 1 || zeta.Options.DurationSeconds[0] != (ChannelModelCatalogOption{Value: "5", Label: "Five"}) {
		t.Fatalf("Zeta durations = %#v", zeta.Options.DurationSeconds)
	}
	if zeta.SupportsImages == nil || !*zeta.SupportsImages || zeta.MinImages == nil || *zeta.MinImages != 1 || zeta.MaxImages == nil || *zeta.MaxImages != 4 {
		t.Fatalf("Zeta image constraints = %#v", zeta)
	}

	names, err := svc.FetchChannelModels(context.Background(), &model.User{ID: "user-1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, wantIDs) {
		t.Fatalf("FetchChannelModels() = %#v, want %#v", names, wantIDs)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestFetchChannelModelCatalogRejectsMissingIdentityOrCredentials(t *testing.T) {
	t.Setenv("ENABLE_PROVIDER_PLUGINS", "false")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()

	svc := &Service{}
	validActor := &model.User{ID: "user-1"}
	for _, test := range []struct {
		name  string
		actor *model.User
		input ChannelModelsRequest
		code  int
	}{
		{
			name:  "not logged in",
			actor: nil,
			input: ChannelModelsRequest{BaseURL: upstream.URL, APIKey: "test-key"},
			code:  http.StatusUnauthorized,
		},
		{
			name:  "missing api key",
			actor: validActor,
			input: ChannelModelsRequest{BaseURL: upstream.URL},
			code:  http.StatusBadRequest,
		},
		{
			name:  "missing base url",
			actor: validActor,
			input: ChannelModelsRequest{APIKey: "test-key"},
			code:  http.StatusBadRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.FetchChannelModelCatalog(context.Background(), test.actor, test.input)
			var authErr *AuthError
			if !errors.As(err, &authErr) || authErr.Status != test.code {
				t.Fatalf("err = %#v, want status %d", err, test.code)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("rejected requests reached upstream %d times", got)
	}
}
