package core

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// LastTurn feeds the stop-judge: it must pick the last GENUINE human request (not wrappers, tool results, subagent
// side chains or meta lines) and what the agent said and did after it, with valid UTF-8 even when a tool input is cut.
func TestLastTurnPicksTheGenuineRequest(t *testing.T) {
	line := func(m map[string]any) string { b, _ := json.Marshal(m); return string(b) }
	user := func(content any) string {
		return line(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
	}
	asst := func(blocks ...map[string]any) string {
		return line(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": blocks}})
	}
	tamil := strings.Repeat("சொல் ", 200) // multi-byte, longer than the 400-character cut
	lines := []string{
		user("an older request"),
		asst(map[string]any{"type": "text", "text": "old answer"}),
		user("Fix the port in the README"),
		user("<system-reminder>not a human</system-reminder>"),
		line(map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"content": "meta"}}),
		line(map[string]any{"type": "user", "isSidechain": true, "message": map[string]any{"content": "subagent prompt"}}),
		asst(map[string]any{"type": "tool_use", "name": "Write", "input": map[string]any{"file_path": "README.md", "content": tamil}}),
		user([]map[string]any{{"type": "tool_result", "content": "ok"}}),
		asst(map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "go test ./..."}}),
		user("[Request interrupted by user]"),
		asst(map[string]any{"type": "text", "text": "Done: the port is 21160 now."}),
	}
	p := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, p, strings.Join(lines, "\n")+"\n")
	req, text, acts, ok := LastTurn(p)
	if !ok || req != "Fix the port in the README" {
		t.Fatalf("request: %q ok=%v", req, ok)
	}
	if text != "Done: the port is 21160 now." || len(acts) != 2 || !strings.HasPrefix(acts[0], "Write README.md: ") || acts[1] != "Bash: go test ./..." {
		t.Fatalf("text %q actions %q", text, acts)
	}
	for _, a := range acts {
		if !utf8.ValidString(a) {
			t.Fatalf("an action was cut mid-character: %q", a)
		}
	}
	if skipTurn(acts) != "checked after the last edit" {
		t.Fatalf("a test after the edit skips the judge: %q", skipTurn(acts))
	}
}

// State fits a turn into a 1600-byte budget (a token proxy). Its last-resort trim must cut on a character boundary:
// a long Tamil turn used to come out as invalid UTF-8.
func TestStateFallbackCutsByCharacter(t *testing.T) {
	long := strings.Repeat("சொல் ", 60) // 300 characters, 900+ bytes each
	acts := []string{"Write a.md: " + long, "Write b.md: " + long, "Bash: " + long}
	s := State(strings.Repeat("அ", 290), strings.Repeat("ஆ", 390), acts)
	if !utf8.ValidString(s) || len(s) > 1600 || !strings.HasPrefix(s, "Request: ") {
		t.Fatalf("valid UTF-8 within 1600 bytes, request first: valid=%v bytes=%d", utf8.ValidString(s), len(s))
	}
}
