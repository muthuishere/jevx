package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFlagHelpHasNoBackquotes: Go's flag package turns a `backquoted` word in a usage string into the flag's argument
// name, so "-yes `jevx defaults`" printed as "-yes jevx defaults" in `jevx ask -h`. Usage strings must not carry them.
func TestFlagHelpHasNoBackquotes(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for n, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "fs.") && strings.Contains(l, `", "`) || strings.Contains(l, "fs.Bool(") || strings.Contains(l, "fs.Float64(") || strings.Contains(l, "fs.Int(") || strings.Contains(l, "fs.String(") {
				if i := strings.LastIndex(l, `"`); i > 0 {
					// the usage is the last double-quoted string on the line
					j := strings.LastIndex(l[:i], `"`)
					if j >= 0 && strings.Contains(l[j:i], "`") {
						t.Errorf("%s:%d: a backquote in a flag usage string renames the flag's argument in -h:\n  %s", f, n+1, strings.TrimSpace(l))
					}
				}
			}
		}
	}
}
