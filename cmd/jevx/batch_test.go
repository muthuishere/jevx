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
	"strconv"
	"strings"
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
