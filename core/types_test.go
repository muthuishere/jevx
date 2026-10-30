package core

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pngBytes is the smallest valid PNG header jevx's sniffing accepts.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func testImage(t *testing.T) Image {
	t.Helper()
	f := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(f, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	imgs, err := LoadImages([]string{f})
	if err != nil {
		t.Fatal(err)
	}
	return imgs[0]
}

func TestKindAndImages(t *testing.T) {
	for _, c := range []struct {
		p    Profile
		kind string
		img  bool
	}{
		{Profile{URL: "https://api.typesafe.ai/v1/systemone"}, TypeTypeSafe, true},
		{Profile{URL: "https://x/client/v4/accounts/1/ai/run"}, TypeCloudflare, false},
		{Profile{URL: "https://x", Style: "cloudflare"}.Norm(), TypeCloudflare, false},
		{Profile{URL: "https://x", Type: TypeOpenAI}, TypeOpenAI, true},
		{Profile{URL: "https://x", Type: TypeCloudflare, Images: bp(true)}, TypeCloudflare, true},
		{Profile{URL: "https://x", Images: bp(false)}, TypeTypeSafe, false},
	} {
		if c.p.Kind() != c.kind || c.p.TakesImages() != c.img {
			t.Errorf("%+v: kind %s images %v, want %s %v", c.p, c.p.Kind(), c.p.TakesImages(), c.kind, c.img)
		}
	}
	if n := (Config{}).Settings(Profile{Type: TypeOpenAI}).Chunk; *n != 10 {
		t.Errorf("openai chunk %d, want 10", *n)
	}
	if n := (Config{}).Settings(Profile{}).Chunk; *n != 32 {
		t.Errorf("typesafe chunk %d, want 32", *n)
	}
}

func TestLoadImageRejectsNonImages(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.png")
	_ = os.WriteFile(f, []byte("hello"), 0o644)
	if _, err := LoadImage(f); err == nil || !strings.Contains(err.Error(), "not a PNG or JPEG") {
		t.Fatalf("want a not-an-image error, got %v", err)
	}
}

func TestWithImageRefs(t *testing.T) {
	im := Image{Name: "image1"}
	ts := Profile{}
	if got := WithImageRefs(ts, "is this broken?", []Image{im}); got != "is this broken?\nImage: image1" {
		t.Errorf("text: %q", got)
	}
	if got := WithImageRefs(ts, "", []Image{im, {Name: "image2"}}); got != "Images: image1, image2" {
		t.Errorf("empty: %q", got)
	}
	if got := WithImageRefs(ts, `{"a":1}`, []Image{im}); got != `{"a":1,"images":["image1"]}` {
		t.Errorf("json: %q", got)
	}
	if got := WithImageRefs(Profile{Type: TypeOpenAI}, "x", []Image{im}); got != "x" {
		t.Errorf("openai needs no refs: %q", got)
	}
}

// A typesafe call with an image sends it as an asset, with TypeSafe's opt-in header; a cloudflare profile refuses
// images before any request is made.
func TestTypeSafeImages(t *testing.T) {
	im := testImage(t)
	var got map[string]any
	var hdr string
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		hdr = r.Header.Get(ImagesHeader)
		_ = json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}`)
	}))
	defer s.Close()
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "q"}}
	p := Profile{URL: s.URL + "/v1/systemone", Model: "jev"}
	ans, _, err := AskImages(p, WithImageRefs(p, "shot", []Image{im}), []Image{im}, qs, 5*time.Second)
	if err != nil || ans["a"].P() != 0.9 {
		t.Fatalf("%v %+v", err, ans)
	}
	a, _ := got["assets"].(map[string]any)["image1"].(map[string]any)
	if hdr != "true" || a["type"] != "image" || a["format"] != "png" || a["data_base64"] != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Fatalf("header %q, assets %v", hdr, got["assets"])
	}
	if got["state"] != "shot\nImage: image1" {
		t.Fatalf("state %q", got["state"])
	}
	cf := Profile{URL: s.URL + "/x", Model: "m", Type: TypeCloudflare}
	if _, _, err := AskImages(cf, "shot", []Image{im}, qs, 5*time.Second); err == nil || calls != 1 {
		t.Fatalf("cloudflare took an image: %v (calls %d)", err, calls)
	}
}

// The openai type sends {model, input, questions: [...]} and reads the answers list back into System One answers.
func TestOpenAIType(t *testing.T) {
	im := testImage(t)
	var got map[string]any
	reply := `{"model":"gpt-6-luna","answers":[
		{"name":"err","type":"predicate","probability":0.93},
		{"name":"sev","type":"score","score":1.8,"confidence":0.7},
		{"name":"team","type":"choice","value":"api","probabilities":[{"value":"api","probability":0.8},{"value":"web","probability":0.2}]}],
		"usage":{"input_tokens":12}}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, reply)
	}))
	defer s.Close()
	qs := map[string]any{
		"err":  map[string]any{"type": "noul", "instructions": "Is this an error?"},
		"team": map[string]any{"type": "choice", "instructions": "Which team?", "criteria": map[string]string{"api": "backend", "web": "frontend"}},
		"sev":  map[string]any{"type": "score", "instructions": "How severe?", "criteria": []string{"low", "mid", "high"}},
	}
	p := Profile{URL: s.URL + "/v1/decisions", Model: "gpt-6-luna", Type: TypeOpenAI}
	ans, raw, err := AskImages(p, "checkout down", []Image{im}, qs, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ans["err"].P() != 0.93 || ans["team"].Choice != "api" || *ans["team"].Confidence != 0.8 || *ans["sev"].Score != 1.8 {
		t.Fatalf("answers %+v", ans)
	}
	if !strings.Contains(string(raw), `"model":"gpt-6-luna"`) {
		t.Fatalf("raw %s", raw)
	}
	q, _ := got["questions"].([]any)
	if got["model"] != "gpt-6-luna" || len(q) != 3 || got["state"] != nil {
		t.Fatalf("body %v", got)
	}
	first := q[0].(map[string]any) // name order: err, sev, team
	if first["name"] != "err" || first["type"] != "predicate" {
		t.Fatalf("question %v", first)
	}
	if lv := q[1].(map[string]any)["levels"].([]any); len(lv) != 3 || lv[0].(map[string]any)["label"] != "low" {
		t.Fatalf("levels %v", q[1])
	}
	if cs := q[2].(map[string]any)["choices"].([]any); len(cs) != 2 || cs[0].(map[string]any)["value"] != "api" {
		t.Fatalf("choices %v", q[2])
	}
	msg := got["input"].([]any)[0].(map[string]any)
	parts := msg["content"].([]any)
	if msg["role"] != "user" || len(parts) != 2 || !strings.HasPrefix(parts[1].(map[string]any)["image_url"].(string), "data:image/png;base64,") {
		t.Fatalf("input %v", got["input"])
	}
	// Text only: input is the plain string. A refusal is an error, never a no.
	reply = `{"answers":[{"name":"err","type":"refusal"}]}`
	if _, _, err := AskRaw(p, "x", map[string]any{"err": qs["err"]}, 5*time.Second); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("want a refusal error, got %v", err)
	}
	if got["input"] != "x" {
		t.Fatalf("text input %v", got["input"])
	}
}
