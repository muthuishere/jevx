package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Native batch: many states in ONE request, for a System One server that serves POST {url}/batch (openjevx does;
// TypeSafe's public API has none, and neither does toolnexus's wire, so this one call is jevx's own). A profile opts
// in with "batch": N, the most states per request. The wire is the same shape self-hosted System One servers use:
//
//	request  {"requests": [{"state": ..., "questions": {...}, "model": "...", "assets": {...}}, ...]}
//	reply    {"model": "...", "results": [<a /v1/systemone reply> | {"error": "..."}, ...], "usage": {...}}
//
// A server without the route (404, 405, 501) gets ErrNoBatch, and the caller asks one state at a time instead.

// ErrNoBatch says the endpoint has no native batch route.
var ErrNoBatch = errors.New("no native batch endpoint")

// MaxBatch is the most states one native batch request carries, whatever the profile says.
const MaxBatch = 50

// BatchURL is where native batches go: the profile's batch_url, else its url + "/batch".
func (p Profile) BatchURL() string {
	if p.BatchEndpoint != "" {
		return p.BatchEndpoint
	}
	return strings.TrimRight(p.URL, "/") + "/batch"
}

// BatchSize is how many states one native batch request carries: 0 when the profile does not batch natively (only
// the typesafe type does).
func (p Profile) BatchSize() int {
	if p.Batch <= 1 || p.Kind() != TypeTypeSafe {
		return 0
	}
	return min(p.Batch, MaxBatch)
}

// BatchResult is one state's outcome in a native batch.
type BatchResult struct {
	Answers map[string]Answer
	Model   string
	Err     error
}

// AskBatch asks the same questions about every state in one request. Each state may carry its own images (already
// named in the state). Answers are validated per state, so one bad result fails only its own state; an error for
// the whole request (transport, HTTP status, an unreadable reply) is returned as err.
func AskBatch(p Profile, states []string, imgs [][]Image, qs map[string]any, timeout time.Duration) ([]BatchResult, error) {
	if MissingEnv(p.Norm()) != "" {
		return nil, ErrNeedKey
	}
	p = p.Expanded()
	hosted, redacted, nImgs := Hosted(p), 0, 0
	reqs := make([]map[string]any, len(states))
	for i, st := range states {
		sendState, sendQs := st, qs
		if hosted {
			var kinds map[string]int
			sendState, sendQs, kinds = redactKinds(st, qs)
			for _, n := range kinds {
				redacted += n
			}
			NoteRedacted(kinds)
		}
		r := map[string]any{"state": sendState, "questions": sendQs, "model": p.Model}
		if i < len(imgs) && len(imgs[i]) > 0 {
			assets := map[string]any{}
			for _, im := range imgs[i] {
				assets[im.Name] = im.asset()
			}
			r["assets"] = assets
			nImgs += len(imgs[i])
		}
		reqs[i] = r
	}
	if nImgs > 0 {
		h := map[string]string{ImagesHeader: "true"}
		for k, v := range p.Headers {
			h[k] = v
		}
		p.Headers = h
	}
	body, _ := json.Marshal(map[string]any{"requests": reqs})
	bp := p
	bp.URL = p.BatchURL()
	t0 := time.Now()
	var raw []byte
	var status int
	var err error
	for try := 0; try < max(1, Retries); try++ {
		if try > 0 {
			time.Sleep(time.Duration(try*try) * 500 * time.Millisecond)
		}
		var retry bool
		raw, status, retry, err = postStatus(bp, body, timeout)
		if err == nil || !retry {
			break
		}
	}
	if status == 404 || status == 405 || status == 501 {
		return nil, ErrNoBatch
	}
	var out struct {
		Model   string            `json:"model"`
		Results []json.RawMessage `json:"results"`
		Usage   map[string]any    `json:"usage"`
	}
	if err == nil {
		if e := json.Unmarshal(raw, &out); e != nil {
			err = fmt.Errorf("unreadable answer: %v", e)
		} else if len(out.Results) != len(states) {
			err = fmt.Errorf("invalid answer: %d results for %d states", len(out.Results), len(states))
		}
	}
	ledgerBatch(bp, qs, out.Model, out.Usage, time.Since(t0), err, redacted, nImgs, len(states))
	if err != nil {
		return nil, err
	}
	res := make([]BatchResult, len(states))
	for i, r := range out.Results {
		var one struct {
			Model   string            `json:"model"`
			Answers map[string]Answer `json:"answers"`
			Error   any               `json:"error"`
		}
		if e := json.Unmarshal(r, &one); e != nil {
			res[i].Err = fmt.Errorf("unreadable answer: %v", e)
			continue
		}
		if one.Error != nil {
			res[i].Err = fmt.Errorf("%s: %v", bp.URL, one.Error)
			continue
		}
		res[i].Model = one.Model
		if res[i].Model == "" {
			res[i].Model = out.Model
		}
		if e := validate(qs, one.Answers); e != nil {
			res[i].Err = e
			continue
		}
		res[i].Answers = one.Answers
	}
	return res, nil
}

// postStatus is post with the HTTP status: a native batch tells "no such route" apart from a failure.
func postStatus(p Profile, body []byte, timeout time.Duration) ([]byte, int, bool, error) {
	raw, retry, err := post(p, body, timeout)
	if err != nil {
		var status int
		if _, rest, ok := strings.Cut(err.Error(), ": HTTP "); ok {
			fmt.Sscanf(rest, "%d", &status)
		}
		return nil, status, retry, err
	}
	return raw, 200, false, nil
}
