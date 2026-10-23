package hostupdate

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseHealthInternalVersion(t *testing.T) {
	for _, test := range []struct {
		build, target string
		wantError     bool
		omitVersion   bool
	}{
		{build: "v1.6.0(abcdef0)", target: "v1.6.0"},
		{build: "v1.6.1(1234567)", target: "v1.6.1"},
		{build: "v1.6.0(abcdef0)", target: "v1.6.1", wantError: true},
		{build: "v1.6.0.1(abcdef0)", target: "v1.6.0.1"},
		{target: "v1.6.1", wantError: true},
		{target: "v1.6.1", wantError: true, omitVersion: true},
	} {
		name := test.build + "-" + test.target
		if test.omitVersion {
			name = "missing-" + test.target
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.omitVersion {
					fmt.Fprint(w, `{"code":0,"data":{"build":{}}}`)
					return
				}
				fmt.Fprintf(w, `{"code":0,"data":{"build":{"version":%q}}}`, test.build)
			}))
			defer server.Close()
			manager := &Manager{httpClient: server.Client()}
			if err := manager.checkHealthOnce(server.URL, test.target); (err != nil) != test.wantError {
				t.Fatalf("checkHealthOnce() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"v1.2.0-preview.2", "v1.2.0-preview.3", -1},
		{"v1.2.0-preview.10", "v1.2.0-preview.3", 1},
		{"v1.2.0-preview.3", "v1.2.0", -1},
		{"v1.2.1", "v1.2.0", 1},
		{"v1.5.7.1", "v1.5.7", 1},
		{"v1.5.7.1", "v1.5.7.2", -1},
		{"v1.2.0", "v1.2.0", 0},
		{"v1.6.0(abcdef0)", "v1.6.0", 0},
		{"v1.6.1(1234567)", "v1.6.1", 0},
		{"v1.6.0(abcdef0)", "v1.6.1", -1},
		{"v1.5.7.2(45bd1062)", "v1.5.7.2", 0},
		{"v1.5.7.3(abcdef0)", "v1.5.7.2(45bd1062)", 1},
		{"vsha-5509b17", "v1.5.0", -1},
		{"sha-5509b17", "v1.5.0", -1},
		{"v1.5.0", "vsha-5509b17", 1},
		{"v1.2.9", "v1.5.0", -1},
	}
	for _, test := range tests {
		got := CompareVersions(test.left, test.right)
		if got < 0 {
			got = -1
		} else if got > 0 {
			got = 1
		}
		if got != test.want {
			t.Fatalf("CompareVersions(%q, %q)=%d, want %d", test.left, test.right, got, test.want)
		}
	}
}
