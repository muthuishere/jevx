package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// A profile's type is the API it speaks. Each type has its own request body, reply shape, image support and limits;
// the rest of jevx (questions, verdicts, cache, batch) is the same for all of them.
const (
	TypeTypeSafe   = "typesafe"   // System One: flat {model, state, questions, assets}; TypeSafe's hosted Jev and openjevx
	TypeCloudflare = "cloudflare" // Workers AI /ai/run: the System One body inside a {model, input} envelope
	TypeOpenAI     = "openai"     // OpenAI Decisions API: {model, input, questions: [...]}, answers as a list
)

// TypeInfo describes a profile type for `jevx profile types` and the defaults it brings.
type TypeInfo struct {
	Name    string
	Summary string
	Body    string // the request shape, one line
	Images  bool   // takes images by default (a profile's "images" overrides it)
	Chunk   int    // questions per request the API accepts (0: the global chunk setting)
	URL     string // an example url
}

var Types = []TypeInfo{
	{Name: TypeTypeSafe, Summary: "System One / Jev contract (default): TypeSafe's hosted Jev, openjevx, any System One server",
		Body: `{"model", "state", "questions": {NAME: {type: noul|choice|score, ...}}, "assets": {NAME: image}}`, Images: true,
		URL: "https://api.typesafe.ai/v1/systemone"},
	{Name: TypeCloudflare, Summary: "Cloudflare Workers AI /ai/run: the System One body in an envelope, reply in {result, success}",
		Body: `{"model", "input": {"state", "questions", ...}}`, Images: false,
		URL: "https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT/ai/run"},
	{Name: TypeOpenAI, Summary: "OpenAI Decisions API (/v1/decisions): predicate / choice / score questions as a list",
		Body: `{"model", "input": TEXT | [{role, content: [input_text, input_image]}], "questions": [{name, type, ...}]}`, Images: true, Chunk: 10,
		URL: "https://api.openai.com/v1/decisions"},
}

// TypeNames is "typesafe, cloudflare, openai".
func TypeNames() string {
	var n []string
	for _, t := range Types {
		n = append(n, t.Name)
	}
	return strings.Join(n, ", ")
}

// LookupType returns the type's description, or false for an unknown name.
func LookupType(name string) (TypeInfo, bool) {
	for _, t := range Types {
		if t.Name == name {
			return t, true
		}
	}
	return TypeInfo{}, false
}

// Kind is the profile's type: "type", else the legacy "style", else a Cloudflare /ai/run url, else typesafe.
func (p Profile) Kind() string {
	switch {
	case p.Type != "":
		return p.Type
	case p.Style != "":
		return p.Style
	case strings.Contains(p.URL, "/ai/run"):
		return TypeCloudflare
	}
	return TypeTypeSafe
}

// TakesImages says whether images may be sent to this profile: its "images" setting, else its type's default.
func (p Profile) TakesImages() bool {
	if p.Images != nil {
		return *p.Images
	}
	t, _ := LookupType(p.Kind())
	return t.Images
}

// ---------------------------------------------------------------- OpenAI Decisions API

// openaiQuestions turns System One questions (noul / choice / score, keyed by name) into the Decisions API's list
// (predicate / choice / score, each with its name), in name order.
func openaiQuestions(qs map[string]any) []map[string]any {
	names := make([]string, 0, len(qs))
	for k := range qs {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []map[string]any
	for _, k := range names {
		q, _ := qs[k].(map[string]any)
		instr, _ := q["instructions"].(string)
		o := map[string]any{"name": k, "instructions": instr}
		switch q["type"] {
		case "noul":
			o["type"] = "predicate"
			if c, ok := q["criteria"].(map[string]string); ok && (c["true"] != "" || c["false"] != "") {
				o["instructions"] = fmt.Sprintf("%s\nTrue means: %s\nFalse means: %s", instr, c["true"], c["false"])
			}
		case "choice":
			o["type"] = "choice"
			var cs []map[string]any
			if c, ok := q["criteria"].(map[string]string); ok {
				for _, key := range sortedKeys(c) {
					cs = append(cs, map[string]any{"value": key, "description": c[key]})
				}
			}
			o["choices"] = cs
		case "score":
			o["type"] = "score"
			var lv []map[string]any
			if c, ok := q["criteria"].([]string); ok {
				for _, l := range c {
					lv = append(lv, map[string]any{"label": l})
				}
			}
			o["levels"] = lv
		default:
			o["type"] = q["type"]
		}
		out = append(out, o)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// openaiInput is the Decisions API input: the text alone, or one user message with the text and each image as a
// base64 data URL.
func openaiInput(state string, imgs []Image) any {
	if len(imgs) == 0 {
		return state
	}
	content := []map[string]any{{"type": "input_text", "text": state}}
	for _, im := range imgs {
		content = append(content, map[string]any{"type": "input_image", "image_url": im.DataURL()})
	}
	return []map[string]any{{"role": "user", "content": content}}
}

// openaiAnswers reads a Decisions API reply into System One answers keyed by question name. The reply lists answers
// in question order, each with its name; a list without names is matched by position. A refusal is an error, never
// a "no".
func openaiAnswers(raw []byte, qs map[string]any) ([]byte, error) {
	var r struct {
		Model   string          `json:"model"`
		Answers json.RawMessage `json:"answers"`
		Usage   map[string]any  `json:"usage"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("unreadable answer: %v", err)
	}
	var list []oaAns
	if json.Unmarshal(r.Answers, &list) != nil {
		var m map[string]oaAns
		if err := json.Unmarshal(r.Answers, &m); err != nil {
			return nil, fmt.Errorf("unreadable answer: no answers list")
		}
		for k, a := range m {
			if a.Name == "" {
				a.Name = k
			}
			list = append(list, a)
		}
	}
	names := make([]string, 0, len(qs))
	for k := range qs {
		names = append(names, k)
	}
	sort.Strings(names)
	out := map[string]Answer{}
	for i, a := range list {
		name := a.Name
		if name == "" && i < len(names) {
			name = names[i]
		}
		if a.Type == "refusal" {
			return nil, fmt.Errorf("the model refused to answer %q", name)
		}
		out[name] = a.answer()
	}
	b, _ := json.Marshal(map[string]any{"model": r.Model, "answers": out, "usage": r.Usage})
	return b, nil
}

// oaAns is one Decisions API answer, read leniently: a choice's value may be a string or a boolean, and its
// probabilities a map or a list of {value, probability}.
type oaAns struct {
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	Probability   *float64        `json:"probability"`
	Value         any             `json:"value"`
	Choice        any             `json:"choice"`
	Score         *float64        `json:"score"`
	Confidence    *float64        `json:"confidence"`
	Probabilities json.RawMessage `json:"probabilities"`
}

func (a oaAns) answer() Answer {
	var out Answer
	probs := map[string]float64{}
	var m map[string]float64
	if json.Unmarshal(a.Probabilities, &m) == nil {
		probs = m
	} else {
		var l []struct {
			Value       any     `json:"value"`
			Label       any     `json:"label"`
			Probability float64 `json:"probability"`
		}
		if json.Unmarshal(a.Probabilities, &l) == nil {
			for _, x := range l {
				k := x.Value
				if k == nil {
					k = x.Label
				}
				probs[fmt.Sprint(k)] = x.Probability
			}
		}
	}
	switch a.Type {
	case "predicate", "noul":
		out.Type, out.Probability = "noul", a.Probability
	case "choice":
		out.Type = "choice"
		v := a.Value
		if v == nil {
			v = a.Choice
		}
		if v != nil {
			out.Choice = fmt.Sprint(v)
		}
		if len(probs) > 0 {
			out.Probabilities = probs
		}
		out.Confidence = a.Confidence
		if out.Confidence == nil {
			if pr, ok := probs[out.Choice]; ok {
				out.Confidence = &pr
			}
		}
	case "score":
		out.Type, out.Score, out.Confidence = "score", a.Score, a.Confidence
	default:
		out.Type, out.Probability = a.Type, a.Probability
	}
	return out
}
