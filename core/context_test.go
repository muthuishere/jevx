package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithContext(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	setHome(t, home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude/CLAUDE.md"), []byte("# me\n\n## Jev\n\nglobal\n\n## Other\nno\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".codex/AGENTS.md"), []byte("#jev\nglobal\n"), 0o644) // a synced copy: deduplicated
	_ = os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# repo\n\n### Jev notes\nfolder\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("## jev\nfolder\nclaude only\n"), 0o644)
	sub := filepath.Join(repo, "sub")
	_ = os.MkdirAll(sub, 0o755)
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd); NoContext = false })
	_ = os.Chdir(sub)

	c := Config{}
	if got := c.WithContext(Profile{}, "the text", "call"); got != "the text\n\nContext: global\nfolder\nclaude only\ncall" {
		t.Fatalf("text state: %q", got)
	}
	if got := c.WithContext(Profile{}, `{"message":"hi","context":"own"}`); got != `{"message":"hi","context":"global\nfolder\nclaude only\nown"}` {
		t.Fatalf("JSON state must stay JSON with a merged context field: %q", got)
	}
	NoContext = true
	if got := c.WithContext(Profile{}, "x", "call"); got != "x\n\nContext: call" {
		t.Fatalf("--no-context keeps only the call's context: %q", got)
	}
	if got := c.WithContext(Profile{}, "x"); got != "x" {
		t.Fatalf("no context must leave the state alone: %q", got)
	}
	if w := (Question{Instructions: "Q?", Context: "bg"}).Wire()["instructions"]; !strings.HasPrefix(w.(string), "Context: bg\n") {
		t.Fatalf("question context: %q", w)
	}
}

// The item being judged must survive an over-long context: it comes first, and the context is capped.
func TestLongContextNeverPushesTheItemOut(t *testing.T) {
	NoContext = true
	defer func() { NoContext = false }()
	long := strings.Repeat("background sentence about the project. ", 2000) // ~78k chars
	item := "ITEM-UNDER-JUDGEMENT: is this reply on topic?"
	for _, state := range []string{item, `{"text":"` + item + `","lang":"en"}`} {
		got := Config{}.WithContext(Profile{}, state, long)
		if i := strings.Index(got, "ITEM-UNDER-JUDGEMENT"); i < 0 || i > 20 {
			t.Fatalf("item not at the front (index %d) of %.80q", i, got)
		}
		if len(got) > len(state)+MaxContextChars+100 {
			t.Fatalf("context not capped: %d chars", len(got))
		}
		if strings.HasPrefix(state, "{") {
			var m map[string]any
			if err := json.Unmarshal([]byte(got), &m); err != nil || m["text"] != item || m["lang"] != "en" {
				t.Fatalf("JSON state broken: %v %v", err, m)
			}
		}
	}
}
