package core

import (
	"encoding/base64"
	"encoding/binary"
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

// pngOf is a PNG signature plus an IHDR of w x h: what jevx sniffs and toolnexus measures.
func pngOf(w, h int) []byte {
	b := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	b = binary.BigEndian.AppendUint32(b, uint32(w))
	b = binary.BigEndian.AppendUint32(b, uint32(h))
	return append(b, 8, 6, 0, 0, 0, 0, 0, 0, 0)
}

func testImage(t *testing.T, w, h int) Image {
	t.Helper()
	f := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(f, pngOf(w, h), 0o644); err != nil {
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
		{Profile{URL: "https://x/client/v4/accounts/1/ai/run", Model: "typesafe/jev"}, TypeCloudflare, false},
		{Profile{URL: "https://x/ai/run", Model: "@cf/cloudflare/clef"}, TypeCloudflare, true},
		{Profile{URL: "https://x", Style: "cloudflare"}.Norm(), TypeCloudflare, false},
		{Profile{URL: "https://openrouter.ai/api/v1/systemone"}, TypeOpenRouter, true},
		{Profile{URL: "https://ai-gateway.vercel.sh/typesafe/v1/systemone"}, TypeVercel, false},
		{Profile{URL: "http://box:9000/v1/systemone", Type: TypeCustom}, TypeCustom, false},
		{Profile{URL: "http://box:9000", Type: TypeCustom, Images: bp(true)}, TypeCustom, true},
		{Profile{URL: "https://x", Images: bp(false)}, TypeTypeSafe, false},
	} {
		if c.p.Kind() != c.kind || c.p.TakesImages() != c.img {
			t.Errorf("%+v: kind %s images %v, want %s %v", c.p, c.p.Kind(), c.p.TakesImages(), c.kind, c.img)
		}
	}
	// A new profile of a type gets toolnexus's preset url, model and key variable when it gives none.
	cf := Preset(TypeCloudflare, Profile{Type: TypeCloudflare})
	if !strings.HasSuffix(cf.URL, "/ai/run") || cf.Model != "typesafe/jev" || cf.Headers["Authorization"] != "Bearer $CLOUDFLARE_JEV_API_KEY" {
		t.Fatalf("cloudflare preset %+v", cf)
	}
	if or := Preset(TypeOpenRouter, Profile{}); or.URL != "https://openrouter.ai/api/v1/systemone" || or.Headers["Authorization"] != "Bearer $OPENROUTER_API_KEY" {
		t.Fatalf("openrouter preset %+v", or)
	}
	for _, typ := range []string{TypeCustom, TypeTypeSafe} {
		if c := Preset(typ, Profile{URL: "http://box"}); c.Headers != nil || c.URL != "http://box" {
			t.Fatalf("a %s profile with its own url gets no key: %+v", typ, c)
		}
	}
}

func TestLoadImageRejectsNonImages(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.png")
	_ = os.WriteFile(f, []byte("hello"), 0o644)
	if _, err := LoadImage(f); err == nil || !strings.Contains(err.Error(), "not a PNG or JPEG") {
		t.Fatalf("want a not-an-image error, got %v", err)
	}
}

func TestImageState(t *testing.T) {
	im := Image{Name: "image1", Format: "png", Data: pngOf(300, 300)}
	if got := ImageState("x", nil); got != "x" {
		t.Errorf("no images: %v", got)
	}
	if got, _ := ImageState("is this broken?", []Image{im}).([]any); len(got) != 2 || got[0] != "is this broken?" {
		t.Errorf("text: %v", got)
	}
	if got, _ := ImageState(`{"a":1}`, []Image{im}).(map[string]any); got["a"] == nil || got["images"] == nil {
		t.Errorf("json: %v", got)
	}
}

// Through toolnexus, a typesafe image goes as an asset with TypeSafe's opt-in header; an image outside the pixel
// limits, or to a backend that does not take images, is refused before any request.
func TestImagesThroughToolnexus(t *testing.T) {
	var got map[string]any
	var hdr string
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		hdr = r.Header.Get("TypeSafe-Danger-Zone-Images")
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}`)
	}))
	defer s.Close()
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "q"}}
	im := testImage(t, 300, 300)
	p := Profile{URL: s.URL + "/v1/systemone", Model: "jev-1.14.0"}
	ans, _, err := AskImages(p, "shot", []Image{im}, qs, 5*time.Second)
	if err != nil || ans["a"].P() != 0.9 {
		t.Fatalf("%v %+v", err, ans)
	}
	assets, _ := got["assets"].(map[string]any)
	a, _ := assets["image1"].(map[string]any)
	if hdr != "true" || a["type"] != "image" || a["format"] != "png" || a["data_base64"] != base64.StdEncoding.EncodeToString(im.Data) {
		t.Fatalf("header %q, assets %v", hdr, got["assets"])
	}
	if st, _ := got["state"].([]any); len(st) != 2 || st[0] != "shot" || st[1] != "image1" {
		t.Fatalf("state %v", got["state"])
	}
	small := testImage(t, 100, 100)
	if _, _, err := AskImages(p, "shot", []Image{small}, qs, 5*time.Second); err == nil || !strings.Contains(err.Error(), "280..1568") || calls != 1 {
		t.Fatalf("a 100 px image: %v (calls %d)", err, calls)
	}
	cf := Profile{URL: s.URL + "/ai/run", Model: "typesafe/jev", Type: TypeCloudflare}
	if _, _, err := AskImages(cf, "shot", []Image{im}, qs, 5*time.Second); err == nil || calls != 1 {
		t.Fatalf("cloudflare jev took an image: %v (calls %d)", err, calls)
	}
}

// The cloudflare type is toolnexus's /ai/run envelope: {model, input:{state, questions}} out, {success, result} back,
// posted to the profile's url as it is.
func TestCloudflareType(t *testing.T) {
	var got map[string]any
	var path string
	ok := true
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		if !ok {
			io.WriteString(w, `{"success":false,"errors":[{"code":2021,"message":"Insufficient balance"}],"result":null}`)
			return
		}
		io.WriteString(w, `{"success":true,"errors":[],"result":{"state":"Completed","result":{"model":"m","answers":{"a":{"type":"noul","noul":0.9}}}}}`)
	}))
	defer s.Close()
	qs := map[string]any{"a": map[string]any{"type": "noul", "instructions": "q"}}
	p := Profile{URL: s.URL + "/client/v4/accounts/1/ai/run", Model: "typesafe/jev", Type: TypeCloudflare}
	ans, _, err := AskRaw(p, "item", qs, 5*time.Second)
	if err != nil || ans["a"].P() != 0.9 {
		t.Fatalf("%v %+v", err, ans)
	}
	in, _ := got["input"].(map[string]any)
	if path != "/client/v4/accounts/1/ai/run" || got["model"] != "typesafe/jev" || in["state"] != "item" {
		t.Fatalf("path %s body %v", path, got)
	}
	ok = false
	if _, _, err := AskRaw(p, "item", qs, 5*time.Second); err == nil || !strings.Contains(err.Error(), "not successful") {
		t.Fatalf("a failed envelope must be an error, got %v", err)
	}
}
