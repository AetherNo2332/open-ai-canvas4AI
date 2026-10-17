package platform

import (
	"fmt"
	"testing"
)

func TestResolveDirectorDeskPrevisMode(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		users []string
		bp    int
		user  string
		want  string
	}{
		{"on applies to everyone", DirectorDeskPrevisOn, nil, 0, "user-1", DirectorDeskPrevisOn},
		{"on applies even to an empty user", DirectorDeskPrevisOn, nil, 0, "", DirectorDeskPrevisOn},
		{"off applies to everyone even for listed canary users", DirectorDeskPrevisOff, []string{"user-1"}, 10000, "user-1", DirectorDeskPrevisOff},
		{"invalid mode fails closed", "weird", []string{"user-1"}, 10000, "user-1", DirectorDeskPrevisOff},
		{"empty mode fails closed", "", nil, 10000, "user-1", DirectorDeskPrevisOff},
		{"canary allowlist user gets canary", DirectorDeskPrevisCanary, []string{"internal-1"}, 0, "internal-1", DirectorDeskPrevisCanary},
		{"allowlist entries are trimmed before matching", DirectorDeskPrevisCanary, []string{"  internal-2 "}, 0, "internal-2", DirectorDeskPrevisCanary},
		{"unlisted user with zero ratio stays off", DirectorDeskPrevisCanary, nil, 0, "user-2", DirectorDeskPrevisOff},
		{"full ratio canaries everyone with a user id", DirectorDeskPrevisCanary, nil, 10000, "anyone", DirectorDeskPrevisCanary},
		{"empty user never enters the ratio bucket", DirectorDeskPrevisCanary, nil, 10000, "", DirectorDeskPrevisOff},
		{"negative ratio clamps to zero", DirectorDeskPrevisCanary, nil, -5, "user-3", DirectorDeskPrevisOff},
		{"ratio above 10000 clamps to full", DirectorDeskPrevisCanary, nil, 20000, "user-4", DirectorDeskPrevisCanary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveDirectorDeskPrevisMode(tc.mode, tc.users, tc.bp, tc.user)
			if got != tc.want {
				t.Fatalf("ResolveDirectorDeskPrevisMode(%q, %v, %d, %q) = %q, want %q", tc.mode, tc.users, tc.bp, tc.user, got, tc.want)
			}
		})
	}
}

func TestDirectorDeskPrevisCanaryBucketIsStableAndSplitting(t *testing.T) {
	users := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		users = append(users, fmt.Sprintf("user-%d", i))
	}
	first := make(map[string]string, len(users))
	for _, u := range users {
		first[u] = ResolveDirectorDeskPrevisMode(DirectorDeskPrevisCanary, nil, 5000, u)
		again := ResolveDirectorDeskPrevisMode(DirectorDeskPrevisCanary, nil, 5000, u)
		if again != first[u] {
			t.Fatalf("canary bucket is not stable for %s: %s -> %s", u, first[u], again)
		}
	}
	canary, off := 0, 0
	for _, u := range users {
		switch first[u] {
		case DirectorDeskPrevisCanary:
			canary++
		case DirectorDeskPrevisOff:
			off++
		default:
			t.Fatalf("unexpected mode %q for %s", first[u], u)
		}
	}
	if canary == 0 || off == 0 {
		t.Fatalf("a 5000bp ratio should split the fixed 24-user set, got canary=%d off=%d", canary, off)
	}
}