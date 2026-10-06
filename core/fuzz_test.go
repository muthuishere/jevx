package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Fuzz the parsers that read user text (memory pages, claims, anything sent to a hosted endpoint): they must never
// panic, and must keep their basic promises on any input.

func FuzzSections(f *testing.F) {
	for _, s := range []string{"# T\nintro\n\n## A\nx\n```sh\n# not a heading\n```\n## B\ny\n", "", "##", "# ā சொல்\n" + strings.Repeat("word ", 400)} {
		f.Add(s, 900)
	}
	f.Fuzz(func(t *testing.T, md string, maxc int) {
		if maxc < 1 || maxc > 5000 {
			maxc = 900
		}
		for _, s := range Sections("p.md", md, maxc) {
			if s.Start < 1 || s.End < s.Start {
				t.Fatalf("bad line range %d-%d", s.Start, s.End)
			}
			if utf8.ValidString(md) && !utf8.ValidString(s.Text) {
				t.Fatalf("section cut a rune: %q", s.Text)
			}
		}
	})
}

func FuzzParseCites(f *testing.F) {
	for _, s := range []string{"x [[openjevx:README.md:2-3]] y", "[[a:b:1]]", "[[a:b:9-1]]", "[[::]]", "[[a:b:99999999999999999999-1]]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, md string) {
		for _, c := range parseCites(md) {
			if c.Repo == "" || c.Path == "" {
				t.Fatalf("empty repo or path: %+v", c)
			}
		}
	})
}

func FuzzMemDiffAndRedact(f *testing.F) {
	for _, s := range []string{"Claim: port 21160 and `task build`", "password=hunter2 at user@example.com", "", "0.0.0.0:21118"} {
		f.Add(s, "notes say 21160")
	}
	f.Fuzz(func(t *testing.T, item, note string) {
		_ = MemDiff(item, []MemHit{{Page: "p.md", Head: "h", text: note}})
		r := Redact(item)
		if utf8.ValidString(item) && !utf8.ValidString(r) {
			t.Fatalf("redaction broke UTF-8: %q -> %q", item, r)
		}
		_ = RedactedKinds(item, r)
	})
}
