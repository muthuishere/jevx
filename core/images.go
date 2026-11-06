package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tn "github.com/muthuishere/toolnexus/golang"
)

// Image is one picture sent with an input. Images travel inline (base64), never as a URL jevx fetches; the file is
// read once and its bytes are hashed into the cache key, so a changed image is asked again.
type Image struct {
	Name   string // the name the state refers to: image1, image2, ...
	Path   string
	Format string // png | jpeg
	Data   []byte
}

// MaxImageBytes is the per-image size jevx reads (toolnexus checks it again, with the pixel limits).
const MaxImageBytes = tn.ClassifierImageMaxBytes

// LoadImage reads a PNG or JPEG file (detected from its bytes, not its name).
func LoadImage(path string) (Image, error) {
	b, err := os.ReadFile(expand(path))
	if err != nil {
		return Image{}, err
	}
	if len(b) > MaxImageBytes {
		return Image{}, fmt.Errorf("%s: %d MB, over the %d MB limit", path, len(b)>>20, MaxImageBytes>>20)
	}
	switch http.DetectContentType(b) {
	case "image/png":
		return Image{Path: path, Format: "png", Data: b}, nil
	case "image/jpeg":
		return Image{Path: path, Format: "jpeg", Data: b}, nil
	}
	return Image{}, fmt.Errorf("%s: not a PNG or JPEG image", path)
}

// LoadImages reads every file and names them image1, image2, ... in order.
func LoadImages(paths []string) ([]Image, error) {
	var out []Image
	for i, p := range paths {
		im, err := LoadImage(p)
		if err != nil {
			return nil, err
		}
		im.Name = fmt.Sprintf("image%d", i+1)
		out = append(out, im)
	}
	return out, nil
}

// ImageFiles expands an --images source into image paths, sorted: a directory (its .png / .jpg / .jpeg files), a
// glob, or a text file listing one path per line.
func ImageFiles(src string) ([]string, error) {
	src = expand(src)
	if st, err := os.Stat(src); err == nil && st.IsDir() {
		ents, _ := os.ReadDir(src)
		var out []string
		for _, e := range ents {
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".png", ".jpg", ".jpeg":
				if !e.IsDir() {
					out = append(out, filepath.Join(src, e.Name()))
				}
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s: no .png, .jpg or .jpeg files", src)
		}
		return out, nil
	} else if err == nil {
		b, _ := os.ReadFile(src)
		if ext := strings.ToLower(filepath.Ext(src)); ext == ".png" || ext == ".jpg" || ext == ".jpeg" {
			return []string{src}, nil
		}
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return out, nil
	}
	m, err := filepath.Glob(src)
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("%s: no such file, folder or matching files", src)
	}
	sort.Strings(m)
	return m, nil
}

// ImageDigest identifies the images' bytes and names for the cache key ("" for none).
func ImageDigest(imgs []Image) string {
	if len(imgs) == 0 {
		return ""
	}
	h := sha256.New()
	for _, im := range imgs {
		s := sha256.Sum256(im.Data)
		fmt.Fprintf(h, "%s:%x;", im.Name, s)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ImageState is the state toolnexus is given: the text alone, or the text followed by each image as a toolnexus
// ContentPart. toolnexus moves every image into the request's assets under its own name, checks TypeSafe's limits
// (PNG/JPEG, 280-1568 px per side, 16 MiB each, 32 MiB a request) and adds the opt-in header where the backend
// needs it. A JSON object state keeps its shape and gets the images under "images".
func ImageState(state string, imgs []Image) any {
	if len(imgs) == 0 {
		return state
	}
	parts := make([]any, 0, len(imgs))
	for _, im := range imgs {
		parts = append(parts, tn.Bytes(im.Data, "image/"+im.Format))
	}
	trimmed := strings.TrimSpace(state)
	var obj map[string]any
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &obj) == nil {
		if _, taken := obj["images"]; !taken {
			obj["images"] = parts
			return obj
		}
	}
	if trimmed == "" { // an image-only input
		return parts
	}
	return append([]any{state}, parts...)
}
