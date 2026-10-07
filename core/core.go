// Package core is jevx: a vendor-neutral client for any System One (Jev-contract) decision endpoint (hosted,
// self-hosted or personal; set per profile), plus agent integration: a skill and config-gated hook templates that
// `jevx install` writes into Claude Code / Codex.
package core

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//go:embed questions.json
var questionsJSON []byte

// Questions are the texts jevx asks. The embedded pack is neutral ("the user"); a profile may point to its own pack (a
// model trained on specific wording should be asked in exactly that wording). Keys: noul accepts|wanted_more,
// choice reaction, score satisfaction.
type Questions struct {
	Noul   map[string]struct{ Instructions, True, False string } `json:"noul"`
	Choice map[string]struct {
		Instructions string            `json:"instructions"`
		Options      map[string]string `json:"options"`
	} `json:"choice"`
	Score map[string]struct {
		Instructions string   `json:"instructions"`
		Levels       []string `json:"levels"`
	} `json:"score"`
}

func questions(p Profile) Questions {
	p = p.Expanded()
	raw := questionsJSON
	if p.Questions != "" {
		b, err := os.ReadFile(expand(p.Questions))
		if err != nil {
			fmt.Fprintf(os.Stderr, "jevx: question pack %s: %v (using the built-in pack)\n", p.Questions, err)
		} else {
			raw = b
		}
	}
	var q Questions
	if err := json.Unmarshal(raw, &q); err != nil {
		panic(fmt.Sprintf("question pack: %v", err))
	}
	return q
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(Home(), p[2:])
	}
	return p
}

// ---------------------------------------------------------------- config

// Profile is one endpoint: url, model and request headers. Secrets are never stored: a header value names an env var
// ("Bearer $JEV_API_KEY") that is expanded only when a request is sent.
type Profile struct {
	URL       string            `json:"url"`
	Model     string            `json:"model"`
	Style     string            `json:"style,omitempty"`     // request shape: typesafe (flat {model,state,questions}, the default) or cloudflare ({model, input} envelope with a {result,...} reply)
	Headers   map[string]string `json:"headers,omitempty"`   // values may reference env vars ("Bearer $JEV_API_KEY"), expanded per request
	Questions string            `json:"questions,omitempty"` // optional question pack (JSON) for a model trained on specific wording
	Note      string            `json:"note,omitempty"`
	Endpoint  string            `json:"endpoint,omitempty"` // legacy name for url
	KeyEnv    string            `json:"key_env,omitempty"`  // legacy: same as headers {"Authorization": "Bearer $KEY_ENV"}
	Defaults  Settings          `json:"defaults,omitempty"` // overrides the global defaults for this profile
}

// Settings are every tunable knob. Unset fields fall back: flag > profile defaults > config defaults > Builtin.
type Settings struct {
	Yes           *float64 `json:"yes,omitempty"`            // noul P at or above: verdict yes
	No            *float64 `json:"no,omitempty"`             // noul P at or below: verdict no; in between: unsure
	MinConfidence *float64 `json:"min_confidence,omitempty"` // choice / score confidence below: unsure
	Parallel      *int     `json:"parallel,omitempty"`       // concurrent requests in a batch
	Retries       *int     `json:"retries,omitempty"`        // tries on 429 / 5xx / network errors
	TimeoutS      *int     `json:"timeout_s,omitempty"`      // per request
	Chunk         *int     `json:"chunk,omitempty"`          // questions per request (servers cap it)
	Ledger        *bool    `json:"ledger,omitempty"`         // log every call (no content) to calls.jsonl
	AcceptMin     *float64 `json:"accept_min,omitempty"`     // Stop hook: block when P(accepts) is below
	MoreMax       *float64 `json:"more_max,omitempty"`       // Stop hook: block when P(wanted_more) is above
	Cache         *bool    `json:"cache,omitempty"`          // reuse stored answers for the same endpoint, model, input and question
	CacheTTLDays  *int     `json:"cache_ttl_days,omitempty"` // a stored answer older than this is asked again
}

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }
func bp(v bool) *bool       { return &v }

// Builtin are the defaults when nothing is configured.
var Builtin = Settings{Yes: fp(0.8), No: fp(0.2), MinConfidence: fp(0.6), Parallel: ip(8), Retries: ip(3), TimeoutS: ip(60),
	Chunk: ip(32), Ledger: bp(true), AcceptMin: fp(0.35), MoreMax: fp(0.65), Cache: bp(true), CacheTTLDays: ip(7)}

// Over returns s with every field set in o taking precedence.
func (s Settings) Over(o Settings) Settings {
	if o.Yes != nil {
		s.Yes = o.Yes
	}
	if o.No != nil {
		s.No = o.No
	}
	if o.MinConfidence != nil {
		s.MinConfidence = o.MinConfidence
	}
	if o.Parallel != nil {
		s.Parallel = o.Parallel
	}
	if o.Retries != nil {
		s.Retries = o.Retries
	}
	if o.TimeoutS != nil {
		s.TimeoutS = o.TimeoutS
	}
	if o.Chunk != nil {
		s.Chunk = o.Chunk
	}
	if o.Ledger != nil {
		s.Ledger = o.Ledger
	}
	if o.AcceptMin != nil {
		s.AcceptMin = o.AcceptMin
	}
	if o.MoreMax != nil {
		s.MoreMax = o.MoreMax
	}
	if o.Cache != nil {
		s.Cache = o.Cache
	}
	if o.CacheTTLDays != nil {
		s.CacheTTLDays = o.CacheTTLDays
	}
	return s
}

// Settings resolves the effective settings for a profile: Builtin < config defaults < profile defaults.
func (c Config) Settings(p Profile) Settings { return Builtin.Over(c.Defaults).Over(p.Defaults) }

// Question is a named, reusable question: a System One question plus its own thresholds (optional).
type Question struct {
	Type          string   `json:"type"` // noul | choice | score
	Instructions  string   `json:"instructions"`
	Context       string   `json:"context,omitempty"`  // background for this question only, sent ahead of its instructions
	Criteria      any      `json:"criteria,omitempty"` // noul {true,false} | choice {key: desc} | score [levels]
	Yes           *float64 `json:"yes,omitempty"`
	No            *float64 `json:"no,omitempty"`
	MinConfidence *float64 `json:"min_confidence,omitempty"`
}

// Wire is the question as the API takes it.
func (q Question) Wire() map[string]any {
	instr := q.Instructions
	if c := strings.TrimSpace(os.ExpandEnv(q.Context)); c != "" {
		instr = "Context: " + c + "\n" + instr
	}
	m := map[string]any{"type": q.Type, "instructions": instr}
	if q.Criteria != nil {
		m["criteria"] = q.Criteria
	}
	return m
}

// Norm folds the legacy fields into url + headers.
func (p Profile) Norm() Profile {
	if p.URL == "" {
		p.URL = p.Endpoint
	}
	p.Endpoint = ""
	if p.KeyEnv != "" {
		if p.Headers == nil {
			p.Headers = map[string]string{}
		}
		if _, ok := p.Headers["Authorization"]; !ok {
			p.Headers["Authorization"] = "Bearer $" + p.KeyEnv
		}
		p.KeyEnv = ""
	}
	return p
}

type HookCfg struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`           // shadow (log only) | block (send the agent back)
	Profile string `json:"profile"`        // which endpoint judges the turn
	Gate    string `json:"gate,omitempty"` // "" = judge only turns that edited files with no check after; "all" = every turn
}

type Config struct {
	Default    string              `json:"default_profile"`
	Profiles   map[string]Profile  `json:"profiles"`
	Hooks      map[string]HookCfg  `json:"hooks,omitempty"` // legacy (v0.8): migrated into plugins["stop-judge"]
	Plugins    map[string]Plugin   `json:"plugins"`
	Defaults   Settings            `json:"defaults,omitempty"`
	Questions  map[string]Question `json:"questions,omitempty"`             // named questions: jevx ask NAME
	LocalFile  string              `json:"local_context_file,omitempty"`    // file holding the folder section (default AGENTS.md, then CLAUDE.md)
	LocalSect  string              `json:"local_context_section,omitempty"` // the section heading, global and folder (default "Jev"; any level)
	GlobalFile string              `json:"global_context_file,omitempty"`   // global files holding the section (comma list; default below)
	CacheDir   string              `json:"cache_dir,omitempty"`             // where stored answers live (default ~/.cache/jevx)
	PluginDirs []string            `json:"plugin_dirs,omitempty"`           // folder .jevx dirs with plugins: their enabled ones keep their hooks
}

func Home() string { h, _ := os.UserHomeDir(); return h }
func ConfigPath() string {
	if p := firstEnv("JEVX_CONFIG", "JEVCLI_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".config/jevx/config.json")
}
func DataDir() string { return filepath.Join(Home(), ".local/share/jevx") }

// firstEnv is the first set variable: the jevx name, then the old jevcli one.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// migrate moves a jevcli-era config and data dir to the jevx paths once (the tool was renamed; nothing is lost).
func migrate() {
	h := Home()
	for _, pair := range [][2]string{{".config/jevcli", ".config/jevx"}, {".local/share/jevcli", ".local/share/jevx"}} {
		old, now := filepath.Join(h, pair[0]), filepath.Join(h, pair[1])
		if _, err := os.Stat(now); err == nil {
			continue
		}
		if _, err := os.Stat(old); err == nil {
			_ = os.MkdirAll(filepath.Dir(now), 0o700)
			_ = os.Rename(old, now)
		}
	}
}

// BuiltinProfile is hosted Jev: works with no config at all once TYPESAFE_API_KEY is in the environment. A profile of the
// same name in the config overrides it; any other endpoint is a custom profile (jevx profile add).
const BuiltinName = "jev"

var BuiltinProfile = Profile{URL: "https://api.typesafe.ai/v1/systemone", Model: "jev-latest",
	Headers: map[string]string{"Authorization": "Bearer $TYPESAFE_API_KEY"}}

func DefaultConfig() Config {
	return Config{Default: BuiltinName, Profiles: map[string]Profile{}, Plugins: shipped()}
}

// LoadConfig reads the user's config; the built-in profiles only seed a config that does not exist yet.
func LoadConfig() Config {
	migrate()
	b, err := os.ReadFile(ConfigPath())
	if err != nil {
		return DefaultConfig()
	}
	var c Config
	if json.Unmarshal(b, &c) != nil {
		fmt.Fprintf(os.Stderr, "jevx: %s is not valid JSON; using built-in defaults\n", ConfigPath())
		return DefaultConfig()
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	for k, p := range c.Profiles {
		c.Profiles[k] = p.Norm()
	}
	if c.Plugins == nil {
		c.Plugins = shipped()
		if h, ok := c.Hooks["stop"]; ok { // v0.8 config: carry the Stop hook's state over
			pl := c.Plugins["stop-judge"]
			pl.Enabled, pl.Profile, pl.Gate = h.Enabled, h.Profile, h.Gate
			if h.Mode == "block" {
				pl.Mode = "act"
			}
			c.Plugins["stop-judge"] = pl
		}
		c.Hooks = nil
	}
	return c
}

// shipped is a fresh copy of the shipped plugins.
func shipped() map[string]Plugin {
	m := map[string]Plugin{}
	for k, p := range Shipped {
		m[k] = p
	}
	return m
}

func SaveConfig(c Config) error {
	_ = os.MkdirAll(filepath.Dir(ConfigPath()), 0o700)
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(ConfigPath(), append(b, '\n'), 0o600)
}

func (c Config) Profile(name string) (string, Profile, error) {
	if name == "" {
		name = c.Default
	}
	// No default chosen, or the empty "default" placeholder of older configs: use the built-in hosted Jev.
	if name == "" || (name == "default" && c.Profiles["default"].URL == "") {
		name = BuiltinName
	}
	ActiveProfile = name
	p, ok := c.Profiles[name]
	if !ok && name == BuiltinName {
		return name, BuiltinProfile, nil
	}
	if !ok {
		return name, p, fmt.Errorf("no profile %q (have: %s)", name, strings.Join(append(keys(c.Profiles), BuiltinName+" (built in)"), ", "))
	}
	if p.URL == "" {
		return name, p, fmt.Errorf("profile %q has no url: jevx profile add %s URL --model M [--header 'K: V']", name, name)
	}
	return name, p, nil
}

// NeedKeyHelp explains a missing key: for the built-in profile, how to get going; otherwise which variable to set.
func NeedKeyHelp(name string, p Profile) string {
	v := MissingEnv(p)
	if name == BuiltinName && v == "TYPESAFE_API_KEY" {
		return "set your Jev API key: export TYPESAFE_API_KEY=... (create one at https://console.typesafe.ai/keys). Or use another endpoint: jevx profile add NAME URL --model M --header 'Authorization: Bearer $YOUR_VAR' && jevx profile use NAME"
	}
	return fmt.Sprintf("profile %s needs %s in the environment: export %s=...", name, v, v)
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// WithContext prepends the configured standing context (global, then the profile's) to a state. Context is the user's
// own data in their config: jevx ships none.
// LocalDir is the nearest .jevx folder from the working directory up (like .git), or "" when there is none.
// It holds folder-level questions.json that apply to every call made inside that folder.
func LocalDir() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		for _, n := range []string{".jevx", ".jevcli"} { // .jevcli: the folder name before the rename
			if st, err := os.Stat(filepath.Join(d, n)); err == nil && st.IsDir() && d != Home() {
				return filepath.Join(d, n)
			}
		}
		up := filepath.Dir(d)
		if up == d {
			return ""
		}
		d = up
	}
}

// LocalSection is the heading of the folder context section: config local_context_section, else "Jev".
func (c Config) LocalSection() string {
	if c.LocalSect != "" {
		return c.LocalSect
	}
	return "Jev"
}

// GlobalContextFiles are the user's global agent instruction files searched for the section, first match wins:
// config global_context_file (comma list, ~ expanded), else $CLAUDE_CONFIG_DIR/CLAUDE.md, ~/.claude/CLAUDE.md,
// ~/.codex/AGENTS.md, ~/.agents/AGENTS.md, ~/AGENTS.md.
func (c Config) GlobalContextFiles() []string {
	var out []string
	for _, f := range strings.Split(c.GlobalFile, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, expand(f))
		}
	}
	if len(out) > 0 {
		return out
	}
	h := Home()
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		out = append(out, filepath.Join(d, "CLAUDE.md"))
	}
	return append(out, filepath.Join(h, ".claude/CLAUDE.md"), filepath.Join(h, ".codex/AGENTS.md"),
		filepath.Join(h, ".agents/AGENTS.md"), filepath.Join(h, "AGENTS.md"))
}

// GlobalContext is the section merged from every global agent file that has one (synced copies are deduplicated).
func (c Config) GlobalContext() (string, string) { return c.sections(c.GlobalContextFiles()) }

// sections reads the section from each file that exists and has one, and merges them without repeats: identical
// sections count once, and a line already sent is not sent again. It returns the text and the files it came from.
func (c Config) sections(files []string) (string, string) {
	var lines, from []string
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		sec, ok := Section(string(b), c.LocalSection())
		if !ok {
			continue
		}
		from = append(from, f)
		for _, l := range strings.Split(sec, "\n") {
			k := strings.Join(strings.Fields(l), " ")
			if k != "" && seen[k] {
				continue
			}
			seen[k] = true
			lines = append(lines, l)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), strings.Join(from, ", ")
}

// LocalContextFiles are the files searched for a "## Jev" section: config local_context_file, else AGENTS.md then
// CLAUDE.md.
func (c Config) LocalContextFiles() []string {
	var out []string
	for _, f := range strings.Split(c.LocalFile, ",") { // "AGENTS.md,CLAUDE.md,GEMINI.md": first file with the section wins
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		out = []string{"AGENTS.md", "CLAUDE.md"}
	}
	return out
}

// LocalContext is the folder context: the section of the nearest folder (working directory up, home skipped) whose
// AGENTS.md / CLAUDE.md has one; when both files have it, the two are merged without repeats.
func (c Config) LocalContext() (string, string) {
	d, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	for {
		if d != Home() {
			var fs []string
			for _, n := range c.LocalContextFiles() {
				fs = append(fs, filepath.Join(d, n))
			}
			if sec, from := c.sections(fs); from != "" {
				return sec, from
			}
		}
		up := filepath.Dir(d)
		if up == d {
			return "", ""
		}
		d = up
	}
}

// headingLevel is the level (1-6) of a markdown heading line, or 0. "#jev" (no space) counts too, but not "#!/bin/sh"
// or a bare "#".
func headingLevel(line string) (int, string) {
	t := strings.TrimSpace(line)
	n := len(t) - len(strings.TrimLeft(t, "#"))
	rest := strings.TrimSpace(t[n:])
	if n == 0 || n > 6 || rest == "" || strings.HasPrefix(rest, "!") {
		return 0, ""
	}
	return n, rest
}

// heading matches a heading whose text starts with name (any level, any case, with or without a space after the #:
// "# Jev", "##jev", "### Jev notes").
func heading(line, name string) (level int, ok bool) {
	lv, rest := headingLevel(line)
	if lv == 0 {
		return 0, false
	}
	h, n := strings.Fields(strings.ToLower(rest)), strings.Fields(strings.ToLower(name))
	if len(n) == 0 || len(h) < len(n) {
		return 0, false
	}
	return lv, strings.Join(h[:len(n)], " ") == strings.Join(n, " ")
}

// Section returns the body of the first section headed name: every line up to the next heading of the same or a higher
// level (fenced code blocks are not mistaken for headings).
func Section(md, name string) (string, bool) {
	lines := strings.Split(md, "\n")
	for i, l := range lines {
		lv, ok := heading(l, name)
		if !ok {
			continue
		}
		var body []string
		fence := false
		for _, m := range lines[i+1:] {
			if strings.HasPrefix(strings.TrimSpace(m), "```") {
				fence = !fence
			}
			if n, _ := headingLevel(m); !fence && n > 0 && n <= lv {
				break
			}
			body = append(body, m)
		}
		return strings.TrimSpace(strings.Join(body, "\n")), true
	}
	return "", false
}

// LocalQuestions are the named questions of the nearest .jevx folder (they override global ones with the same name).
func LocalQuestions() map[string]Question {
	m := map[string]Question{}
	if d := LocalDir(); d != "" {
		if b, err := os.ReadFile(filepath.Join(d, "questions.json")); err == nil {
			_ = json.Unmarshal(b, &m)
		}
	}
	return m
}

// NoContext skips the standing context (the global and folder "## Jev" sections) for this process: --no-context.
var NoContext bool

// MaxContextChars caps the standing + per-call context added to a state (~1k tokens), so it can never crowd the item out.
const MaxContextChars = 4000

// WithContext adds the context AFTER the state: the global section, the folder section, then the per-call extra.
// The item being judged always comes first, because the server keeps only the first max_len tokens (openjevx: 1024);
// context in front of it could push the item out of the model's view (found 2026-10-03).
func (c Config) WithContext(p Profile, state string, extra ...string) string {
	var parts []string
	if !NoContext {
		for _, get := range []func() (string, string){c.GlobalContext, c.LocalContext} {
			if sec, _ := get(); strings.TrimSpace(sec) != "" {
				parts = append(parts, strings.TrimSpace(sec))
			}
		}
	}
	for _, e := range extra { // per-call context, most specific last
		if e = strings.TrimSpace(e); e != "" {
			parts = append(parts, e)
		}
	}
	if len(parts) == 0 {
		return state
	}
	ctx := strings.Join(parts, "\n")
	// A JSON state keeps its structure: the context goes in as the LAST field (an existing one is kept first).
	trimmed := strings.TrimSpace(state)
	var obj map[string]any
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &obj) == nil {
		old, hasOld := obj["context"].(string)
		if hasOld && old != "" {
			ctx = ctx + "\n" + old
		}
		cb, _ := json.Marshal(capContext(ctx))
		if _, exists := obj["context"]; !exists && strings.HasSuffix(trimmed, "}") {
			body := strings.TrimSpace(strings.TrimSuffix(trimmed, "}"))
			sep := ","
			if strings.HasSuffix(body, "{") {
				sep = ""
			}
			return body + sep + `"context":` + string(cb) + "}" // original fields, in their original order, first
		}
		delete(obj, "context") // rare: the state carried its own context; re-emit it with the merged one last
		b, _ := json.Marshal(obj)
		body := strings.TrimSuffix(string(b), "}")
		if body != "{" {
			body += ","
		}
		return body + `"context":` + string(cb) + "}"
	}
	return state + "\n\nContext: " + capContext(ctx)
}

func capContext(ctx string) string {
	if r := []rune(ctx); len(r) > MaxContextChars {
		return string(r[:MaxContextChars]) + " …[context cut]"
	}
	return ctx
}

// ---------------------------------------------------------------- System One client

type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Probability   *float64           `json:"probability,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

var ErrNeedKey = errors.New("need key")

var envRef = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// fields are every profile value that may hold $VAR references (expanded at runtime, stored raw).
func (p Profile) fields() []string {
	f := []string{p.URL, p.Model, p.Questions}
	for _, v := range p.Headers {
		f = append(f, v)
	}
	return f
}

// MissingEnv returns the first env var any profile value references that is not set ("" = all set).
func MissingEnv(p Profile) string {
	for _, v := range p.fields() {
		for _, m := range envRef.FindAllStringSubmatch(v, -1) {
			if os.Getenv(m[1]) == "" {
				return m[1]
			}
		}
	}
	return ""
}

// Expanded returns the profile with every $VAR expanded from the environment (runtime only; never saved).
func (p Profile) Expanded() Profile {
	p = p.Norm()
	e := os.ExpandEnv
	p.URL, p.Model, p.Questions = e(p.URL), e(p.Model), e(p.Questions)
	h := map[string]string{}
	for k, v := range p.Headers {
		h[k] = e(v)
	}
	p.Headers = h
	return p
}

// Ask posts one System One request to the profile's url with its headers (env vars expanded here, never stored).
func Ask(p Profile, state string, qs map[string]any, timeout time.Duration) (map[string]Answer, error) {
	ans, raw, err := AskRaw(p, state, qs, timeout)
	if err == nil {
		LastRaw = raw
	}
	return ans, err
}

// Retries and LedgerOn are set from the effective Settings by the CLI before calls are made.
var (
	Retries  = 3
	LedgerOn = true
)

// RedactNotice receives one line, once per process, when a hosted call had something redacted: otherwise a question like
// "does this contain a password?" silently comes back unsure, because the model never saw the password.
var RedactNotice io.Writer = os.Stderr

var redactNotice sync.Once

// NoteRedacted tells the caller, once per process, what was redacted before a hosted call: counts per category, never
// the values. kinds comes from RedactedKinds.
func NoteRedacted(kinds map[string]int) {
	if len(kinds) > 0 {
		redactNotice.Do(func() {
			fmt.Fprintf(RedactNotice, "jevx: redacted %s before this call to a hosted endpoint, so the model judged the text without them. Each was present in the text; to judge the raw text, use a local endpoint.\n", RedactionSummary(kinds))
		})
	}
}

// redactLabels names each Scrub marker for people. user@host in a connection URL matches the email rule, so the
// label says "email-shaped" rather than claiming it was an email address.
var redactLabels = map[string]string{
	"credentials": "password (in a URL)", "secret": "secret", "email": "email-shaped (user@host)", "phone": "phone number",
	"private-key": "private key", "api-key": "API key", "github-token": "GitHub token", "slack-token": "Slack token",
	"aws-key": "AWS key", "google-key": "Google key", "jwt": "JWT",
}

var redactMarker = regexp.MustCompile(`\[REDACTED:([a-z-]+)\]`)

// RedactedKinds counts the redaction markers Scrub added to after that were not already in before, per category.
func RedactedKinds(before, after string) map[string]int {
	had := map[string]int{}
	for _, m := range redactMarker.FindAllStringSubmatch(before, -1) {
		had[m[1]]++
	}
	kinds := map[string]int{}
	for _, m := range redactMarker.FindAllStringSubmatch(after, -1) {
		if had[m[1]] > 0 {
			had[m[1]]--
			continue
		}
		kinds[m[1]]++
	}
	return kinds
}

// RedactionSummary is "1 password (in a URL), 1 email-shaped (user@host)": categories in a stable order.
func RedactionSummary(kinds map[string]int) string {
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		label := redactLabels[k]
		if label == "" {
			label = k
		}
		parts = append(parts, fmt.Sprintf("%d %s", kinds[k], label))
	}
	return strings.Join(parts, ", ")
}

// redactKinds scrubs the state and the questions as a hosted call does and returns what it removed, per category.
func redactKinds(state string, qs map[string]any) (string, map[string]any, map[string]int) {
	sendState, _ := Scrub(state)
	sendQs, _ := redactQuestions(qs)
	kinds := RedactedKinds(state, sendState)
	before, _ := json.Marshal(qs)
	after, _ := json.Marshal(sendQs)
	for k, n := range RedactedKinds(string(before), string(after)) {
		kinds[k] += n
	}
	return sendState, sendQs, kinds
}

// Redactions is what a call to p would redact, per category (empty for a local endpoint). A cached answer to a hosted
// call came from redacted text too, so its caller is told the same way.
func Redactions(p Profile, state string, qs map[string]any) map[string]int {
	if !Hosted(p.Expanded()) {
		return nil
	}
	_, _, kinds := redactKinds(state, qs)
	return kinds
}

// AskRaw is Ask without the shared LastRaw: it returns the response body too, so concurrent callers stay race-free.
// It retries 429 / 5xx / network errors with backoff, rejects a reply that does not answer exactly the questions asked
// or carries out-of-range probabilities (fail closed), and appends one line per call to the ledger.
func AskRaw(p Profile, state string, qs map[string]any, timeout time.Duration) (map[string]Answer, []byte, error) {
	if MissingEnv(p.Norm()) != "" {
		return nil, nil, ErrNeedKey
	}
	p = p.Expanded()
	// Before any HOSTED call, scrub secrets, emails and phones from the state and question text (CEO 2026-10-03).
	redacted := 0
	sendState, sendQs := state, qs
	if Hosted(p) {
		var kinds map[string]int
		sendState, sendQs, kinds = redactKinds(state, qs)
		for _, n := range kinds {
			redacted += n
		}
		NoteRedacted(kinds)
	}
	core := map[string]any{"state": sendState, "questions": sendQs}
	// Cloudflare's universal /ai/run is an envelope: {"model": ..., "input": {...}} with a
	// {"result": ..., "success": ...} reply (Cloudflare REST 2026-10). Detect and adapt; the
	// System One contract stays the default everywhere else.
	isCF := p.Style == "cloudflare" || (p.Style == "" && strings.Contains(p.URL, "/ai/run"))
	var body []byte
	if isCF {
		body, _ = json.Marshal(map[string]any{"model": p.Model, "input": core})
	} else {
		core["model"] = p.Model
		body, _ = json.Marshal(core)
	}
	t0 := time.Now()
	var raw []byte
	var err error
	for try := 0; try < max(1, Retries); try++ {
		if try > 0 {
			time.Sleep(time.Duration(try*try) * 500 * time.Millisecond)
		}
		var retry bool
		raw, retry, err = post(p, body, timeout)
		if err == nil || !retry {
			break
		}
	}
	if isCF && err == nil {
		var env struct {
			Result  json.RawMessage `json:"result"`
			Success bool            `json:"success"`
			Errors  []struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if e := json.Unmarshal(raw, &env); e != nil || !env.Success {
			msg := "cloudflare envelope: unknown error"
			if e == nil && len(env.Errors) > 0 {
				msg = env.Errors[0].Message
			} else if e == nil {
				msg = strings.TrimSpace(string(raw[:min(200, len(raw))]))
			}
			err = fmt.Errorf("%s: %s", p.URL, msg)
		} else {
			raw = env.Result
			// The Workers AI /ai/run reply nests once more: {"state": "Completed", "result": {model, answers}}.
			// Any other state (queued, running, failed) is not an answer; say so instead of reading it as one.
			var inner struct {
				State  string          `json:"state"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(raw, &inner) == nil && inner.State != "" {
				if inner.State != "Completed" {
					err = fmt.Errorf("%s: cloudflare run state %q, not Completed", p.URL, inner.State)
				} else {
					raw = inner.Result
				}
			}
		}
	}
	var out struct {
		Model   string            `json:"model"`
		Answers map[string]Answer `json:"answers"`
		Usage   map[string]any    `json:"usage"`
	}
	if err == nil {
		if e := json.Unmarshal(raw, &out); e != nil {
			err = fmt.Errorf("unreadable answer: %v", e)
		} else {
			err = validate(qs, out.Answers)
		}
	}
	ledger(p, qs, out.Model, out.Usage, time.Since(t0), err, redacted)
	if err != nil {
		return nil, nil, err
	}
	return out.Answers, raw, nil
}

func post(p Profile, body []byte, timeout time.Duration) ([]byte, bool, error) {
	req, _ := http.NewRequest("POST", p.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t := strings.TrimSpace(string(raw))
		return nil, resp.StatusCode == 429 || resp.StatusCode >= 500, fmt.Errorf("%s: HTTP %d: %s", p.URL, resp.StatusCode, t[:min(300, len(t))])
	}
	return raw, false, nil
}

// validate fails closed: every question answered and nothing extra, probabilities in [0,1] summing to ~1, a choice
// that is one of the offered keys.
func validate(qs map[string]any, ans map[string]Answer) error {
	if len(ans) != len(qs) {
		return fmt.Errorf("invalid answer: %d answers for %d questions", len(ans), len(qs))
	}
	for k, q := range qs {
		a, ok := ans[k]
		if !ok {
			return fmt.Errorf("invalid answer: no answer for %q", k)
		}
		if pr := a.P(); a.Type == "noul" && (pr < 0 || pr > 1) {
			return fmt.Errorf("invalid answer: %q probability %v", k, pr)
		}
		if len(a.Probabilities) > 0 {
			sum := 0.0
			for _, v := range a.Probabilities {
				if v < 0 || v > 1 {
					return fmt.Errorf("invalid answer: %q probability %v", k, v)
				}
				sum += v
			}
			if sum < 0.98 || sum > 1.02 {
				return fmt.Errorf("invalid answer: %q probabilities sum to %.3f", k, sum)
			}
		}
		if a.Type == "choice" {
			if crit, _ := q.(map[string]any)["criteria"].(map[string]string); crit != nil {
				if _, ok := crit[a.Choice]; !ok {
					return fmt.Errorf("invalid answer: %q chose %q, not an offered key", k, a.Choice)
				}
			}
		}
	}
	return nil
}

// ledger appends one JSON line per call to ~/.local/share/jevx/calls.jsonl: profile, caller (the calling agent's cwd,
// or JEVX_CALLER), host, hosted, model, question count and hash, tokens, latency, how many items were redacted, error.
// Never the state, the question text or a header. JEVX_LEDGER=off disables it.
func ledger(p Profile, qs map[string]any, model string, usage map[string]any, d time.Duration, err error, redacted int) {
	if !LedgerOn || firstEnv("JEVX_LEDGER", "JEVCLI_LEDGER") == "off" {
		return
	}
	host := p.URL
	if u, e := url.Parse(p.URL); e == nil {
		host = u.Host
	}
	qb, _ := json.Marshal(qs)
	h := sha256.Sum256(qb)
	row := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339), "host": host, "model": model, "questions": len(qs),
		"qhash": hex.EncodeToString(h[:6]), "ms": d.Milliseconds(), "usage": usage,
		"profile": ActiveProfile, "caller": Caller(), "hosted": Hosted(p), "redacted": redacted}
	if err != nil {
		row["error"] = trimErr(err.Error())
	}
	b, _ := json.Marshal(row)
	path := filepath.Join(Home(), ".local/share/jevx/calls.jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); e == nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
}

// ActiveProfile is the name of the profile the last Config.Profile call resolved (for the ledger).
var ActiveProfile string

// Caller identifies who is asking, for spend by agent: JEVX_CALLER if set, else the working directory.
func Caller() string {
	if c := firstEnv("JEVX_CALLER"); c != "" {
		return c
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

func trimErr(s string) string {
	if i := strings.Index(s, ": HTTP "); i >= 0 {
		s = s[i+2:]
	}
	return s[:min(120, len(s))]
}

// LastRaw is the full body of the last successful response (model, answers, usage, id), for commands that print it as is.
var LastRaw []byte

// P returns an answer's probability of yes (noul / Vercel-style "probability").
func (a Answer) P() float64 {
	if a.Noul != nil {
		return *a.Noul
	}
	if a.Probability != nil {
		return *a.Probability
	}
	return -1
}

// NoulQ and friends build question objects from the profile's question pack.
func NoulQ(p Profile, id string) map[string]any {
	q := questions(p).Noul[id]
	return map[string]any{"type": "noul", "instructions": q.Instructions, "criteria": map[string]string{"true": q.True, "false": q.False}}
}
func ChoiceQ(p Profile, id string) map[string]any {
	q := questions(p).Choice[id]
	return map[string]any{"type": "choice", "instructions": q.Instructions, "criteria": q.Options}
}
func ScoreQ(p Profile, id string) map[string]any {
	q := questions(p).Score[id]
	return map[string]any{"type": "score", "instructions": q.Instructions, "criteria": q.Levels}
}

// ---------------------------------------------------------------- the turn: request + proposal, in the training format

var secretRx = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-(?:proj-|ant-|live-|test-)?[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{20,}`),
	regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|_PW|PASSWD)[A-Z0-9_]*\s*[=:]\s*)['"]?[^\s'"$]{8,}`),
}

// Redact removes common secret shapes (private keys, API tokens, JWTs, KEY=value) before anything leaves the machine.
func Redact(t string) string {
	for _, rx := range secretRx {
		t = rx.ReplaceAllStringFunc(t, func(m string) string {
			sub := rx.FindStringSubmatch(m)
			if len(sub) > 1 {
				return sub[1] + "[SECRET]"
			}
			return "[SECRET]"
		})
	}
	return t
}

func cut(t string, n int) string {
	t = Redact(t)
	if len([]rune(t)) <= n {
		return t
	}
	return string([]rune(t)[:n]) + " …[cut]"
}

// State renders one agent turn as the premise: request, agent text, actions, trimmed to ~440 tokens by a 1600-byte
// budget (actions first, then the agent text, then the request; the server also truncates a long premise). Bytes, not
// characters, approximate tokens for every script; cuts still happen on character boundaries.
func State(req, text string, actions []string) string {
	const budget = 1600
	if text == "" {
		text = "(no text)"
	}
	acts := append([]string(nil), actions...)
	total := len(actions)
	for i := 0; i < 40; i++ {
		s := "Request: " + req + "\nAgent: " + text + "\n"
		if len(acts) > 0 {
			s += "Actions:\n- " + strings.Join(acts, "\n- ")
		} else {
			s += "Actions: none"
		}
		if len(s) <= budget { // bytes on purpose: closer to tokens than characters for non-Latin text, so the server
			return s // (which cuts a long premise from the END, where the actions are) gets a premise it keeps whole
		}
		switch {
		case len(acts) > 3:
			acts = append(acts[:len(acts)/2], fmt.Sprintf("(+%d more)", total-len(acts)/2))
		case len([]rune(text)) > 400:
			text = string([]rune(text)[:len([]rune(text))*7/10]) + " …"
		case len([]rune(req)) > 300:
			req = string([]rune(req)[:len([]rune(req))*7/10]) + " …"
		default: // shorten each action, rebuild, then cut by character (never half a multi-byte one)
			for j := range acts {
				acts[j] = firstRunes(acts[j], 120)
			}
			s = "Request: " + req + "\nAgent: " + text + "\nActions:\n- " + strings.Join(acts, "\n- ")
			return firstBytes(s, budget)
		}
	}
	return "Request: " + req + "\nAgent: " + text
}

var (
	wrapperRx   = regexp.MustCompile(`^\s*<(command-name|command-message|local-command|system-reminder|task-notification|cross-session-message|bash-)`)
	interruptRx = regexp.MustCompile(`^\[Request interrupted by user`)
)

// firstBytes is at most n bytes of s, ending on a character boundary.
func firstBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// firstRunes is the first n characters of s, never half a multi-byte one.
func firstRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func actionOf(name string, in map[string]any) string {
	s := func(k string) string { v, _ := in[k].(string); return v }
	switch name {
	case "Edit", "Write", "NotebookEdit":
		d := s("new_string")
		if d == "" {
			d = s("content")
		}
		return fmt.Sprintf("%s %s: %s", name, s("file_path"), firstRunes(d, 400))
	case "Bash":
		return "Bash: " + s("command")
	case "Skill":
		return fmt.Sprintf("Skill %s %s", s("skill"), s("args"))
	case "Agent", "SendMessage":
		p := s("prompt")
		if p == "" {
			p = s("message")
		}
		d := s("description")
		if d == "" {
			d = s("to")
		}
		return fmt.Sprintf("%s %s: %s", name, d, firstRunes(p, 200))
	}
	b, _ := json.Marshal(in)
	return name + " " + firstRunes(string(b), 200)
}

// LastTurn reads a Claude Code transcript (JSONL) and returns the last genuine human request with everything the agent
// said and did after it. Only the tail of a long transcript is read.
// skipTurn is the local pre-gate for the Stop hook: a turn that changed no files, or ran a test / build / check after
// its last edit, is not worth a call. It returns the reason to skip, or "" to judge the turn.
func skipTurn(acts []string) string {
	last := -1
	for i, a := range acts {
		for _, t := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
			if strings.HasPrefix(a, t+" ") || strings.HasPrefix(a, t+":") {
				last = i
			}
		}
	}
	if last < 0 {
		return "no file changes"
	}
	for _, a := range acts[last+1:] {
		if strings.HasPrefix(a, "Bash") {
			l := strings.ToLower(a)
			for _, w := range []string{"test", "build", "vet", "lint", "check", "pytest", "tsc", "cargo", "make"} {
				if strings.Contains(l, w) {
					return "checked after the last edit"
				}
			}
		}
	}
	return ""
}

func LastTurn(path string) (req, text string, actions []string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if st, _ := f.Stat(); st != nil && st.Size() > 4_000_000 {
		_, _ = f.Seek(st.Size()-4_000_000, io.SeekStart)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r struct {
			Type        string          `json:"type"`
			IsSidechain bool            `json:"isSidechain"`
			IsMeta      bool            `json:"isMeta"`
			Message     json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.IsSidechain {
			continue
		}
		var m struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(r.Message, &m)
		var str string
		var blocks []map[string]any
		isStr := json.Unmarshal(m.Content, &str) == nil
		if !isStr {
			_ = json.Unmarshal(m.Content, &blocks)
		}
		switch {
		case r.Type == "user" && !r.IsMeta:
			t, human := str, isStr
			if !isStr {
				human = true
				var parts []string
				for _, b := range blocks {
					if b["type"] == "tool_result" {
						human = false
						break
					}
					if b["type"] == "text" {
						s, _ := b["text"].(string)
						parts = append(parts, s)
					}
				}
				t = strings.Join(parts, "\n")
			}
			if !human || wrapperRx.MatchString(t) || interruptRx.MatchString(t) {
				continue
			}
			req, text, actions = t, "", nil
		case r.Type == "assistant" && req != "":
			for _, b := range blocks {
				switch b["type"] {
				case "text":
					if s, _ := b["text"].(string); strings.TrimSpace(s) != "" {
						text = s
					}
				case "tool_use":
					name, _ := b["name"].(string)
					in, _ := b["input"].(map[string]any)
					actions = append(actions, cut(actionOf(name, in), 400))
				}
			}
		}
	}
	if req == "" || (text == "" && len(actions) == 0) {
		return
	}
	if len(actions) > 25 { // keep the end of the turn: the last edits and checks matter most
		actions = actions[len(actions)-25:]
	}
	return cut(req, 2500), cut(text, 3000), actions, true
}

// ---------------------------------------------------------------- the Stop hook

// HookInput is what Claude Code sends a Stop hook on stdin.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	StopHookActive bool   `json:"stop_hook_active"`
}

func AppendLog(rec map[string]any) {
	_ = os.MkdirAll(DataDir(), 0o700)
	f, err := os.OpenFile(filepath.Join(DataDir(), "verdicts.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(rec)
	_, _ = f.Write(append(b, '\n'))
}

// Detach re-runs this binary with args in its own session, stdin from a file, so a shadow hook costs the agent nothing.
func Detach(args []string, stdinFile string) error {
	self, _ := os.Executable()
	cmd := exec.Command(self, args...)
	in, err := os.Open(stdinFile)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, nil, nil
	detachAttr(cmd)
	return cmd.Start()
}

// ---------------------------------------------------------------- agent settings (Claude Code hooks)

const HookMarker = " hook run "

// isOurHook matches our hook command on every OS: `/x/jevx hook run stop`, `"C:\x\jevx.exe" hook run stop`.
func isOurHook(c string) bool {
	return (strings.Contains(c, "jevx") || strings.Contains(c, "jevcli")) && strings.Contains(c, HookMarker)
}

// SettingsPath is the REAL Claude Code user settings file (~/.claude/settings.json may be a symlink).
func SettingsPath() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(Home(), ".claude")
	}
	p := filepath.Join(dir, "settings.json")
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func readJSON(p string) (map[string]any, error) {
	m := map[string]any{}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func writeJSON(p string, m map[string]any) error {
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if _, err := os.Stat(p); err == nil {
		b, _ := os.ReadFile(p)
		_ = os.WriteFile(fmt.Sprintf("%s.bak-jevx-%s-%d", p, time.Now().Format("20060102-150405"), os.Getpid()), b, 0o600)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp-jevx"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	var check map[string]any
	if b2, _ := os.ReadFile(tmp); json.Unmarshal(b2, &check) != nil {
		return errors.New("refusing to write invalid JSON")
	}
	return os.Rename(tmp, p)
}

func isOurs(entry any) bool {
	e, _ := entry.(map[string]any)
	hs, _ := e["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if c, _ := hm["command"].(string); isOurHook(c) {
			return true
		}
	}
	return false
}
