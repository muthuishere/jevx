package core

// Plugins: a hook behaviour declared in config, run by the one generic hook runner (`jevx hook run EVENT`).
//
//	{"on": "PreToolUse:Bash", "ask": "destroys,remote", "deny": "destroys >= 0.8", "warn": "remote >= 0.5"}
//
// on = the agent event, with an optional tool after the colon (a regex). ask = saved question names (global, folder or
// the built-in pack), asked in one call about a state built from the event. deny / warn / block / context = a condition
// over the answers that triggers that action. exec = hand the event (and the answers) to any command instead.
// Plugins ship disabled, and start in shadow mode: they log what they would do and act only after `plugin enable`.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Plugin struct {
	Desc    string `json:"description,omitempty"`
	On      string `json:"on"`                // Stop | PreToolUse[:Tool] | PostToolUse[:Tool] | UserPromptSubmit | PreCompact | SessionStart | SubagentStop
	Ask     string `json:"ask,omitempty"`     // comma-separated question names
	Deny    string `json:"deny,omitempty"`    // PreToolUse: refuse the tool call
	Warn    string `json:"warn,omitempty"`    // PreToolUse: ask the user; elsewhere: add a warning as context
	Block   string `json:"block,omitempty"`   // Stop / PostToolUse: send the agent back with a reason
	Context string `json:"context,omitempty"` // add context for the agent
	Say     string `json:"say,omitempty"`     // the text to inject for warn / context / block; {{name}} = that answer. Default: the answers
	Exec    string `json:"exec,omitempty"`    // a command: gets {event, plugin, payload, state, answers} on stdin, prints the decision JSON
	Profile string `json:"profile,omitempty"`
	Gate    string `json:"gate,omitempty"` // Stop: "" = only turns that edited files with no check after; "all" = every turn
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode,omitempty"` // shadow (log only, default) | act
}

// Event and Tool split "PreToolUse:Bash".
func (p Plugin) Event() string { e, _, _ := strings.Cut(p.On, ":"); return e }
func (p Plugin) Tool() string  { _, t, _ := strings.Cut(p.On, ":"); return t }

// Shipped are the plugins every config starts with, all disabled.
var Shipped = map[string]Plugin{
	"stop-judge": {Desc: "Before the agent hands a turn back: would the user accept it, and did it stop short?",
		On: "Stop", Ask: "accepts,wanted_more", Block: "accepts < 0.35 || wanted_more > 0.65"},
	"bash-guard": {Desc: "Before a shell command runs: refuse one that would destroy data, ask about one that touches remote systems.",
		On: "PreToolUse:Bash", Ask: "destroys,remote,irreversible", Deny: "destroys >= 0.8 && irreversible >= 0.6", Warn: "destroys >= 0.5 || remote >= 0.8"},
	"injection-screen": {Desc: "After the agent reads a web page: warn when the text tries to give the agent instructions.",
		On: "PostToolUse:WebFetch|WebSearch", Ask: "injection", Warn: "injection >= 0.7"},
	"route": {Desc: "When the user writes: if the request is simple, tell the agent to hand it to a sub-agent on a smaller model.",
		On: "UserPromptSubmit", Ask: "complexity,kind", Context: "complexity <= 0.5",
		Say: "This request looks simple ({{kind}}). Delegate it to one sub-agent with model haiku titled \"{{kind}}: <the task in 4 words>\" and return its result to the user; do not do it in this session."},
}

// LocalPlugins are the nearest .jevx/plugins.json (they override global ones with the same name).
func LocalPlugins() map[string]Plugin {
	m := map[string]Plugin{}
	if d := LocalDir(); d != "" {
		if b, err := os.ReadFile(filepath.Join(d, "plugins.json")); err == nil {
			_ = json.Unmarshal(b, &m)
		}
	}
	return m
}

// AllPlugins merges global and folder plugins.
func (c Config) AllPlugins() map[string]Plugin {
	m := map[string]Plugin{}
	for k, p := range c.Plugins {
		m[k] = p
	}
	for k, p := range LocalPlugins() {
		m[k] = p
	}
	return m
}

// ResolveQuestion finds a question by name: saved (folder, then global), else the profile's question pack.
func (c Config) ResolveQuestion(p Profile, name string) (Question, bool) {
	if q, ok := LocalQuestions()[name]; ok {
		return q, true
	}
	if q, ok := c.Questions[name]; ok {
		return q, true
	}
	qs := questions(p)
	if q, ok := qs.Noul[name]; ok {
		return Question{Type: "noul", Instructions: q.Instructions, Criteria: map[string]string{"true": q.True, "false": q.False}}, true
	}
	if q, ok := qs.Choice[name]; ok {
		return Question{Type: "choice", Instructions: q.Instructions, Criteria: q.Options}, true
	}
	if q, ok := qs.Score[name]; ok {
		return Question{Type: "score", Instructions: q.Instructions, Criteria: q.Levels}, true
	}
	return Question{}, false
}

// ---------------------------------------------------------------- the event payload -> a state

// Payload is the hook's stdin JSON, kept as a map because every event has its own fields.
type Payload map[string]any

func (p Payload) Str(k string) string { v, _ := p[k].(string); return v }

// Matches says whether a plugin applies to this payload: same event, and the tool regex (if any) matches tool_name.
func (pl Plugin) Matches(event string, p Payload) bool {
	if pl.Event() != event {
		return false
	}
	if t := pl.Tool(); t != "" {
		re, err := regexp.Compile("^(" + t + ")$")
		return err == nil && re.MatchString(p.Str("tool_name"))
	}
	return true
}

// StateOf builds the text the questions are asked about. skip is a reason to spend no call (the Stop gate).
func StateOf(event string, p Payload, gate string) (state, skip string) {
	compact := func(v any, n int) string {
		b, _ := json.Marshal(v)
		s := string(b)
		if s == "null" {
			s = ""
		}
		return cut(s, n)
	}
	switch event {
	case "Stop", "SubagentStop":
		req, text, acts, ok := LastTurn(p.Str("transcript_path"))
		if !ok {
			return "", "no turn found in the transcript"
		}
		if gate != "all" {
			if why := skipTurn(acts); why != "" {
				return "", why
			}
		}
		return State(req, text, acts), ""
	case "PreToolUse":
		return fmt.Sprintf("The agent is about to call tool %s with:\n%s\nWorking directory: %s", p.Str("tool_name"), compact(p["tool_input"], 3000), p.Str("cwd")), ""
	case "PostToolUse":
		return fmt.Sprintf("The agent called tool %s with:\n%s\nIt returned:\n%s", p.Str("tool_name"), compact(p["tool_input"], 1500), compact(p["tool_response"], 3000)), ""
	case "UserPromptSubmit":
		return "The user just wrote to the agent:\n" + cut(p.Str("prompt"), 3000), ""
	}
	return cut(fmt.Sprintf("Event %s:\n%s", event, compact(map[string]any(p), 3000)), 3200), ""
}

// ---------------------------------------------------------------- conditions

// Eval evaluates a condition such as "destroys >= 0.8 && remote < 0.5 || team == billing" over the answers.
// A name is the answer's P (noul), score (score) or confidence (choice); "name == key" tests a choice. && binds
// tighter than ||; "*" or "" is true.
func Eval(cond string, ans map[string]Answer) (bool, error) {
	cond = strings.TrimSpace(cond)
	if cond == "" || cond == "*" {
		return true, nil
	}
	for _, or := range strings.Split(cond, "||") {
		all := true
		for _, term := range strings.Split(or, "&&") {
			ok, err := evalTerm(strings.TrimSpace(term), ans)
			if err != nil {
				return false, err
			}
			all = all && ok
		}
		if all {
			return true, nil
		}
	}
	return false, nil
}

var termRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*(>=|<=|==|!=|>|<)\s*([^\s]+)$`)

func evalTerm(term string, ans map[string]Answer) (bool, error) {
	m := termRe.FindStringSubmatch(term)
	if m == nil {
		return false, fmt.Errorf("bad condition %q (want NAME >= 0.8, NAME == key)", term)
	}
	a, ok := ans[m[1]]
	if !ok {
		return false, fmt.Errorf("condition names %q, which was not asked", m[1])
	}
	if num, err := strconv.ParseFloat(m[3], 64); err == nil {
		v := a.P()
		switch {
		case a.Type == "score" && a.Score != nil:
			v = *a.Score
		case a.Type == "choice" && a.Confidence != nil:
			v = *a.Confidence
		}
		switch m[2] {
		case ">=":
			return v >= num, nil
		case "<=":
			return v <= num, nil
		case ">":
			return v > num, nil
		case "<":
			return v < num, nil
		case "==":
			return v == num, nil
		case "!=":
			return v != num, nil
		}
	}
	switch m[2] {
	case "==":
		return a.Choice == m[3], nil
	case "!=":
		return a.Choice != m[3], nil
	}
	return false, fmt.Errorf("%q: %s needs a number", term, m[2])
}

// ---------------------------------------------------------------- running a plugin

// Decision is what a plugin decided for one event.
type Decision struct {
	Action string `json:"action"` // allow | deny | warn | block | context | exec
	Reason string `json:"reason,omitempty"`
	Raw    string `json:"raw,omitempty"` // exec: the command's stdout, emitted as is
}

// Run executes one plugin on a payload: state, questions, condition, decision. It never panics or exits; errors come
// back in rec["error"] and the decision is "allow" (fail open).
func (c Config) Run(name string, pl Plugin, event string, p Payload) (Decision, map[string]any) {
	t0 := time.Now()
	rec := map[string]any{"ts": time.Now().Format("2006-01-02T15:04:05"), "plugin": name, "event": event, "session": p.Str("session_id"),
		"cwd": p.Str("cwd"), "tool": p.Str("tool_name"), "mode": pl.Mode}
	fail := func(err error) (Decision, map[string]any) {
		rec["error"] = err.Error()
		return Decision{Action: "allow"}, rec
	}
	if cwd := p.Str("cwd"); cwd != "" {
		_ = os.Chdir(cwd) // the session's folder, so its "## Jev" section is the one sent
	}
	state, skip := StateOf(event, p, pl.Gate)
	if skip != "" {
		rec["skipped"] = skip
		return Decision{Action: "allow"}, rec
	}
	rec["state"] = cut(state, 2000)
	ans := map[string]Answer{}
	if strings.TrimSpace(pl.Ask) != "" {
		pname, prof, err := c.Profile(pl.Profile)
		if err != nil {
			return fail(err)
		}
		rec["profile"] = pname
		qs := map[string]any{}
		for _, n := range strings.Split(pl.Ask, ",") {
			n = strings.TrimSpace(n)
			q, ok := c.ResolveQuestion(prof, n)
			if !ok {
				return fail(fmt.Errorf("no question named %q", n))
			}
			qs[n] = q.Wire()
		}
		set := c.Settings(prof)
		Retries, LedgerOn = *set.Retries, *set.Ledger
		ans, err = Ask(prof, c.WithContext(prof, state), qs, time.Duration(*set.TimeoutS)*time.Second)
		if err != nil {
			return fail(err)
		}
		if len(prof.Headers) > 0 {
			rec["third_party"] = true // answered by a hosted endpoint: never use as training data (its terms)
		}
		short := map[string]any{}
		for k, a := range ans {
			switch a.Type {
			case "choice":
				short[k] = a.Choice
			case "score":
				if a.Score != nil {
					short[k] = *a.Score
				}
			default:
				short[k] = a.P()
			}
		}
		rec["answers"] = short
	}
	rec["ms"] = time.Since(t0).Milliseconds()
	if pl.Exec != "" {
		d, err := runExec(pl.Exec, map[string]any{"event": event, "plugin": name, "payload": p, "state": state, "answers": ans})
		if err != nil {
			return fail(err)
		}
		rec["decision"] = "exec"
		return d, rec
	}
	for _, act := range []struct{ name, cond string }{{"deny", pl.Deny}, {"block", pl.Block}, {"warn", pl.Warn}, {"context", pl.Context}} {
		if strings.TrimSpace(act.cond) == "" {
			continue
		}
		hit, err := Eval(act.cond, ans)
		if err != nil {
			return fail(err)
		}
		if hit {
			reason := fmt.Sprintf("jevx %s: %s (%s: %s)", name, summarize(ans), act.name, act.cond)
			if pl.Say != "" {
				reason = fill(pl.Say, ans)
			}
			d := Decision{Action: act.name, Reason: reason}
			rec["decision"], rec["reason"] = d.Action, d.Reason
			return d, rec
		}
	}
	rec["decision"] = "allow"
	return Decision{Action: "allow"}, rec
}

// fill replaces {{name}} in say with that answer (the key, the score or P).
func fill(say string, ans map[string]Answer) string {
	for k, a := range ans {
		v := fmt.Sprintf("%.2f", a.P())
		switch {
		case a.Type == "choice":
			v = a.Choice
		case a.Type == "score" && a.Score != nil:
			v = fmt.Sprintf("%.2f", *a.Score)
		}
		say = strings.ReplaceAll(say, "{{"+k+"}}", v)
	}
	return say
}

func summarize(ans map[string]Answer) string {
	var parts []string
	for _, k := range keys(ans) {
		a := ans[k]
		switch {
		case a.Type == "choice":
			parts = append(parts, fmt.Sprintf("%s=%s", k, a.Choice))
		case a.Type == "score" && a.Score != nil:
			parts = append(parts, fmt.Sprintf("%s %.2f", k, *a.Score))
		default:
			parts = append(parts, fmt.Sprintf("%s %.2f", k, a.P()))
		}
	}
	return strings.Join(parts, ", ")
}

func runExec(command string, in map[string]any) (Decision, error) {
	b, _ := json.Marshal(in)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Stdin = strings.NewReader(string(b))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return Decision{}, fmt.Errorf("exec %q: %v", command, err)
	}
	return Decision{Action: "exec", Raw: strings.TrimSpace(string(out))}, nil
}

// Output turns the decisions of every plugin that ran on an event into the JSON the agent expects (Claude Code hook
// protocol). Empty means "nothing to say". deny wins, then block, then the contexts and warnings are joined.
func Output(event string, ds []Decision, stopActive bool) string {
	var deny, block *Decision
	var ctx []string
	for i := range ds {
		d := ds[i]
		switch d.Action {
		case "exec":
			if d.Raw != "" {
				return d.Raw // the command's own protocol JSON
			}
		case "deny":
			if deny == nil {
				deny = &d
			}
		case "block":
			if block == nil {
				block = &d
			}
		case "warn":
			if event == "PreToolUse" {
				if deny == nil && block == nil {
					ctx = append(ctx, "ASK:"+d.Reason)
				}
			} else {
				ctx = append(ctx, "Warning: "+d.Reason)
			}
		case "context":
			ctx = append(ctx, d.Reason)
		}
	}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	switch event {
	case "PreToolUse":
		if deny != nil || block != nil {
			d := deny
			if d == nil {
				d = block
			}
			return js(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "permissionDecision": "deny", "permissionDecisionReason": d.Reason}})
		}
		for _, c := range ctx {
			if strings.HasPrefix(c, "ASK:") {
				return js(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "permissionDecision": "ask", "permissionDecisionReason": strings.TrimPrefix(c, "ASK:")}})
			}
		}
		return ""
	case "Stop", "SubagentStop":
		if (block != nil || deny != nil) && !stopActive {
			d := block
			if d == nil {
				d = deny
			}
			return js(map[string]any{"decision": "block", "reason": d.Reason + ". If this is already right, say so briefly with the evidence and stop."})
		}
		return ""
	}
	if block != nil || deny != nil {
		d := block
		if d == nil {
			d = deny
		}
		return js(map[string]any{"decision": "block", "reason": d.Reason})
	}
	if len(ctx) > 0 {
		return js(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "additionalContext": strings.Join(ctx, "\n")}})
	}
	return ""
}

// ---------------------------------------------------------------- settings.json entries

// HookEvents are the agent events that have at least one ENABLED plugin: one settings.json entry each. With nothing
// enabled there are no entries at all, so a disabled jevx never runs on an agent event (the owner's rule).
func (c Config) HookEvents() []string {
	seen := map[string]bool{}
	var out []string
	plugins := []Plugin{}
	for _, p := range c.AllPlugins() {
		plugins = append(plugins, p)
	}
	// folder plugins elsewhere: settings.json is global, so `jevx install` in one folder must not drop another repo's hook
	for _, d := range c.PluginDirs {
		m := map[string]Plugin{}
		if b, err := os.ReadFile(filepath.Join(d, "plugins.json")); err == nil && json.Unmarshal(b, &m) == nil {
			for _, p := range m {
				plugins = append(plugins, p)
			}
		}
	}
	sort.SliceStable(plugins, func(i, j int) bool { return plugins[i].On < plugins[j].On })
	for _, p := range plugins {
		if e := p.Event(); e != "" && p.Enabled && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// RememberPluginDir records the current folder's .jevx dir when it holds plugins (and forgets recorded dirs whose
// plugins.json is gone), so their enabled plugins keep their hooks wherever jevx runs next. It reports a change.
func (c *Config) RememberPluginDir() bool {
	var keep []string
	changed := false
	for _, d := range c.PluginDirs {
		if _, err := os.Stat(filepath.Join(d, "plugins.json")); err == nil {
			keep = append(keep, d)
		} else {
			changed = true
		}
	}
	if d := LocalDir(); d != "" && len(LocalPlugins()) > 0 {
		found := false
		for _, k := range keep {
			found = found || k == d
		}
		if !found {
			keep, changed = append(keep, d), true
		}
	}
	c.PluginDirs = keep
	return changed
}

// TempPath is true for a binary in a temporary folder (a test install, a scratchpad): a hook pointing there breaks every
// agent event once the folder is cleaned, so InstallHooks refuses it.
func TempPath(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	roots := []string{os.TempDir(), "/tmp", "/private/tmp", "/var/tmp", "/private/var/folders", "/var/folders"}
	for _, r := range roots {
		r = filepath.ToSlash(filepath.Clean(r))
		if r != "." && r != "/" && (p == r || strings.HasPrefix(p, r+"/")) {
			return true
		}
	}
	return strings.Contains(p, "/scratchpad/")
}

// hookSelf is the binary the hook entries run (a variable so tests can point it at a lasting path).
var hookSelf = func() string {
	self, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	return self
}

// hookCommand is the settings.json command for an event. On macOS and Linux it is a no-op when the binary is gone, so an
// uninstalled or moved jevx never makes every tool call fail.
func hookCommand(self, event string) string {
	if runtime.GOOS == "windows" {
		return `"` + self + `" hook run ` + event
	}
	return `[ -x "` + self + `" ] || exit 0; "` + self + `" hook run ` + event
}

// InstallHooks makes our settings.json entries match the ENABLED plugins (idempotent): one per event that has an
// enabled plugin, ours removed for every other event. Nothing enabled means no entries. It refuses a binary in a
// temporary folder.
func (c Config) InstallHooks(settings string) (added, removed int, err error) {
	m, err := readJSON(settings)
	if err != nil {
		return 0, 0, err
	}
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		m["hooks"] = hooks
	}
	want := map[string]bool{}
	for _, e := range c.HookEvents() {
		want[e] = true
	}
	self := hookSelf()
	if len(want) > 0 && TempPath(self) {
		return 0, 0, fmt.Errorf("not adding hooks that run %s: it is in a temporary folder, so every agent event would fail once it is cleaned. Install jevx to a lasting folder (e.g. ~/.local/bin) and run `jevx install --hooks` from there", self)
	}
	cmd := func(e string) string { return hookCommand(self, e) }
	for e := range want {
		list, _ := hooks[e].([]any)
		var keep []any
		current := false
		for _, x := range list {
			if !isOurs(x) {
				keep = append(keep, x)
				continue
			}
			if c := firstCommand(x); c == cmd(e) && !current {
				current = true
				keep = append(keep, x)
			} else {
				removed++ // an entry for an older binary path or the jevcli name: replaced below
			}
		}
		if !current {
			keep = append(keep, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd(e), "timeout": 20}}})
			added++
		}
		hooks[e] = keep
	}
	for e, v := range hooks {
		if want[e] {
			continue
		}
		list, _ := v.([]any)
		var keep []any
		for _, x := range list {
			if !isOurs(x) {
				keep = append(keep, x)
			}
		}
		if n := len(list) - len(keep); n > 0 {
			removed += n
			if len(keep) == 0 {
				delete(hooks, e)
			} else {
				hooks[e] = keep
			}
		}
	}
	if added+removed == 0 {
		return 0, 0, nil
	}
	return added, removed, writeJSON(settings, m)
}

func firstCommand(entry any) string {
	e, _ := entry.(map[string]any)
	hs, _ := e["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if c, _ := hm["command"].(string); c != "" {
			return c
		}
	}
	return ""
}

// RemoveHooks removes every jevx entry from settings.json, whatever the event.
func RemoveHooks(settings string) (int, error) {
	m, err := readJSON(settings)
	if err != nil {
		return 0, err
	}
	hooks, _ := m["hooks"].(map[string]any)
	n := 0
	for e, v := range hooks {
		list, _ := v.([]any)
		var keep []any
		for _, x := range list {
			if !isOurs(x) {
				keep = append(keep, x)
			}
		}
		if len(list) != len(keep) {
			n += len(list) - len(keep)
			if len(keep) == 0 {
				delete(hooks, e)
			} else {
				hooks[e] = keep
			}
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, writeJSON(settings, m)
}

// InstalledEvents lists the events that have a jevx entry in settings.json.
func InstalledEvents(settings string) []string {
	m, _ := readJSON(settings)
	hooks, _ := m["hooks"].(map[string]any)
	var out []string
	for e, v := range hooks {
		list, _ := v.([]any)
		for _, x := range list {
			if isOurs(x) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}
