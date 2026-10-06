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

// Regression (found running the plugins guide, 2026-10-06): a plugin enabled in one repo's .jevx/plugins.json must keep
// its hook when `jevx install` (or an upgrade) runs from another folder.
func TestAnotherFoldersEnabledPluginKeepsItsHook(t *testing.T) {
	old := hookSelf
	hookSelf = func() string { return "/opt/jevx/bin/jevx" }
	defer func() { hookSelf = old }()
	repo := filepath.Join(t.TempDir(), ".jevx")
	writeFile(t, filepath.Join(repo, "plugins.json"), `{"secret-guard":{"on":"PreToolUse:Write|Edit","ask":"secret","deny":"secret >= 0.8","enabled":true}}`)
	settings := filepath.Join(t.TempDir(), "settings.json")
	c := Config{Plugins: map[string]Plugin{}, PluginDirs: []string{repo}}
	if added, _, err := c.InstallHooks(settings); err != nil || added != 1 {
		t.Fatalf("the recorded folder's enabled plugin needs its PreToolUse entry: added %d err %v", added, err)
	}
	if _, removed, _ := c.InstallHooks(settings); removed != 0 {
		t.Fatal("a second sync from another folder must not remove it")
	}
	_ = os.Remove(filepath.Join(repo, "plugins.json"))
	if !c.RememberPluginDir() || len(c.PluginDirs) != 0 {
		t.Fatalf("a folder whose plugins.json is gone is forgotten: %v", c.PluginDirs)
	}
	if _, removed, _ := c.InstallHooks(settings); removed != 1 {
		t.Fatal("then its entry is removed")
	}
}

// RemoveHooks is `jevx uninstall --hooks`, the cleanup given to the owner after the 2026-10-06 incident: it must remove
// every jevx entry shape (plain, guarded, the old jevcli name), keep every other hook and setting, and back up first.
func TestRemoveHooksTakesOnlyJevxEntriesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	orig := `{"model":"opus","hooks":{
		"PreToolUse":[
			{"hooks":[{"type":"command","command":"\"/private/var/folders/x/jevx\" hook run PreToolUse"}]},
			{"matcher":"Bash","hooks":[{"type":"command","command":"other-guard.sh"}]},
			{"hooks":[{"type":"command","command":"[ -x \"/opt/jevx\" ] || exit 0; \"/opt/jevx\" hook run PreToolUse"}]}],
		"Stop":[{"hooks":[{"type":"command","command":"/usr/local/bin/jevcli hook run Stop"}]}],
		"SessionStart":[{"hooks":[{"type":"command","command":"echo hello"}]}]}}`
	writeFile(t, settings, orig)
	n, err := RemoveHooks(settings)
	if err != nil || n != 3 {
		t.Fatalf("removed %d err %v, want 3", n, err)
	}
	b, _ := os.ReadFile(settings)
	got := string(b)
	for _, keep := range []string{"other-guard.sh", `"matcher": "Bash"`, "echo hello", `"model": "opus"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %s:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "hook run") || strings.Contains(got, `"Stop"`) {
		t.Errorf("a jevx entry or an emptied event is left:\n%s", got)
	}
	baks, _ := filepath.Glob(settings + ".bak-jevx-*")
	if len(baks) != 1 {
		t.Fatalf("want one backup, got %v", baks)
	}
	if bb, _ := os.ReadFile(baks[0]); string(bb) != orig {
		t.Fatal("the backup must be the original file")
	}
	if n, _ := RemoveHooks(settings); n != 0 {
		t.Fatal("a second run removes nothing")
	}
}
