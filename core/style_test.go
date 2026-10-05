package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The cloudflare style wraps the request as {model, input} and unwraps {result, success}; a failed envelope is an
// error carrying Cloudflare's message, never an answer.
func TestCloudflareStyle(t *testing.T) {
	var got map[string]any
	ok, nested, state := true, false, "Completed"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = nil
		_ = json.Unmarshal(b, &got)
		if !ok {
			io.WriteString(w, `{"success":false,"errors":[{"code":2021,"message":"Insufficient balance"}],"result":null}`)
			return
		}
		if nested {
			io.WriteString(w, `{"success":true,"errors":[],"result":{"state":"`+state+`","result":{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}}}`)
			return
		}
		io.WriteString(w, `{"success":true,"errors":[],"result":{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}}`)
	}))
	defer s.Close()
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "q"}}
	for _, p := range []Profile{{URL: s.URL + "/x", Model: "typesafe/jev", Style: "cloudflare"}, {URL: s.URL + "/client/v4/accounts/1/ai/run", Model: "typesafe/jev"}} {
		ok = true
		ans, _, err := AskRaw(p, "item", qs, 5*time.Second)
		if err != nil || ans["a"].P() != 0.9 {
			t.Fatalf("%s: %v %+v", p.URL, err, ans)
		}
		in, _ := got["input"].(map[string]any)
		if got["model"] != "typesafe/jev" || in == nil || in["state"] != "item" || in["model"] != nil {
			t.Fatalf("%s: envelope %v", p.URL, got)
		}
		ok = false
		if _, _, err := AskRaw(p, "item", qs, 5*time.Second); err == nil || !strings.Contains(err.Error(), "Insufficient balance") {
			t.Fatalf("%s: want the envelope error, got %v", p.URL, err)
		}
	}
	// The real Workers AI reply nests {state, result}; Completed is unwrapped, anything else is an error.
	p := Profile{URL: s.URL + "/x", Model: "typesafe/jev", Style: "cloudflare"}
	ok, nested = true, true
	if ans, _, err := AskRaw(p, "item", qs, 5*time.Second); err != nil || ans["a"].P() != 0.9 {
		t.Fatalf("nested Completed: %v %+v", err, ans)
	}
	state = "Queued"
	if _, _, err := AskRaw(p, "item", qs, 5*time.Second); err == nil || !strings.Contains(err.Error(), "Queued") {
		t.Fatalf("nested Queued: want an error naming the state, got %v", err)
	}
	nested, state = false, "Completed"
	// The default style stays the flat System One body.
	ok = true
	if _, _, err := AskRaw(Profile{URL: s.URL + "/v1/systemone", Model: "m"}, "item", qs, 5*time.Second); err == nil {
		t.Fatal("a flat-style call accepted an envelope reply")
	}
	if got["state"] != "item" || got["input"] != nil {
		t.Fatalf("flat body: %v", got)
	}
}
