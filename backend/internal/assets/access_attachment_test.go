package assets

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/storage"
)

func TestBrowserNonMediaAlwaysRequiresAttachmentWithoutProxy(t *testing.T) {
	now := time.Now().UTC()
	for _, mediaType := range []string{"image/svg+xml", "application/xml", "text/html", "application/pdf", "application/octet-stream", ""} {
		t.Run(mediaType, func(t *testing.T) {
			resource := testReadyResource("aliyun")
			resource.MimeType = mediaType
			setting := storage.Settings{Provider: "aliyun", Endpoint: "https://1.1.1.1", Bucket: "bucket", AccessKeyID: "id", AccessKeySecret: "secret", CDNBaseURL: "https://cdn.example.com", Delivery: storage.DeliverySettings{CDNAuthMode: "public", AllowPrivateProxy: true}}
			for _, purpose := range []AccessPurpose{PurposeDisplay, PurposeCopy, PurposeProcess, PurposeDownload} {
				access, err := ResolveAccess(resource, setting, AccessOptions{Purpose: purpose}, now, testPlatformURL(&[]ResourceVariant{}))
				if err != nil {
					t.Fatal(err)
				}
				parsed, _ := url.Parse(access.URL)
				if access.Delivery != DeliveryOrigin || !strings.HasPrefix(parsed.Query().Get("response-content-disposition"), "attachment") {
					t.Fatalf("%s: %#v", purpose, access)
				}
			}
			setting.Endpoint = "http://storage.internal"
			if _, err := ResolveAccess(resource, setting, AccessOptions{}, now, testPlatformURL(&[]ResourceVariant{})); err == nil {
				t.Fatal("unsafe attachment access was accepted")
			}
			provider, err := ResolveAccess(resource, setting, AccessOptions{Purpose: PurposeProvider}, now, testPlatformURL(&[]ResourceVariant{}))
			if err != nil || provider.Delivery != DeliveryCDN {
				t.Fatalf("provider policy changed: %#v %v", provider, err)
			}
		})
	}
}

func TestPublicCDNAttachmentRequiresExplicitForwardingContract(t *testing.T) {
	setting := storage.Settings{Provider: "aliyun", Endpoint: "http://storage.internal", CDNBaseURL: "https://cdn.example.com", Delivery: storage.DeliverySettings{CDNAuthMode: "public"}}
	resource := testReadyResource("aliyun")
	now := time.Now().UTC()
	if _, err := ResolveAccess(resource, setting, AccessOptions{Purpose: PurposeDownload}, now, testPlatformURL(&[]ResourceVariant{})); err == nil {
		t.Fatal("CDN without attachment contract must fail")
	}
	setting.Delivery.CDNAttachmentQueryForwarding = true
	for _, mediaType := range []string{"image/png", "image/svg+xml"} {
		resource.MimeType = mediaType
		access, err := ResolveAccess(resource, setting, AccessOptions{Purpose: PurposeDownload, DownloadName: "镜头.png"}, now, testPlatformURL(&[]ResourceVariant{}))
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(access.URL)
		if access.Delivery != DeliveryCDN || access.ExpiresAt != nil || parsed.Host != "cdn.example.com" || !strings.HasPrefix(parsed.Query().Get("response-content-disposition"), "attachment") {
			t.Fatalf("CDN access = %#v", access)
		}
	}
}

func TestOriginDisplayWindowUsesConfiguredTTLWithoutChangingProviderTTL(t *testing.T) {
	now := time.Date(2026, 10, 23, 13, 17, 0, 0, time.UTC)
	setting := storage.Settings{Provider: "aliyun", Endpoint: "https://1.1.1.1", Bucket: "bucket", AccessKeyID: "id", AccessKeySecret: "secret", Runtime: storage.RuntimePolicy{AccessURLTTL: 2 * time.Hour, ProviderAccessURLTTL: 6 * time.Hour}}
	resource := testReadyResource("aliyun")
	for _, purpose := range []AccessPurpose{PurposeDisplay, PurposeCopy, PurposeProcess} {
		first, err := ResolveAccess(resource, setting, AccessOptions{Purpose: purpose}, now, testPlatformURL(&[]ResourceVariant{}))
		if err != nil {
			t.Fatal(err)
		}
		second, err := ResolveAccess(resource, setting, AccessOptions{Purpose: purpose}, now.Add(time.Minute), testPlatformURL(&[]ResourceVariant{}))
		if err != nil {
			t.Fatal(err)
		}
		if first.URL != second.URL || !first.ExpiresAt.Equal(time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC)) {
			t.Fatalf("unstable configured window: %#v %#v", first, second)
		}
		parsed, _ := url.Parse(first.URL)
		if parsed.Query().Get("response-cache-control") != "private, max-age=5400, immutable" {
			t.Fatalf("cache policy = %s", parsed.RawQuery)
		}
		next, err := ResolveAccess(resource, setting, AccessOptions{Purpose: purpose}, now.Add(30*time.Minute), testPlatformURL(&[]ResourceVariant{}))
		if err != nil || next.URL == first.URL || !next.ExpiresAt.Equal(time.Date(2026, 10, 23, 15, 30, 0, 0, time.UTC)) {
			t.Fatalf("window did not rotate: %#v %v", next, err)
		}
		capped, err := ResolveAccess(resource, setting, AccessOptions{Purpose: purpose, ExpiresAt: now.Add(40 * time.Second)}, now, testPlatformURL(&[]ResourceVariant{}))
		if err != nil {
			t.Fatal(err)
		}
		cappedURL, _ := url.Parse(capped.URL)
		if !capped.ExpiresAt.Equal(now.Add(40*time.Second)) || cappedURL.Query().Get("response-cache-control") != "private, no-store" {
			t.Fatalf("outer expiry exceeded: %#v", capped)
		}
		cappedAgain, err := ResolveAccess(resource, setting, AccessOptions{Purpose: purpose, ExpiresAt: now.Add(40 * time.Second)}, now.Add(10*time.Second), testPlatformURL(&[]ResourceVariant{}))
		if err != nil || cappedAgain.URL != capped.URL {
			t.Fatalf("capped window is unstable: %#v %v", cappedAgain, err)
		}
	}
	provider, err := ResolveAccess(resource, setting, AccessOptions{Purpose: PurposeProvider}, now, testPlatformURL(&[]ResourceVariant{}))
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(provider.URL)
	if !provider.ExpiresAt.Equal(now.Add(6*time.Hour)) || parsed.Query().Get("response-cache-control") != "" {
		t.Fatalf("provider expiry/cache changed: %#v", provider)
	}
}
