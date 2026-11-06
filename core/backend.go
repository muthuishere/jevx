package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tn "github.com/muthuishere/toolnexus/golang"
)

// The request path runs on toolnexus's System One Classifier (SPEC §8B): its canonical request body, ${VAR} header
// expansion at call time, retries with Retry-After, and an auth error that never echoes a credential. jevx keeps
// what is its own: profiles, redaction before a hosted call, the cache, verdicts, validation and the ledger.
//
// toolnexus speaks one wire, POST {base}/systemone. The other profile types use its two documented seams:
// BodyTransform reshapes the request (Cloudflare's {model, input} envelope, the OpenAI Decisions body, image assets)
// and the injectable HTTP client sends it to the profile's own URL and turns the reply back into a System One one.

// noKeyEnv disables toolnexus's API-key variable: jevx sends credentials only through a profile's own headers, so
// TYPESAFE_API_KEY never reaches a profile that did not ask for it.
const noKeyEnv = "JEVX_TOOLNEXUS_NO_KEY_ENV"

// evaluate sends one request for profile p (already expanded; headers still raw) and returns the System One reply
// body. state and wireQs are what is sent (redacted for a hosted endpoint).
func evaluate(p Profile, headers map[string]string, kind, state string, imgs []Image, qs, wireQs map[string]any, timeout time.Duration) ([]byte, error) {
	questions := map[string]tn.Question{}
	for k, v := range wireQs {
		q, err := tnQuestion(v)
		if err != nil {
			return nil, fmt.Errorf("question %q: %v", k, err)
		}
		questions[k] = q
	}
	hdr := map[string]string{}
	if len(imgs) > 0 && kind == TypeTypeSafe { // TypeSafe's opt-in for image input; a header the profile sets itself wins
		hdr[ImagesHeader] = "true"
	}
	for k, v := range headers {
		hdr[k] = v
	}
	base := strings.TrimSuffix(strings.TrimRight(p.URL, "/"), "/systemone")
	endpoint := base + "/systemone" // what toolnexus addresses; the transport sends to p.URL
	w := &wire{url: p.URL, kind: kind, qs: qs}
	retries := Retries - 1 // jevx counts tries, toolnexus counts retries; -1 is its explicit zero
	if retries <= 0 {
		retries = -1
	}
	c, err := tn.CreateClassifier(tn.ClassifierOptions{
		BaseURL: base, Model: p.Model, APIKeyEnv: noKeyEnv, Headers: hdr, Timeout: timeout,
		Retries: retries, RetryableStatuses: otherServerErrors,
		HTTPClient: &http.Client{Transport: w},
		BodyTransform: func(b map[string]any) map[string]any {
			return requestBody(kind, b, state, imgs, wireQs)
		},
	})
	if err != nil {
		return nil, err
	}
	if _, err := c.Evaluate(context.Background(), state, questions); err != nil {
		if w.err != nil {
			return nil, w.err
		}
		if w.raw != nil { // a 2xx reply arrived, so it is the reply that did not decode
			return nil, fmt.Errorf("unreadable answer: %v", err)
		}
		msg := strings.ReplaceAll(err.Error(), endpoint, p.URL)
		return nil, fmt.Errorf("%s", strings.TrimPrefix(strings.TrimPrefix(msg, "classifier: "), "POST "))
	}
	return w.raw, nil
}

// otherServerErrors widens toolnexus's retryable set (408, 429, 500, 502, 503, 504, 529) to every 5xx, as jevx
// always retried (a Cloudflare-fronted origin answers 520-527).
var otherServerErrors = func() []int {
	var s []int
	for c := 500; c < 600; c++ {
		s = append(s, c)
	}
	return s
}()

// requestBody reshapes toolnexus's System One body for the profile type.
func requestBody(kind string, b map[string]any, state string, imgs []Image, wireQs map[string]any) map[string]any {
	if kind == TypeOpenAI {
		return map[string]any{"model": b["model"], "input": openaiInput(state, imgs), "questions": openaiQuestions(wireQs)}
	}
	if len(imgs) > 0 {
		assets := map[string]any{}
		for _, im := range imgs {
			assets[im.Name] = im.asset()
		}
		b["assets"] = assets
	}
	if kind == TypeCloudflare {
		model := b["model"]
		delete(b, "model")
		return map[string]any{"model": model, "input": b}
	}
	return b
}

// tnQuestion turns a System One question (as jevx builds it, or as JSON decoded it) into a toolnexus one.
func tnQuestion(v any) (tn.Question, error) {
	m, _ := v.(map[string]any)
	instr, _ := m["instructions"].(string)
	switch m["type"] {
	case "noul":
		q := tn.NoulQuestion{Instructions: instr}
		if c := stringMap(m["criteria"]); c != nil {
			q.Criteria = &tn.NoulCriteria{True: c["true"], False: c["false"]}
		}
		return q, nil
	case "choice":
		return tn.ChoiceQuestion{Instructions: instr, Criteria: stringMap(m["criteria"])}, nil
	case "score":
		var lv []string
		switch c := m["criteria"].(type) {
		case []string:
			lv = c
		case []any:
			for _, x := range c {
				lv = append(lv, fmt.Sprint(x))
			}
		}
		return tn.ScoreQuestion{Instructions: instr, Criteria: lv}, nil
	}
	return nil, fmt.Errorf("unknown question type %v (want noul, choice or score)", m["type"])
}

func stringMap(v any) map[string]string {
	switch c := v.(type) {
	case map[string]string:
		return c
	case map[string]any:
		out := map[string]string{}
		for k, x := range c {
			out[k] = fmt.Sprint(x)
		}
		return out
	}
	return nil
}

// wire is the HTTP transport toolnexus sends through: it posts to the profile's own URL and reads the reply of the
// profile type back into a System One reply. A reply that is not an answer (a failed Cloudflare envelope, a run
// that has not completed, an OpenAI refusal) becomes a 422, which toolnexus does not retry, and its message is kept
// for the caller.
type wire struct {
	url, kind string
	qs        map[string]any
	raw       []byte // the last answer, in the System One shape
	err       error  // why the last reply was not an answer
}

func (w *wire) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(w.url)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL, req.Host = u, u.Host
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil || resp.StatusCode/100 != 2 {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	w.err = nil
	switch w.kind {
	case TypeCloudflare:
		body, w.err = cloudflareResult(w.url, body)
	case TypeOpenAI:
		if body, err = openaiAnswers(body, w.qs); err != nil {
			w.err = fmt.Errorf("%s: %v", w.url, err)
		}
	}
	if w.err != nil {
		resp.StatusCode, resp.Status = http.StatusUnprocessableEntity, "422 Unprocessable Entity"
		body = []byte(w.err.Error())
	} else {
		w.raw = body
	}
	resp.Body, resp.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	resp.Header.Del("Content-Length")
	return resp, nil
}

// cloudflareResult unwraps Cloudflare's {"result": ..., "success": ...} envelope (Cloudflare REST 2026-10). The
// Workers AI /ai/run reply nests once more: {"state": "Completed", "result": {model, answers}}. Any other state
// (queued, running, failed) is not an answer; it says so instead of reading it as one.
func cloudflareResult(u string, raw []byte) ([]byte, error) {
	var env struct {
		Result  json.RawMessage `json:"result"`
		Success bool            `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if e := json.Unmarshal(raw, &env); e != nil || !env.Success {
		msg := "cloudflare envelope: unknown error"
		if e == nil && len(env.Errors) > 0 {
			msg = env.Errors[0].Message
		} else if e == nil {
			msg = strings.TrimSpace(string(raw[:min(200, len(raw))]))
		}
		return nil, fmt.Errorf("%s: %s", u, msg)
	}
	var inner struct {
		State  string          `json:"state"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(env.Result, &inner) == nil && inner.State != "" {
		if inner.State != "Completed" {
			return nil, fmt.Errorf("%s: cloudflare run state %q, not Completed", u, inner.State)
		}
		return inner.Result, nil
	}
	return env.Result, nil
}
