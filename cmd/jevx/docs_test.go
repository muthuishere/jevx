package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsUsageLinesMatchBinary keeps the docs honest: every usage line quoted in the docs, README or skill must be a
// line the current binary actually prints. A command that changes its usage fails this until the docs follow.
func TestDocsUsageLinesMatchBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "jevx")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	printed := map[string]bool{}
	for _, args := range [][]string{{"help"}, {"is"}, {"pick"}, {"filter"}, {"rank"}, {"question", "add"}, {"question", "bogus"},
		{"defaults", "set"}, {"profile", "add"}, {"profile", "bogus"}, {"plugin", "add"}, {"plugin", "bogus"}, {"hook"},
		{"hook", "run"}, {"memory"}, {"cache", "bogus"}, {"config", "bogus"}, {"judge"}, {"context", "bogus"}} {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "JEVX_CONFIG="+cfg, "HOME="+dir, "JEVX_LEDGER=off")
		cmd.Stdin = strings.NewReader("")
		out, _ := cmd.CombinedOutput()
		for _, l := range strings.Split(string(out), "\n") {
			printed[strings.TrimRight(l, " ")] = true
		}
	}
	files, _ := filepath.Glob("../../site/src/content/docs/*/*.md*")
	files = append(files, "../../README.md", "skill/SKILL.md")
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for n := 1; sc.Scan(); n++ {
			l := strings.TrimRight(sc.Text(), " ")
			if (strings.HasPrefix(l, "jevx: usage: ") || strings.HasPrefix(l, "usage: jevx ")) && !printed[l] {
				t.Errorf("%s:%d quotes a usage line the binary no longer prints:\n  %s", f, n, l)
			}
		}
		fh.Close()
	}
}
