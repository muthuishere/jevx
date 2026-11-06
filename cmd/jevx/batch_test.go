package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestParallelBatchKeepsEachAnswerWithItsInput runs a 200-line batch at --parallel 16 against a stub whose answer
// depends on the input (even line numbers: yes 0.9, odd: no 0.1, after a random-ish delay), twice: fresh, then from
// the cache. Every row must carry its own input's answer, in input order. With `go test -race` this also covers the
// batch goroutines and concurrent cache writes.
func TestParallelBatchKeepsEachAnswerWithItsInput(t *testing.T) {
	num := regexp.MustCompile(`item (\d+)`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n, _ := strconv.Atoi(append(num.FindStringSubmatch(req.State), "", "")[1])
		time.Sleep(time.Duration(n%7) * time.Millisecond)
		p := 0.1
		if n%2 == 0 {
			p = 0.9
		}
		ans := map[string]any{}
		for k := range req.Questions {
			ans[k] = map[string]any{"type": "noul", "noul": p, "confidence": 0.9}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "stub", "answers": ans})
	}))
	defer srv.Close()

	dir := t.TempDir()
	bin := filepath.Join(dir, "jevx")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := []string{"build", "-o", bin}
	if raceBuild { // under `go test -race`, race-check the binary's own batch goroutines too
		build = append(build, "-race")
	}
	if out, err := exec.Command("go", append(build, ".")...).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	env := append(os.Environ(), "HOME="+dir, "USERPROFILE="+dir, "JEVX_CONFIG="+filepath.Join(dir, "cfg.json"))
	run := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("jevx %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("profile", "add", "stub", srv.URL+"/v1/systemone", "--model", "stub")
	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, fmt.Sprintf("item %d", i))
	}
	in := filepath.Join(dir, "lines.txt")
	_ = os.WriteFile(in, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	for _, extra := range []string{"--fresh", ""} {
		args := []string{"ask", "--profile", "stub", "--no-context", "--parallel", "16", "--lines", in, "--noul", "x=Is this even?"}
		if extra != "" {
			args = append(args, extra)
		}
		rows := strings.Split(strings.TrimSpace(run(args...)), "\n")[1:] // skip the header
		if len(rows) != 200 {
			t.Fatalf("%s: want 200 rows, got %d", extra, len(rows))
		}
		for i, row := range rows {
			want := []string{"no", "0.10", "item", strconv.Itoa(i + 1)}
			if (i+1)%2 == 0 {
				want[0], want[1] = "yes", "0.90"
			}
			if got := strings.Fields(row); strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("%s row %d: %q, want %q", extra, i+1, got, want)
			}
		}
	}
}

// TestImageBatch: --images DIR is one input per image file, in name order, shown by its path; filter prints the
// matching files; a cloudflare profile refuses images with exit 4 before any call.
func TestImageBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
			Assets    map[string]struct {
				Data string `json:"data_base64"`
			} `json:"assets"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		p := 0.1
		if strings.HasSuffix(req.Assets["image1"].Data, "Ag==") || r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" || !strings.Contains(req.State, "Image: image1") {
			p = 0.9 // the second image's last byte is 2; a request without the header or the reference also says yes, and fails the test
		}
		ans := map[string]any{}
		for k := range req.Questions {
			ans[k] = map[string]any{"type": "noul", "noul": p}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "stub", "answers": ans})
	}))
	defer srv.Close()
	dir := t.TempDir()
	bin := filepath.Join(dir, "jevx")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	imgs := filepath.Join(dir, "imgs")
	_ = os.MkdirAll(imgs, 0o755)
	for i, n := range []string{"a.png", "b.png", "notes.txt"} {
		_ = os.WriteFile(filepath.Join(imgs, n), append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 8)...), 0o644)
		if i == 1 {
			_ = os.WriteFile(filepath.Join(imgs, n), append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 0, 0, 0, 0, 2), 0o644)
		}
	}
	env := append(os.Environ(), "HOME="+dir, "USERPROFILE="+dir, "JEVX_CONFIG="+filepath.Join(dir, "cfg.json"), "JEVX_LEDGER=off")
	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}
	run("profile", "add", "stub", srv.URL+"/v1/systemone", "--model", "stub")
	out, code := run("ask", "--profile", "stub", "--no-context", "--images", imgs, "--noul", "x=Is it broken?")
	rows := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(rows) != 3 || !strings.HasPrefix(rows[1], "no ") || !strings.HasSuffix(rows[1], "a.png") ||
		!strings.HasPrefix(rows[2], "yes ") || !strings.HasSuffix(rows[2], "b.png") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if out, _ := run("filter", "Is it broken?", "--profile", "stub", "--no-context", "--images", imgs); strings.TrimSpace(out) != filepath.Join(imgs, "b.png") {
		t.Fatalf("filter:\n%s", out)
	}
	if out, code := run("is", "Is it broken?", "--profile", "stub", "--no-context", "--image", filepath.Join(imgs, "b.png")); code != 0 || !strings.HasPrefix(out, "yes") {
		t.Fatalf("is --image: exit %d %s", code, out)
	}
	run("profile", "add", "cf", srv.URL+"/x", "--type", "cloudflare", "--model", "m")
	if out, code := run("is", "Is it broken?", "--profile", "cf", "--image", filepath.Join(imgs, "a.png")); code != 4 || !strings.Contains(out, "does not take images") {
		t.Fatalf("cloudflare + image: exit %d %s", code, out)
	}
}

// TestNativeBatch: a profile with "batch": 4 sends 10 lines as 3 requests to {url}/batch, every row keeps its own
// answer in input order, and a second run is all from the cache. A server without the route falls back to one
// request per line, once told.
func TestNativeBatch(t *testing.T) {
	num := regexp.MustCompile(`item (\d+)`)
	var batches, singles, sizes []int
	var mu sync.Mutex
	answer := func(state string, qs map[string]json.RawMessage) map[string]any {
		n, _ := strconv.Atoi(append(num.FindStringSubmatch(state), "", "")[1])
		p := 0.1
		if n%2 == 0 {
			p = 0.9
		}
		ans := map[string]any{}
		for k := range qs {
			ans[k] = map[string]any{"type": "noul", "noul": p}
		}
		return map[string]any{"model": "stub", "answers": ans}
	}
	route := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/batch") {
			if !route {
				http.NotFound(w, r)
				return
			}
			var req struct {
				Requests []struct {
					State     string                     `json:"state"`
					Questions map[string]json.RawMessage `json:"questions"`
				} `json:"requests"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			batches, sizes = append(batches, 1), append(sizes, len(req.Requests))
			var res []any
			for _, x := range req.Requests {
				res = append(res, answer(x.State, x.Questions))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "stub", "results": res})
			return
		}
		var req struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		singles = append(singles, 1)
		_ = json.NewEncoder(w).Encode(answer(req.State, req.Questions))
	}))
	defer srv.Close()
	dir := t.TempDir()
	bin := filepath.Join(dir, "jevx")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	env := append(os.Environ(), "HOME="+dir, "USERPROFILE="+dir, "JEVX_CONFIG="+filepath.Join(dir, "cfg.json"), "JEVX_LEDGER=off")
	run := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("jevx %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("profile", "add", "nb", srv.URL+"/v1/systemone", "--model", "stub", "--batch", "4")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("item %d", i))
	}
	in := filepath.Join(dir, "lines.txt")
	_ = os.WriteFile(in, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	check := func(out string) {
		rows := strings.Split(strings.TrimSpace(out), "\n")
		rows = rows[len(rows)-10:]
		for i, row := range rows {
			want := "no 0.10 item " + strconv.Itoa(i+1)
			if (i+1)%2 == 0 {
				want = "yes 0.90 item " + strconv.Itoa(i+1)
			}
			if got := strings.Join(strings.Fields(row), " "); got != want {
				t.Fatalf("row %d: %q, want %q\n%s", i+1, got, want, out)
			}
		}
	}
	args := []string{"ask", "--profile", "nb", "--no-context", "--lines", in, "--noul", "x=Is this even?"}
	counts := func() (int, int) { mu.Lock(); defer mu.Unlock(); return len(batches), len(singles) }
	check(run(args...))
	mu.Lock()
	sort.Ints(sizes) // the groups run in parallel, so they arrive in any order
	got := fmt.Sprint(sizes)
	mu.Unlock()
	if b, n := counts(); b != 3 || n != 0 || got != "[2 4 4]" {
		t.Fatalf("batches %d (sizes %v), singles %d", b, got, n)
	}
	check(run(args...)) // all cached: nothing sent
	if b, n := counts(); b != 3 || n != 0 {
		t.Fatalf("cached run sent %d batches, %d singles", b-3, n)
	}
	mu.Lock()
	route = false
	mu.Unlock()
	out := run(append(args, "--fresh")...)
	check(out)
	if _, n := counts(); n != 10 || !strings.Contains(out, "no native batch route") {
		t.Fatalf("fallback: %d singles\n%s", n, out)
	}
}
