package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Image is one picture sent with an input. Images travel inline (base64), never as a URL jevx fetches; the file is
// read once and its bytes are hashed into the cache key, so a changed image is asked again.
type Image struct {
	Name   string // the name the state refers to: image1, image2, ...
	Path   string
	Format string // png | jpeg
	Data   []byte
}

// MaxImageBytes is the per-image size jevx accepts before sending (TypeSafe documents 16 MiB).
const MaxImageBytes = 16 << 20

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

// DataURL is the image as a data: URL (the OpenAI input_image form).
func (im Image) DataURL() string {
	return "data:image/" + im.Format + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
}

// asset is the image as a System One asset.
func (im Image) asset() map[string]any {
	f := im.Format
	if f == "jpeg" {
		f = "jpg"
	}
	return map[string]any{"type": "image", "format": f, "data_base64": base64.StdEncoding.EncodeToString(im.Data)}
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

// WithImageRefs names the images in the state, right after the item, for the types that send images as assets: a
// System One server rejects an asset the state never mentions. A JSON state gets an "images" field; text gets one
// "Image: image1" line (or "Images: image1, image2"). The OpenAI type sends images as message parts and needs no name.
func WithImageRefs(p Profile, state string, imgs []Image) string {
	if len(imgs) == 0 || p.Kind() == TypeOpenAI {
		return state
	}
	var names []string
	for _, im := range imgs {
		names = append(names, im.Name)
	}
	trimmed := strings.TrimSpace(state)
	var obj map[string]any
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") && json.Unmarshal([]byte(trimmed), &obj) == nil {
		if _, taken := obj["images"]; !taken {
			nb, _ := json.Marshal(names)
			body := strings.TrimSpace(strings.TrimSuffix(trimmed, "}"))
			sep := ","
			if strings.HasSuffix(body, "{") {
				sep = ""
			}
			return body + sep + `"images":` + string(nb) + "}"
		}
	}
	label := "Image: "
	if len(names) > 1 {
		label = "Images: "
	}
	if trimmed == "" {
		return label + strings.Join(names, ", ")
	}
	return state + "\n" + label + strings.Join(names, ", ")
}

// ImagesHeader is the opt-in header TypeSafe asks for on a request that carries images.
const ImagesHeader = "TypeSafe-Danger-Zone-Images"
