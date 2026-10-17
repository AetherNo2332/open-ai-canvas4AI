package outbound

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDesktopPrivateHostOptIn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("model-stub")) }))
	defer server.Close()
	policy, err := NewHostPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policy.ValidateURL(server.URL); err == nil {
		t.Fatal("private URL accepted without opt-in")
	}
	policy, err = NewHostPolicy([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policy.ValidateURL(server.URL); err != nil {
		t.Fatal(err)
	}
	response, err := policy.HTTPClient(time.Second).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if _, err = policy.ValidateURL("http://localhost:1234"); err == nil {
		t.Fatal("hostname was implicitly trusted")
	}
	if _, err = policy.ValidateURL("http://127.0.0.1.evil.example"); err == nil {
		t.Fatal("suffix match accepted")
	}
	if _, err = NewHostPolicy([]string{"*.local"}); err == nil {
		t.Fatal("wildcard accepted")
	}
	forbidden, _ := NewHostPolicy([]string{"169.254.169.254"})
	if forbidden != nil {
		if _, err = forbidden.ValidateURL("http://169.254.169.254"); err == nil {
			t.Fatal("metadata address accepted")
		}
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, server.URL, 302) }))
	defer redirect.Close()
	if _, err = policy.HTTPClient(time.Second).Get(redirect.URL); err == nil {
		t.Fatal("redirect accepted")
	}
}
