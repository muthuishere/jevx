package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readHooks(t *testing.T, settings string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(settings)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	h, _ := m["hooks"].(map[string]any)
	return h
}

// The owner's rule (jevresearch incident, 2026-10-06): with no plugin enabled, settings.json holds no jevx hook at all.
// Old entries (an older binary, a vanished temp install) are removed; other tools' hooks are kept.
func TestNoEnabledPluginMeansNoHookEntries(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	writeFile(t, settings, `{"hooks":{"PreToolUse":[
		{"hooks":[{"type":"command","command":"\"/tmp/scratch/jevx\" hook run PreToolUse"}]},
		{"hooks":[{"type":"command","command":"other-tool check"}]}]}}`)
	c := Config{Plugins: shipped()}
	for k, p := range c.Plugins {
		p.Enabled = false
		c.Plugins[k] = p
	}
	added, removed, err := c.InstallHooks(settings)
	if err != nil || added != 0 || removed != 1 {
		t.Fatalf("added %d removed %d err %v", added, removed, err)
	}
	h := readHooks(t, settings)
	b, _ := json.Marshal(h)
	if strings.Contains(string(b), "hook run") || !strings.Contains(string(b), "other-tool check") {
		t.Fatalf("want only the other tool's hook left: %s", b)
	}
}

func TestEnablingAddsOneGuardedEntryAndDisablingRemovesIt(t *testing.T) {
	old := hookSelf
	hookSelf = func() string { return "/opt/jevx/bin/jevx" }
	defer func() { hookSelf = old }()
	settings := filepath.Join(t.TempDir(), "settings.json")
	c := Config{Plugins: map[string]Plugin{"g": {On: "PreToolUse:Bash", Ask: "x", Deny: "x >= 0.9", Enabled: true}}}
	if added, _, err := c.InstallHooks(settings); err != nil || added != 1 {
		t.Fatalf("added %d err %v", added, err)
	}
	b, _ := json.Marshal(readHooks(t, settings))
	if runtime.GOOS != "windows" && !strings.Contains(string(b), `[ -x \"/opt/jevx/bin/jevx\" ] || exit 0;`) {
		t.Fatalf("the hook must be a no-op when the binary is gone: %s", b)
	}
	p := c.Plugins["g"]
	p.Enabled = false
	c.Plugins["g"] = p
	if _, removed, err := c.InstallHooks(settings); err != nil || removed != 1 || len(readHooks(t, settings)) != 0 {
		t.Fatalf("disabling the last plugin removes its entry: removed %d err %v %v", removed, err, readHooks(t, settings))
	}
}

func TestHooksRefuseATemporaryBinary(t *testing.T) {
	for p, want := range map[string]bool{
		"/tmp/x/jevx": true, "/private/tmp/jevx": true, "/home/u/claude/scratchpad/rel/jevx": true,
		filepath.Join(os.TempDir(), "a", "jevx"): true, "/home/u/.local/bin/jevx": false, "/usr/local/bin/jevx": false,
	} {
		if TempPath(p) != want {
			t.Errorf("TempPath(%q) = %v, want %v", p, !want, want)
		}
	}
	old := hookSelf
	hookSelf = func() string { return "/tmp/release-check/jevx" }
	defer func() { hookSelf = old }()
	c := Config{Plugins: map[string]Plugin{"g": {On: "Stop", Ask: "x", Block: "x >= 0.9", Enabled: true}}}
	settings := filepath.Join(t.TempDir(), "settings.json")
	if _, _, err := c.InstallHooks(settings); err == nil || !strings.Contains(err.Error(), "temporary folder") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Fatal("a refused install must not write settings.json")
	}
}
