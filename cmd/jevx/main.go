// jevx: ask any System One (Jev-contract) endpoint what the user would decide, and wire it into agents.
//
//	jevx ask QUESTION [--context TEXT|-] [--option KEY=DESC ... | --true DESC --false DESC] [--profile P] [--json]
//	jevx query --state TEXT|JSON|@file|- --noul NAME=INSTRUCTIONS ... [--choice NAME="INSTR|k=desc;k2=desc"] [--score NAME="INSTR|L0;L1;L2"] [--raw]
//	jevx judge --request TEXT --proposal TEXT [--action LINE ...] [--profile P] [--json]
//	jevx install [--no-skill] [--no-hook]     skill into Claude Code + Codex; hook entries only for enabled plugins
//	jevx uninstall                            remove the skill and jevx's hook entries
//	jevx hook enable|disable stop | mode shadow|block | profile NAME | status | review [N] | run stop
//	jevx config show | set-endpoint PROFILE URL [MODEL] | default PROFILE
//	jevx profile list | types | add NAME URL [--type T] [--model M] [--header 'K: V' ...] [--questions FILE] | use NAME | remove NAME | show NAME
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"

	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"time"

	"github.com/muthuishere/jevx/core"
)

// version is set at release time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

//go:embed skill
var skillFS embed.FS

// die exits 4: an error is never a "no" (1) or "unsure" (3), so `if jevx is ...` cannot mistake an outage for an answer.
func die(f string, a ...any) { fmt.Fprintf(os.Stderr, "jevx: "+f+"\n", a...); os.Exit(4) }

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// ask posts one System One request; a profile's API key comes from the environment variable it names.
func ask(name string, p core.Profile, state string, qs map[string]any, extra ...string) map[string]core.Answer {
	ans, err := core.Ask(p, core.LoadConfig().WithContext(p, state, extra...), qs, 60*time.Second)
	if errors.Is(err, core.ErrNeedKey) {
		die("%s", core.NeedKeyHelp(name, p))
	}
	if err != nil {
		die("%v", err)
	}
	return ans
}

func pr(f string, a ...any) { fmt.Printf(f+"\n", a...) }

func cmdJudge(args []string) {
	fs := flag.NewFlagSet("judge", flag.ExitOnError)
	reqT := fs.String("request", "", "the user's request")
	prop := fs.String("proposal", "", "the agent's final message")
	prof := fs.String("profile", "", "endpoint profile")
	js := fs.Bool("json", false, "raw answers")
	var acts, ctxs multi
	fs.Var(&acts, "action", "one tool call as a short line (repeat)")
	fs.Var(&ctxs, "context", "background for this call: TEXT or @file (repeat)")
	fs.BoolVar(&core.NoContext, "no-context", false, "skip the ## Jev sections of the global and folder agent files")
	_ = fs.Parse(args)
	if *reqT == "" || *prop == "" {
		die("usage: jevx judge --request TEXT --proposal TEXT [--action LINE ...]")
	}
	cfg := core.LoadConfig()
	name, p, err := cfg.Profile(*prof)
	if err != nil {
		die("%v", err)
	}
	set := cfg.Settings(p)
	core.Retries, core.LedgerOn = *set.Retries, *set.Ledger
	var a2 []string
	for _, a := range acts {
		a2 = append(a2, core.Redact(a))
	}
	st := core.State(core.Redact(*reqT), core.Redact(*prop), a2)
	ans := ask(name, p, st, map[string]any{"accepts": core.NoulQ(p, "accepts"), "wanted_more": core.NoulQ(p, "wanted_more"),
		"reaction": core.ChoiceQ(p, "reaction"), "satisfaction": core.ScoreQ(p, "satisfaction")}, callCtx(ctxs)...)
	if *js {
		b, _ := json.Marshal(ans)
		pr("%s", b)
		return
	}
	r, s := ans["reaction"], ans["satisfaction"]
	rp, rc, sv := 0.0, 0.0, 0.0
	if r.Probabilities != nil {
		rp = r.Probabilities[r.Choice]
	}
	if r.Confidence != nil {
		rc = *r.Confidence
	}
	if s.Score != nil {
		sv = *s.Score
	}
	pr("accept        %.2f\nwanted more   %.2f\nreaction      %s (%.2f, confidence %.2f)\nsatisfaction  %.1f / 4  (%s)",
		ans["accepts"].P(), ans["wanted_more"].P(), r.Choice, rp, rc, sv, name)
}

// ------------------------------------------------------------------ install: skill + hooks

// skillDirs are the global skill dirs: Claude Code (~/.claude, $CLAUDE_CONFIG_DIR) and the cross-agent ~/.agents are
// created if missing; Codex only if ~/.codex exists.
func skillDirs() []string {
	h := core.Home()
	var out []string
	for _, d := range []string{filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "skills"), filepath.Join(h, ".claude/skills"), filepath.Join(h, ".agents/skills"), filepath.Join(h, ".codex/skills")} {
		if strings.HasPrefix(d, "skills") { // CLAUDE_CONFIG_DIR unset
			continue
		}
		if strings.HasSuffix(d, filepath.Join(".codex", "skills")) {
			if _, err := os.Stat(filepath.Dir(d)); err != nil {
				continue
			}
		}
		_ = os.MkdirAll(d, 0o755)
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		if !contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func cmdInstall(args []string) {
	skills, hooks := parts("install", args)
	noSkill, noHook := !skills, !hooks
	cfg := core.LoadConfig()
	if _, err := os.Stat(core.ConfigPath()); err != nil {
		if err := core.SaveConfig(cfg); err != nil {
			die("%v", err)
		}
		pr("config   %s (hooks disabled)", core.ConfigPath())
	}
	if !noSkill {
		for _, d := range skillDirs() {
			dst := filepath.Join(d, "jevx")
			_ = os.RemoveAll(filepath.Join(d, "jevcli")) // the skill's name before the rename
			_ = os.RemoveAll(dst)                        // a reinstall leaves no stale files from an older skill
			err := iofs.WalkDir(skillFS, "skill", func(p string, e iofs.DirEntry, _ error) error {
				out := filepath.Join(dst, strings.TrimPrefix(strings.TrimPrefix(p, "skill"), "/"))
				if e.IsDir() {
					return os.MkdirAll(out, 0o755)
				}
				b, _ := skillFS.ReadFile(p)
				return os.WriteFile(out, b, 0o644)
			})
			if err != nil {
				die("%v", err)
			}
			pr("skill    %s", dst)
		}
	}
	if !noHook {
		if cfg.RememberPluginDir() {
			_ = core.SaveConfig(cfg)
		}
		added, removed, err := cfg.InstallHooks(core.SettingsPath())
		if err != nil {
			die("%v", err)
		}
		on := 0
		for _, p := range cfg.AllPlugins() {
			if p.Enabled {
				on++
			}
		}
		if len(cfg.HookEvents()) == 0 {
			pr("hooks    none: 0 of %d plugins enabled, so nothing runs on agent events (%d removed in %s). Enabling a plugin adds its hook: jevx plugin list",
				len(cfg.AllPlugins()), removed, core.SettingsPath())
			return
		}
		pr("hooks    %s: %d added, %d removed in %s; %d of %d plugins enabled (jevx plugin list)",
			strings.Join(cfg.HookEvents(), ", "), added, removed, core.SettingsPath(), on, len(cfg.AllPlugins()))
	}
}

// parts reads --skills / --hooks: both when neither is given. --no-skill / --no-hook still work.
func parts(cmd string, args []string) (skills, hooks bool) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	s, h := fs.Bool("skills", false, "only the agent skill"), fs.Bool("hooks", false, "only the hooks: sync Claude Code entries with enabled plugins")
	ns, nh := fs.Bool("no-skill", false, "skip the agent skill"), fs.Bool("no-hook", false, "skip the hooks")
	_ = fs.Parse(args)
	skills, hooks = *s || !*h, *h || !*s
	return skills && !*ns, hooks && !*nh
}

func cmdUninstall(args []string) {
	skills, hooks := parts("uninstall", args)
	if !skills {
		goto hook
	}
	for _, d := range skillDirs() {
		for _, n := range []string{"jevx", "jevcli"} {
			if _, err := os.Stat(filepath.Join(d, n, "SKILL.md")); err == nil {
				_ = os.RemoveAll(filepath.Join(d, n))
				pr("skill removed  %s", filepath.Join(d, n))
			}
		}
	}
hook:
	if !hooks {
		return
	}
	n, err := core.RemoveHooks(core.SettingsPath())
	if err != nil {
		die("%v", err)
	}
	pr("hook entries removed: %d (other hooks untouched)", n)
}

// ------------------------------------------------------------------ hooks

// cmdHook: `hook run EVENT` is what settings.json calls; the rest are conveniences over `plugin`.
func cmdHook(args []string) {
	if len(args) == 0 {
		die("usage: jevx hook run EVENT | status | review [N] | enable|disable stop  (plugins: jevx plugin)")
	}
	switch args[0] {
	case "run":
		if len(args) < 2 {
			die("usage: jevx hook run EVENT")
		}
		hookRun(args[1], args[2:])
	case "status":
		cfg := core.LoadConfig()
		pr("installed  %s in %s", strings.Join(core.InstalledEvents(core.SettingsPath()), ", "), core.SettingsPath())
		cmdPlugin([]string{"list"})
		scored, acted, errs := 0, 0, 0
		if b, err := os.ReadFile(filepath.Join(core.DataDir(), "verdicts.jsonl")); err == nil {
			day := time.Now().Add(-24 * time.Hour).Format("2006-01-02T15:04:05")
			for _, l := range strings.Split(string(b), "\n") {
				var r map[string]any
				if json.Unmarshal([]byte(l), &r) != nil || fmt.Sprint(r["ts"]) < day {
					continue
				}
				if _, ok := r["answers"]; ok {
					scored++
				}
				if d, _ := r["decision"].(string); d != "" && d != "allow" {
					acted++
				}
				if _, ok := r["error"]; ok {
					errs++
				}
			}
		}
		_ = cfg
		pr("24 h       %d scored, %d would act, %d errors", scored, acted, errs)
	case "review":
		cmdReview(args[1:])
	case "enable", "disable", "mode", "profile": // v0.8 spelling: `hook enable stop`
		name := "stop-judge"
		rest := args[1:]
		if len(rest) > 0 && rest[0] == "stop" {
			rest = rest[1:]
		}
		if args[0] == "mode" && len(rest) > 0 && rest[0] == "block" {
			rest[0] = "act"
		}
		cmdPlugin(append([]string{args[0], name}, rest...))
	default:
		die("unknown hook command %q", args[0])
	}
}

// hookRun is the generic runner behind every settings.json entry: read the event payload, run the enabled plugins
// for it, print the agent's decision. Shadow plugins run detached so the agent never waits; act plugins run inline.
func hookRun(event string, args []string) {
	cfg := core.LoadConfig()
	raw, _ := io.ReadAll(os.Stdin)
	var p core.Payload
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	var act, shadow []string
	all := cfg.AllPlugins()
	for _, n := range keys(all) {
		pl := all[n]
		if !pl.Enabled || !pl.Matches(event, p) {
			continue
		}
		if pl.Mode == "act" {
			act = append(act, n)
		} else {
			shadow = append(shadow, n)
		}
	}
	scored := len(args) > 0 && args[0] == "--scored" // the detached shadow worker
	if scored {
		for _, n := range shadow {
			_, rec := cfg.Run(n, all[n], event, p)
			core.AppendLog(rec)
		}
		return
	}
	if len(shadow) > 0 {
		f, err := os.CreateTemp("", "jevx-hook-*.json")
		if err == nil {
			_, _ = f.Write(raw)
			f.Close()
			_ = core.Detach([]string{"hook", "run", event, "--scored"}, f.Name())
			go func() { time.Sleep(3 * time.Second); os.Remove(f.Name()) }()
			time.Sleep(50 * time.Millisecond)
		}
	}
	if len(act) == 0 {
		return
	}
	var ds []core.Decision
	for _, n := range act {
		d, rec := cfg.Run(n, all[n], event, p)
		core.AppendLog(rec)
		ds = append(ds, d)
	}
	stopActive, _ := p["stop_hook_active"].(bool)
	if out := core.Output(event, ds, stopActive); out != "" {
		pr("%s", out)
	}
}

func cmdReview(args []string) {
	n := 40
	if len(args) > 0 {
		fmt.Sscan(args[0], &n)
	}
	b, err := os.ReadFile(filepath.Join(core.DataDir(), "verdicts.jsonl"))
	if err != nil {
		die("no verdicts yet (%v)", err)
	}
	var rows []map[string]any
	for _, l := range strings.Split(string(b), "\n") {
		var r map[string]any
		if json.Unmarshal([]byte(l), &r) == nil && r["accept"] != nil {
			rows = append(rows, r)
		}
	}
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	would := 0
	for _, r := range rows {
		if r["would_block"] == true {
			would++
		}
	}
	pr("%d scored stops, %d would have been blocked\n", len(rows), would)
	for _, r := range rows {
		st, _ := r["state"].(string)
		req := strings.TrimPrefix(strings.SplitN(st, "\n", 2)[0], "Request: ")
		nxt := nextUserMessage(fmt.Sprint(r["transcript"]), req)
		flag := "           "
		if r["would_block"] == true {
			flag = "WOULD-BLOCK"
		}
		pr("%s acc %.2f more %.2f %s | req: %q\n%44s next: %q", fmt.Sprint(r["ts"])[5:16], r["accept"], r["wanted_more"], flag, trunc(req, 40), "", trunc(nxt, 140))
	}
}

func trunc(s string, n int) string {
	if len([]rune(s)) > n {
		return string([]rune(s)[:n])
	}
	return s
}

// nextUserMessage finds the user's first message after the scored request in that transcript.
func nextUserMessage(path, reqPrefix string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no transcript)"
	}
	seen := false
	for _, l := range strings.Split(string(b), "\n") {
		var r struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(l), &r) != nil || r.Type != "user" || r.IsMeta {
			continue
		}
		var s string
		if json.Unmarshal(r.Message.Content, &s) != nil {
			var bl []map[string]any
			_ = json.Unmarshal(r.Message.Content, &bl)
			human := true
			var parts []string
			for _, x := range bl {
				if x["type"] == "tool_result" {
					human = false
				}
				if x["type"] == "text" {
					t, _ := x["text"].(string)
					parts = append(parts, t)
				}
			}
			if !human {
				continue
			}
			s = strings.Join(parts, "\n")
		}
		if strings.HasPrefix(strings.TrimSpace(s), "<") {
			continue
		}
		if seen {
			return s
		}
		if strings.HasPrefix(strings.TrimSpace(s), strings.TrimSpace(trunc(reqPrefix, 30))) {
			seen = true
		}
	}
	return "(no reply yet)"
}

// ------------------------------------------------------------------ config

func cmdConfig(args []string) {
	cfg := core.LoadConfig()
	if len(args) == 0 || args[0] == "show" {
		b, _ := json.MarshalIndent(cfg, "", "  ")
		pr("# %s\n%s", core.ConfigPath(), b)
		return
	}
	switch args[0] {
	case "cache":
		cmdCache(args[1:])
		return
	case "set-endpoint":
		if len(args) < 3 {
			die("usage: jevx config set-endpoint PROFILE URL [MODEL]  (headers: jevx profile add … --header 'K: V')")
		}
		p := cfg.Profiles[args[1]]
		p.URL = args[2]
		if len(args) > 3 {
			p.Model = args[3]
		}
		if p.Model == "" {
			p.Model = "default"
		}
		cfg.Profiles[args[1]] = p
	case "default":
		if len(args) < 2 {
			die("usage: jevx config default PROFILE")
		}
		if _, _, err := cfg.Profile(args[1]); err != nil {
			die("%v", err)
		}
		cfg.Default = args[1]
	default:
		die("usage: jevx config show | set-endpoint PROFILE URL [MODEL] [KEY_ENV] | default PROFILE | cache enable|disable|ttl DAYS|dir PATH|clear")
	}
	if err := core.SaveConfig(cfg); err != nil {
		die("%v", err)
	}
	pr("saved %s", core.ConfigPath())
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: jevx ask|is|pick|filter|rank|question|memory|context|defaults|cache|judge|plugin|hook|profile|stats|install|uninstall|skill|version")
		os.Exit(2)
	}
	a := takeCwd(os.Args[2:])
	switch os.Args[1] {
	case "help", "-h", "--help":
		cmdHelp(a)
	case "ask", "a":
		cmdAsk(a)
	case "is", "pick", "filter", "rank":
		cmdVerb(os.Args[1], a)
	case "question", "questions", "q":
		cmdQuestion(a)
	case "cache":
		cmdCache(a)
	case "defaults", "settings":
		cmdDefaults(a)
	case "context", "ctx":
		cmdContext(a)
	case "judge":
		cmdJudge(a)
	case "install":
		cmdInstall(a)
	case "uninstall":
		cmdUninstall(a)
	case "hook":
		cmdHook(a)
	case "plugin", "plugins":
		cmdPlugin(a)
	case "config":
		cmdConfig(a)
	case "stats":
		cmdStats(a)
	case "version", "--version", "-v":
		pr("jevx %s (toolnexus %s)", version, toolnexusVersion())
	case "skill", "cookbook":
		b, _ := skillFS.ReadFile("skill/SKILL.md")
		os.Stdout.Write(b)
	case "profile", "profiles":
		cmdProfile(a)
	case "memory", "mem":
		cmdMemory(a)
	default:
		die("unknown command %q", os.Args[1])
	}
}

// toolnexusVersion is the toolnexus Go module this binary was built with: it sends every request.
func toolnexusVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/muthuishere/toolnexus/golang" {
				return d.Version
			}
		}
	}
	return "unknown"
}

// cmdHelp is `jevx help [TOPIC]`: an overview of every command, or one topic in depth.
func cmdHelp(args []string) {
	topic := ""
	if len(args) > 0 {
		topic = args[0]
	}
	switch topic {
	case "":
		pr(`usage: jevx ask|is|pick|filter|rank|question|memory|context|defaults|cache|judge|plugin|hook|profile|stats|install|uninstall|skill|version
  jevx COMMAND -h for flags; jevx skill for the full guide; https://muthuishere.github.io/jevx/

Ask (verdicts, never prose; exit 0 yes, 1 no, 3 unsure, 4 error)
  is "QUESTION" [input]                 yes / no / unsure + P
  pick "QUESTION" key=desc ... [input]  the chosen key + confidence
  filter "QUESTION" < lines [-v]        the lines whose answer is yes, like grep by meaning
  rank "QUESTION" < lines [--top N]     every line with P(yes), best first
  ask NAME,... | --noul/--choice/--score NAME="Q..."   many questions, one input or a batch
  judge --request TEXT --proposal TEXT  would the user accept this turn?

Inputs: stdin, --in TEXT|@file|'{json}', and images with --image FILE (PNG/JPEG, repeat)
Batch (parallel, in input order; a table, --raw for JSONL):
  --lines FILE      one text input per line          --states FILE.jsonl  one JSON input per line
  --images DIR|GLOB one input per image file          --parallel N         concurrent requests

Configure
  profile list|types|add|use|remove|show   endpoints and their type (typesafe, openrouter, cloudflare, vercel, custom)
  question add|list|show|remove            saved questions        defaults   every setting and its source
  context                                  the ## Jev context     cache      the local answer cache
  memory add|index|check|show|list         judge against your own notes
Agents: install, uninstall, skill, plugin, hook, stats

More: jevx help profiles | images | batch`)
	case "profiles", "profile", "types":
		pr(`A profile is one endpoint: its type, url, model and headers (secrets as $VARS, expanded per request). jevx keeps the
profiles; each call hands toolnexus the backend, url, model and headers, and toolnexus builds the request for that type.
With no config, jevx uses the built-in "jev" profile: TypeSafe's hosted Jev with $TYPESAFE_API_KEY.
`)
		cmdProfile([]string{"types"})
		pr(`
Examples:
  jevx profile add openjevx http://127.0.0.1:21160/v1/systemone --model openjevx        # type typesafe, own url
  jevx profile add or --type openrouter                                                  # $OPENROUTER_API_KEY
  jevx profile add cf --type cloudflare                                                  # $CLOUDFLARE_ACCOUNT_ID, $CLOUDFLARE_JEV_API_KEY
  jevx profile add clef --type cloudflare --model @cf/cloudflare/clef                    # Clef takes images
  jevx profile use or          or per call: --profile or`)
	case "images", "image":
		pr(`Images (PNG or JPEG, 280-1568 px per side, up to 16 MiB each and 32 MiB a request) go with an input to profiles that
take them. toolnexus sends them and checks the limits before any request:
  typesafe, openrouter  System One "assets" (image1, image2, ...), with the TypeSafe-Danger-Zone-Images header
  cloudflare            only Clef models (@cf/cloudflare/clef*), as data URLs in input.images
  vercel, custom        refused, unless the profile sets "images": true (jevx profile add ... --images)
The image bytes are part of the cache key; they are never redacted, so do not send screenshots of secrets to a hosted endpoint.

  jevx is "Is this an error page?" --image shot.png
  jevx ask --image before.png --image after.png --noul same="Do the two screenshots show the same layout?"
  jevx filter "Does this photo show a damaged package?" --images ./returns/       # prints the matching files
  jevx ask --images 'shots/*.png' --in "Checkout page" --choice state="Which state?|ok=works;error=error shown;blank=blank page"`)
	case "batch":
		pr(`A batch asks every question about every input, --parallel N at a time (default 8), and prints one row per input in
input order: VERDICT  P  INPUT (a VERDICT/P pair per question when several are asked). --raw prints JSONL.
A failed input prints its error on stderr and the rest go on; the batch exits 4 if any failed, else 0.
  --lines FILE|-        one text input per line
  --states FILE.jsonl   one JSON object (or JSON string) per line
  --images DIR|GLOB|FILE  one input per image; --in gives them all the same text
  --image FILE          attach an image to every input of a --lines / --states batch
filter and rank are batches too. Each input is one request through toolnexus carrying up to "chunk" questions (32):
no backend has a native multi-input request, so the batch is the requests in parallel.`)
	default:
		die("no help topic %q: jevx help [profiles | images | batch]", topic)
	}
}

func cmdProfile(args []string) {
	cfg := core.LoadConfig()
	if len(args) == 0 || args[0] == "list" {
		all := map[string]core.Profile{core.BuiltinName: core.BuiltinProfile}
		for k, p := range cfg.Profiles {
			all[k] = p
		}
		def, _, _ := cfg.Profile("")
		for _, n := range keys(all) {
			p := all[n]
			if p.URL == "" {
				continue // the empty placeholder of older configs
			}
			mark := " "
			if n == def {
				mark = "*"
			}
			if _, own := cfg.Profiles[n]; !own {
				p.Note = "built in"
			}
			hs := ""
			for _, h := range keys(p.Headers) {
				hs += "  " + h + ": " + p.Headers[h]
			}
			note := ""
			if p.Note == "built in" {
				note = "  (built in)"
			}
			img := ""
			if p.TakesImages() {
				img = "  images"
			}
			pr("%s %-10s %-10s %s  model=%s%s%s%s", mark, n, p.Kind(), p.URL, p.Model, img, hs, note)
		}
		pr("(* = default; change with: jevx profile use NAME; the types: jevx profile types)")
		return
	}
	need := func(n int, u string) {
		if len(args) < n {
			die("usage: jevx profile %s", u)
		}
	}
	switch args[0] {
	case "types":
		for _, t := range core.Types {
			img := map[bool]string{true: "yes", false: "no"}[core.Preset(t.Name, core.Profile{Type: t.Name}).TakesImages()]
			url, model, key := t.URL, t.Model, "$"+t.KeyEnv
			if url == "" {
				url, key = "(required)", "(--header)"
			}
			pr("%-11s %s\n%-11s url %s\n%-11s model %s   key %s   images %s\n", t.Name, t.Summary, "", url, "", model, key, img)
		}
		pr("add one: jevx profile add NAME [URL] --type TYPE [--model M] [--header 'Authorization: Bearer $VAR']")
		pr("  the url, model and key above are used when not given; default type: typesafe. toolnexus %s sends every request.", toolnexusVersion())
		pr("images: --image FILE / --images DIR go only to a profile that takes them; override per profile with --images / --no-images")
		return
	case "add":
		need(2, "add NAME [URL] [--type typesafe|openrouter|cloudflare|vercel|custom] [--model M] [--header 'K: V' ...] [--questions FILE]")
		url, rest := "", args[2:]
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			url, rest = rest[0], rest[1:]
		}
		fs := flag.NewFlagSet("profile add", flag.ExitOnError)
		m, q := fs.String("model", "", "model name (default: the type's)"), fs.String("questions", "", "question pack JSON")
		st := fs.String("type", "", "the API the endpoint speaks: "+core.TypeNames()+" (default typesafe; jevx profile types)")
		fs.StringVar(st, "style", "", "legacy name for --type")
		imgOn, imgOff := fs.Bool("images", false, "this endpoint takes images even if its type does not by default"), fs.Bool("no-images", false, "never send images to this endpoint")
		var hdr multi
		fs.Var(&hdr, "header", `request header "Name: value" (repeat); reference env vars for secrets: "Authorization: Bearer $JEV_API_KEY"`)
		_ = fs.Parse(rest)
		hm := map[string]string{}
		for _, h := range hdr {
			k, v, ok := strings.Cut(h, ":")
			if !ok {
				die("header %q: want \"Name: value\"", h)
			}
			hm[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		if *st == "" {
			*st = core.Profile{URL: url}.Kind() // a Cloudflare, OpenRouter or Vercel url is recognised; otherwise typesafe
		}
		if _, ok := core.LookupType(*st); !ok {
			die("--type: want one of %s, got %q (jevx profile types)", core.TypeNames(), *st)
		}
		if url == "" && *st == core.TypeCustom {
			die("a custom profile needs its URL: jevx profile add %s URL --type custom", args[1])
		}
		np := core.Preset(*st, core.Profile{URL: url, Model: *m, Headers: hm, Questions: *q, Type: *st})
		switch {
		case *imgOn && *imgOff:
			die("give --images or --no-images, not both")
		case *imgOn:
			np.Images = &[]bool{true}[0]
		case *imgOff:
			np.Images = &[]bool{false}[0]
		}
		cfg.Profiles[args[1]] = np
		if ph, ok := cfg.Profiles["default"]; ok && ph.URL == "" && args[1] != "default" {
			delete(cfg.Profiles, "default") // the seeded placeholder is replaced by the first real profile
		}
		if _, _, err := cfg.Profile(""); err != nil { // no working default yet: the new profile becomes it
			cfg.Default = args[1]
		}
		if cfg.Default != args[1] {
			pr("added %s; the default stays %s (switch: jevx profile use %s, or --profile %s per call)", args[1], cfg.Default, args[1], args[1])
		}
	case "use":
		need(2, "use NAME")
		if _, ok := cfg.Profiles[args[1]]; !ok && args[1] != core.BuiltinName {
			die("no profile %q", args[1])
		}
		cfg.Default = args[1]
	case "remove":
		need(2, "remove NAME")
		delete(cfg.Profiles, args[1])
		if cfg.Default == args[1] {
			cfg.Default = ""
		}
	case "show":
		need(2, "show NAME")
		b, _ := json.MarshalIndent(cfg.Profiles[args[1]], "", "  ")
		pr("%s", b)
		return
	default:
		die("usage: jevx profile list | types | add | use | remove | show")
	}
	if err := core.SaveConfig(cfg); err != nil {
		die("%v", err)
	}
	pr("saved %s (default: %s)", core.ConfigPath(), cfg.Default)
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cmdQuery is the native System One call: one state (plain text or a JSON object, sent as its JSON text), many named
// questions. It prints the answers the way the API returns them; --raw prints the whole response (model, usage, id).

// cmdStats summarises the call ledger: calls, errors, tokens and latency per day and host.
func cmdStats(args []string) {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	days := fs.Int("days", 7, "how many days back")
	_ = fs.Parse(args)
	b, err := os.ReadFile(filepath.Join(core.Home(), ".local/share/jevx/calls.jsonl"))
	if err != nil {
		die("no ledger yet (%v)", err)
	}
	type agg struct {
		calls, errs, in, out int
		ms                   []int
	}
	from := time.Now().UTC().AddDate(0, 0, -*days).Format("2006-01-02")
	m := map[string]*agg{}
	for _, l := range strings.Split(string(b), "\n") {
		var r struct {
			TS, Host, Error string
			MS              int
			Usage           map[string]float64
		}
		if json.Unmarshal([]byte(l), &r) != nil || len(r.TS) < 10 || r.TS[:10] < from {
			continue
		}
		k := r.TS[:10] + "  " + r.Host
		if m[k] == nil {
			m[k] = &agg{}
		}
		a := m[k]
		a.calls++
		a.ms = append(a.ms, r.MS)
		a.in += int(r.Usage["input_tokens"])
		a.out += int(r.Usage["output_tokens"])
		if r.Error != "" {
			a.errs++
		}
	}
	pr("%-40s %6s %6s %10s %8s %8s", "day  host", "calls", "errors", "tokens in", "out", "p50 ms")
	for _, k := range keys(m) {
		a := m[k]
		sort.Ints(a.ms)
		pr("%-40s %6d %6d %10d %8d %8d", k, a.calls, a.errs, a.in, a.out, a.ms[len(a.ms)/2])
	}
}

// takeCwd applies --cwd DIR (anywhere in the arguments) by changing into it, so the folder "## Jev" section and
// relative paths resolve there; by default everything runs in the current directory.
func takeCwd(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		v, ok := "", false
		switch {
		case (args[i] == "--cwd" || args[i] == "-cwd") && i+1 < len(args):
			v, ok = args[i+1], true
			i++
		case strings.HasPrefix(args[i], "--cwd="):
			v, ok = strings.TrimPrefix(args[i], "--cwd="), true
		}
		if ok {
			if err := os.Chdir(v); err != nil {
				die("--cwd: %v", err)
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}
