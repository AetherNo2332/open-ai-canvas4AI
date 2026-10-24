package assets

import (
	"encoding/binary"
	"testing"
)

func TestAudioContentTypeDetection(t *testing.T) {
	m4a := make([]byte, 32)
	binary.BigEndian.PutUint32(m4a, 32)
	copy(m4a[4:], "ftypM4A ")
	copy(m4a[16:], "M4A isomiso2")
	flac := make([]byte, 42)
	copy(flac, "fLaC")
	flac[7] = 34
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"audio.m4a", m4a, "audio/mp4"}, {"voice.flac", flac, "audio/flac"},
	} {
		if got := DetectedContentType(tc.data, "application/octet-stream", tc.name); got != tc.want {
			t.Fatalf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
	if !InlineMediaType("audio/x-flac") {
		t.Fatal("FLAC alias must use passive media delivery")
	}
	for _, data := range [][]byte{[]byte("<html><script>alert(1)</script>"), []byte("<svg onload='alert(1)'/>"), m4a[:12]} {
		if got := DetectedContentType(data, "audio/mp4", "fake.m4a"); InlineMediaType(got) {
			t.Fatalf("unrecognized content became inline media: %s", got)
		}
	}
}
