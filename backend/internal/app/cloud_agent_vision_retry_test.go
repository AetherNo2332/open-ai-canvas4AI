package app

import (
	"errors"
	"testing"
)

func TestClassifyVisionBatchError(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want cloudAgentVisionBatchErrorClass
	}{
		{"too many images", "HTTP 400: too_many_images", cloudAgentVisionBatchCapacity},
		{"context", "context_length_exceeded", cloudAgentVisionBatchCapacity},
		{"payload", "payload too large", cloudAgentVisionBatchCapacity},
		{"auth", "invalid api key", cloudAgentVisionBatchPermanent},
		{"unknown", "provider exploded", cloudAgentVisionBatchPermanent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyCloudAgentVisionBatchError(errors.New(test.err)); got != test.want {
				t.Fatalf("classifyCloudAgentVisionBatchError(%q) = %q, want %q", test.err, got, test.want)
			}
		})
	}
}
