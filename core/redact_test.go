package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScrubRemovesSecretsEmailsPhones(t *testing.T) {
	t.Setenv("MYAPP_API_KEY", "liveValue_0123456789abcdef")
	in := "key liveValue_0123456789abcdef; mail ops@example.com; call +91 98765 43210 or 9876543210; " +
		"gh ghp_abcdefghijklmnopqrstuvwxyz0123; db postgres://u:pw1234@host/db; password: hunter2hunter2xyz"
	out, n := Scrub(in)
	for _, leak := range []string{"liveValue_0123456789abcdef", "ops@example.com", "98765 43210", "9876543210",
		"ghp_abcdefghijklmnopqrstuvwxyz0123", "u:pw1234@", "hunter2hunter2xyz"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q in %q", leak, out)
		}
	}
	if n < 7 {
		t.Fatalf("redacted %d items, want >= 7: %q", n, out)
	}
}

func TestScrubKeepsOrdinaryText(t *testing.T) {
	in := "2026-10-03 10:15:49 STATUS: OK, ₹2,768 of ₹7,000, 142/142 ok, post 2106092289173213444, port 7714, v0.5.3-2"
	out, n := Scrub(in)
	if out != in || n != 0 {
		t.Fatalf("changed ordinary text (%d): %q", n, out)
	}
}

func TestHosted(t *testing.T) {
	for url, want := range map[string]bool{
		"https://jev.typesafe.ai/v1/system-one": true,
		"http://127.0.0.1:8080/x":               false,
		"http://localhost:8080/x":               false,
		"http://192.168.29.10:8000/x":           false,
		"http://10.0.0.5/x":                     false,
		"http://100.101.1.2/x":                  false, // tailscale / CGNAT
		"http://box.local/x":                    false,
	} {
		if got := Hosted(Profile{URL: url}); got != want {
			t.Errorf("Hosted(%s) = %v, want %v", url, got, want)
		}
	}
}

// A hosted call must leave scrubbed, and the ledger must carry profile + caller + counts but never the payload.
func TestHostedCallIsScrubbedAndLedgered(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("JEVX_LEDGER", "")
	t.Setenv("JEVX_CALLER", "my-agent")
	t.Setenv("SOME_SECRET_TOKEN", "s3cr3t-value-abcdefgh")
	ForceHosted, LedgerOn = true, true
	defer func() { ForceHosted = false }()
	ActiveProfile = "jev"
	var notice bytes.Buffer
	RedactNotice, redactNotice = &notice, sync.Once{}
	defer func() { RedactNotice = os.Stderr }()

	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}`))
	}))
	defer srv.Close()

	state := "agent STATE.md: token s3cr3t-value-abcdefgh, mail me at a.b@example.org"
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "is +1 (415) 555-0100 a lead?"}}
	if _, _, err := AskRaw(Profile{URL: srv.URL, Model: "m"}, state, qs, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"s3cr3t-value-abcdefgh", "a.b@example.org", "555-0100"} {
		if strings.Contains(string(sent), leak) {
			t.Fatalf("request body leaked %q: %s", leak, sent)
		}
	}

	// the caller is told, without the values, so "does this contain a secret?" is not silently unsure
	if n := notice.String(); !strings.Contains(n, "redacted 1 email-shaped (user@host), 1 phone number, 1 secret before") || strings.Contains(n, "s3cr3t") || strings.Contains(n, "example.org") {
		t.Fatalf("redaction notice: %q", n)
	}

	b, err := os.ReadFile(filepath.Join(home, ".local/share/jevx/calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &row); err != nil {
		t.Fatal(err)
	}
	if row["profile"] != "jev" || row["caller"] != "my-agent" || row["questions"] != float64(1) ||
		row["hosted"] != true || row["redacted"].(float64) < 3 || row["ts"] == "" {
		t.Fatalf("ledger row missing fields: %v", row)
	}
	for _, leak := range []string{"STATE.md", "s3cr3t", "example.org", "415", "lead?"} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("ledger holds payload %q: %s", leak, b)
		}
	}
}

// A local (non-hosted) endpoint gets the payload as is: the owner's own model may see everything.
func TestLocalCallIsNotScrubbed(t *testing.T) {
	setHome(t, t.TempDir())
	ForceHosted = false
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}`))
	}))
	defer srv.Close()
	_, _, err := AskRaw(Profile{URL: srv.URL, Model: "m"}, "mail a.b@example.org", map[string]any{"a": map[string]any{"type": "noul"}}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sent), "a.b@example.org") {
		t.Fatalf("local call was scrubbed: %s", sent)
	}
}

// The notice names what was redacted, by category, so a reader can tell a password from a user@host that only looks
// like an email (the email rule also matches user@host in a connection URL).
func TestRedactionNamesTheCategory(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"DATABASE_URL=postgres://app@db.internal:5432/app", "1 email-shaped (user@host)"},
		{"DATABASE_URL=postgres://app:Pr0d-p4ss@db.internal:5432/app", "1 password (in a URL)"},
		{"key AKIAIOSFODNN7EXAMPLE and mail ops@example.org", "1 AWS key, 1 email-shaped (user@host)"},
		{"LOG_LEVEL=debug", ""},
	} {
		out, _ := Scrub(c.in)
		if got := RedactionSummary(RedactedKinds(c.in, out)); got != c.want {
			t.Errorf("%q: summary %q, want %q", c.in, got, c.want)
		}
	}
	// a marker already in the text is not counted again
	if k := RedactedKinds("[REDACTED:email] x", "[REDACTED:email] x"); len(k) != 0 {
		t.Errorf("pre-existing marker counted: %v", k)
	}
}

// Redactions reports what a call would scrub: something for a hosted endpoint, nothing for a local one (the user's own
// machine sees the raw text). Unknown hosts fail safe as hosted.
func TestRedactionsHostedOnly(t *testing.T) {
	state := "db password=hunter2hunter2 for ops@example.com"
	qs := map[string]any{"x": NoulQ(Profile{}, "Is this a leak?")}
	if k := Redactions(Profile{URL: "https://api.example.com/v1/systemone"}, state, qs); len(k) == 0 {
		t.Fatal("a hosted endpoint must redact the password and the email")
	}
	for _, local := range []string{"http://127.0.0.1:21160/v1/systemone", "http://localhost:21160/v1/systemone"} {
		if k := Redactions(Profile{URL: local}, state, qs); len(k) != 0 {
			t.Fatalf("%s is local, nothing is redacted: %v", local, k)
		}
	}
	if k := Redactions(Profile{URL: "not a url"}, state, qs); len(k) == 0 {
		t.Fatal("an unparsable URL fails safe: treated as hosted")
	}
}
