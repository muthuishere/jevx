package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func server(t *testing.T, replies ...func(w http.ResponseWriter)) (*httptest.Server, *int32) {
	var n int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt32(&n, 1) - 1
		replies[min(int(i), len(replies)-1)](w)
	}))
	t.Cleanup(s.Close)
	return s, &n
}

func body(s string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { _, _ = w.Write([]byte(s)) }
}

func status(c int) func(http.ResponseWriter) { return func(w http.ResponseWriter) { w.WriteHeader(c) } }

var noulQ = map[string]any{"q": map[string]any{"type": "noul", "instructions": "Is it?"}}

func TestAskRawValidates(t *testing.T) {
	t.Setenv("JEVX_LEDGER", "off")
	choiceQ := map[string]any{"c": map[string]any{"type": "choice", "instructions": "Which?", "criteria": map[string]string{"a": "A", "b": "B"}}}
	cases := []struct {
		name  string
		qs    map[string]any
		reply string
		want  string // "" = ok, else substring of the error
	}{
		{"ok noul", noulQ, `{"answers":{"q":{"type":"noul","noul":0.9}}}`, ""},
		{"missing answer", noulQ, `{"answers":{"x":{"type":"noul","noul":0.9}}}`, "no answer"},
		{"extra answer", noulQ, `{"answers":{"q":{"type":"noul","noul":0.9},"z":{"type":"noul","noul":0.1}}}`, "unexpected answer"},
		{"noul out of range", noulQ, `{"answers":{"q":{"type":"noul","noul":1.4}}}`, "outside [0,1]"},
		{"probs do not sum", choiceQ, `{"answers":{"c":{"type":"choice","choice":"a","probabilities":{"a":0.9,"b":0.5}}}}`, "sum"},
		{"unknown key", choiceQ, `{"answers":{"c":{"type":"choice","choice":"zz","probabilities":{"a":0.5,"b":0.5}}}}`, "was not offered"},
		{"garbage", noulQ, `not json`, "unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := server(t, body(c.reply))
			_, _, err := AskRaw(Profile{URL: s.URL}, "state", c.qs, 5*time.Second)
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func TestAskRawRetries(t *testing.T) {
	t.Setenv("JEVX_LEDGER", "off")
	s, n := server(t, status(503), status(429), body(`{"answers":{"q":{"type":"noul","noul":0.5}}}`))
	if _, _, err := AskRaw(Profile{URL: s.URL}, "s", noulQ, 5*time.Second); err != nil || *n != 3 {
		t.Fatalf("want success on the 3rd try, got err=%v after %d calls", err, *n)
	}
	s2, n2 := server(t, status(400))
	if _, _, err := AskRaw(Profile{URL: s2.URL}, "s", noulQ, 5*time.Second); err == nil || *n2 != 1 {
		t.Fatalf("a 400 must fail at once, got err=%v after %d calls", err, *n2)
	}
}
