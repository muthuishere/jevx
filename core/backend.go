package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tn "github.com/muthuishere/toolnexus/golang"
)

// jevx is the stateful front end; toolnexus is the stateless library under it (toolnexus ADR 0038). A jevx profile
// resolves to a toolnexus ClassifierOptions and that is all that reaches toolnexus: the backend (the profile's
// type), the endpoint, the model, the headers and what the endpoint supports. toolnexus owns the wire: the request
// for each backend (System One, Cloudflare's /ai/run envelope), images as assets with TypeSafe's opt-in header and
// size limits, retries with Retry-After, and fail-closed reply validation. jevx keeps its state: profiles, redaction
// before a hosted call, the answer cache, verdicts and the ledger.

// noKeyEnv switches off toolnexus's API-key variable: jevx sends credentials only through a profile's headers, so a
// key is never sent to a profile that did not name it.
const noKeyEnv = "JEVX_NO_KEY_ENV"

// Options is the toolnexus ClassifierOptions profile p resolves to. p is normalised but not expanded: toolnexus
// expands $VAR in headers and ${VAR} in the URL at call time, and never logs them.
func (p Profile) Options(timeout time.Duration) tn.ClassifierOptions {
	kind := p.Kind()
	base := strings.TrimRight(p.URL, "/")
	if kind != TypeCloudflare { // toolnexus posts to {base}/systemone; the /ai/run URL is used as it is
		base = strings.TrimSuffix(base, "/systemone")
	}
	retries := Retries - 1 // jevx counts tries, toolnexus counts retries; -1 is its explicit zero
	if retries <= 0 {
		retries = -1
	}
	o := tn.ClassifierOptions{
		Backend: tn.ClassifierBackend(kind), BaseURL: base, Model: p.Model, APIKeyEnv: noKeyEnv, Headers: p.Headers,
		Timeout: timeout, Retries: retries, RetryableStatuses: otherServerErrors,
	}
	if p.Images != nil {
		o.Supports = &tn.ClassifierSupports{Images: *p.Images}
	} else if kind == TypeCustom {
		o.Supports = &tn.ClassifierSupports{} // a self-hosted server takes images only when the profile says so
	}
	return o
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

// evaluate asks one state (text, with its images when there are any) through toolnexus and returns the validated
// answers and the reply in the System One shape.
func evaluate(p Profile, state string, imgs []Image, wireQs map[string]any, timeout time.Duration) (map[string]Answer, []byte, tn.Decision, error) {
	questions := map[string]tn.Question{}
	for k, v := range wireQs {
		q, err := tnQuestion(v)
		if err != nil {
			return nil, nil, tn.Decision{}, fmt.Errorf("question %q: %v", k, err)
		}
		questions[k] = q
	}
	c, err := tn.CreateClassifier(p.Options(timeout))
	if err != nil {
		return nil, nil, tn.Decision{}, errors.New(strings.TrimPrefix(err.Error(), "classifier: "))
	}
	d, err := c.Evaluate(context.Background(), ImageState(state, imgs), questions)
	if err == nil {
		err = tn.ValidateDecision(questions, d)
	}
	if err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return nil, nil, d, fmt.Errorf("unreadable answer: %v", err)
		}
		return nil, nil, d, errors.New(strings.TrimPrefix(err.Error(), "classifier: "))
	}
	ans := answers(d)
	raw, _ := json.Marshal(map[string]any{"model": d.Model, "answers": ans, "usage": d.Usage})
	return ans, raw, d, nil
}

// answers turns a toolnexus Decision into jevx's answers.
func answers(d tn.Decision) map[string]Answer {
	out := map[string]Answer{}
	for k, a := range d.Answers {
		switch x := a.(type) {
		case tn.NoulAnswer:
			out[k] = Answer{Type: "noul", Noul: &x.Noul}
		case tn.ChoiceAnswer:
			out[k] = Answer{Type: "choice", Choice: x.Choice, Probabilities: x.Probabilities, Confidence: &x.Confidence}
		case tn.ScoreAnswer:
			out[k] = Answer{Type: "score", Score: &x.Score, Probabilities: x.Probabilities, Confidence: &x.Confidence}
		default:
			out[k] = Answer{Type: a.AnswerType()}
		}
	}
	return out
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
	case nil, "":
		return nil, errors.New("no question type (want noul, choice or score)")
	}
	t, _ := m["type"].(string)
	return tn.RawQuestion{Type: t, Instructions: instr, Criteria: m["criteria"]}, nil // e.g. Jev 1.14 "box", sent as written
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
