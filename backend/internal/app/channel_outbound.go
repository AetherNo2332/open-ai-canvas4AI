package app

import (
	"net/http"
	"net/url"
	"time"
)

// ValidateChannelOutboundURL keeps all provider channels on the server-side
// outbound policy. Desktop uses an explicit owner-managed exact-host policy.
func (s *Service) ValidateChannelOutboundURL(rawURL string) (*url.URL, error) {
	if s.desktopMode {
		return s.desktopHostPolicy().ValidateURL(rawURL)
	}
	return ValidateOutboundURL(rawURL)
}

func (s *Service) OutboundHTTPClientForChannel(timeout time.Duration, _ *url.URL) *http.Client {
	if s.desktopMode {
		return s.desktopHostPolicy().HTTPClient(timeout)
	}
	return OutboundHTTPClient(timeout)
}
