package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Through toolnexus, a request goes to the profile's URL with exactly the profile's headers: TYPESAFE_API_KEY
// never reaches a profile that did not name it, and a "$" inside an expanded value is sent as it is.
func TestBackendSendsOnlyTheProfileHeadersToItsURL(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "ts-secret")
	t.Setenv("JEVX_TEST_TOKEN", "a$b")
	var path, auth, other string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, other = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Other")
		io.WriteString(w, `{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}`)
	}))
	defer s.Close()
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "q"}}
	p := Profile{URL: s.URL + "/custom/v1/systemone", Model: "m", Headers: map[string]string{"X-Other": "Bearer $JEVX_TEST_TOKEN"}}
	if _, _, err := AskRaw(p, "x", qs, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if path != "/custom/v1/systemone" || auth != "" || other != "Bearer a$b" {
		t.Fatalf("path %q, Authorization %q, X-Other %q", path, auth, other)
	}
}

// Retries counts tries, as before: 3 tries on a 503, one on a 400; an error names the profile's URL.
func TestBackendRetries(t *testing.T) {
	var n atomic.Int32
	status := 503
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(status)
		io.WriteString(w, "nope")
	}))
	defer s.Close()
	old := Retries
	defer func() { Retries = old }()
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "q"}}
	p := Profile{URL: s.URL + "/v1/systemone", Model: "m"}
	for _, c := range []struct{ tries, status, want int }{{3, 503, 3}, {1, 503, 1}, {3, 400, 1}} {
		n.Store(0)
		Retries, status = c.tries, c.status
		_, _, err := AskRaw(p, "x", qs, 5*time.Second)
		if err == nil || int(n.Load()) != c.want || !strings.Contains(err.Error(), p.URL) || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("tries %d status %d: %d calls, err %v", c.tries, c.status, n.Load(), err)
		}
	}
}

// A score needs 2..10 levels and a choice at least one option (toolnexus's client-side limits): refused before
// any request.
func TestBackendLimits(t *testing.T) {
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1) }))
	defer s.Close()
	p := Profile{URL: s.URL, Model: "m"}
	qs := map[string]any{"s": map[string]any{"type": "score", "instructions": "q", "criteria": []string{"only"}}}
	if _, _, err := AskRaw(p, "x", qs, 5*time.Second); err == nil || n.Load() != 0 {
		t.Fatalf("a one-level score: err %v, %d calls", err, n.Load())
	}
}
