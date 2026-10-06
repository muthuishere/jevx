package main

// jevx plugin: list | show NAME | add NAME --on EVENT[:Tool] --ask q1,q2 [--deny|--warn|--block|--context COND]
//                [--say TEXT] [--exec CMD] [--desc TEXT] [--profile P] [--local] | remove | enable NAME|all [--act] | disable NAME|all |
//                mode NAME shadow|act | test NAME [--payload FILE] | log [NAME] [N]

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/muthuishere/jevx/core"
)

func cmdPlugin(args []string) {
	args, local := takeLocal(args)
	cfg := core.LoadConfig()
	all := cfg.AllPlugins()
	if len(args) == 0 || args[0] == "list" {
		if len(all) == 0 {
			pr("no plugins: jevx plugin add NAME --on PreToolUse:Bash --ask destroys --deny \"destroys >= 0.8\"")
			return
		}
		lq := core.LocalPlugins()
		pr("%-18s %-24s %-8s %-7s %s", "plugin", "on", "enabled", "mode", "description")
		for _, k := range keys(all) {
			p := all[k]
			where := ""
			if _, ok := lq[k]; ok {
				where = " (folder)"
			}
			mode := p.Mode
			if mode == "" {
				mode = "shadow"
			}
			pr("%-18s %-24s %-8v %-7s %s%s", k, p.On, p.Enabled, mode, p.Desc, where)
		}
		return
	}
	name := ""
	if len(args) > 1 {
		name = args[1]
	}
	need := func() core.Plugin {
		p, ok := all[name]
		if name == "" || !ok {
			die("no plugin named %q (have: %s)", name, strings.Join(keys(all), ", "))
		}
		return p
	}
	// where a change is written: the folder file with --local (or when the plugin lives there), else the global config
	target := cfg.Plugins
	_, inLocal := core.LocalPlugins()[name]
	if local || inLocal {
		target = core.LocalPlugins()
		local = true
	}
	save := func() {
		if local {
			path := filepath.Join(localDir(), "plugins.json")
			b, _ := json.MarshalIndent(target, "", "  ")
			if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
				die("%v", err)
			}
			pr("saved %s", path)
			return
		}
		cfg.Plugins = target
		if err := core.SaveConfig(cfg); err != nil {
			die("%v", err)
		}
		pr("saved %s", core.ConfigPath())
	}
	defer syncHooks(args[0])
	switch args[0] {
	case "show":
		pr("%s", jsonPretty(need()))
	case "remove", "rm":
		need()
		delete(target, name)
		save()
	case "enable", "disable":
		if name == "all" { // one switch for every plugin, global and folder
			on := args[0] == "enable"
			for _, k := range keys(all) {
				p := all[k]
				p.Enabled = on
				if on && contains(args, "--act") {
					p.Mode = "act"
				}
				if _, isLocal := core.LocalPlugins()[k]; isLocal {
					lp := core.LocalPlugins()
					lp[k] = p
					b, _ := json.MarshalIndent(lp, "", "  ")
					_ = os.WriteFile(filepath.Join(localDir(), "plugins.json"), append(b, '\n'), 0o644)
				} else {
					cfg.Plugins[k] = p
				}
			}
			if err := core.SaveConfig(cfg); err != nil {
				die("%v", err)
			}
			pr("%d plugins %sd", len(all), args[0])
			return
		}
		p := need()
		p.Enabled = args[0] == "enable"
		if contains(args, "--act") {
			p.Mode = "act"
		}
		target[name] = p
		save()
		if p.Enabled {
			mode := p.Mode
			if mode == "" {
				mode = "shadow"
			}
			pr("%s enabled, mode %s%s", name, mode, map[bool]string{true: " (logs only; `jevx plugin mode " + name + " act` to let it act)", false: ""}[mode == "shadow"])
		}
	case "mode":
		p := need()
		if len(args) < 3 || (args[2] != "shadow" && args[2] != "act") {
			die("usage: jevx plugin mode NAME shadow|act")
		}
		p.Mode = args[2]
		target[name] = p
		save()
	case "add":
		if name == "" || strings.HasPrefix(name, "-") {
			die(`usage: jevx plugin add NAME --on EVENT[:Tool] --ask q1,q2 --deny|--warn|--block|--context "COND" [--say TEXT] [--exec CMD] [--desc TEXT] [--profile P] [--local]`)
		}
		fs := flag.NewFlagSet("plugin add", flag.ExitOnError)
		p := target[name]
		fs.StringVar(&p.Desc, "desc", p.Desc, "what it does, one line")
		fs.StringVar(&p.On, "on", p.On, "EVENT[:Tool regex]: Stop, PreToolUse:Bash, PostToolUse:WebFetch, UserPromptSubmit, PreCompact, SessionStart")
		fs.StringVar(&p.Ask, "ask", p.Ask, "question names, comma-separated")
		fs.StringVar(&p.Deny, "deny", p.Deny, "condition: refuse the tool call")
		fs.StringVar(&p.Warn, "warn", p.Warn, "condition: ask the user (PreToolUse) or warn the agent")
		fs.StringVar(&p.Block, "block", p.Block, "condition: send the agent back with a reason")
		fs.StringVar(&p.Context, "context", p.Context, "condition: add --say (or the answers) as context")
		fs.StringVar(&p.Say, "say", p.Say, "the text to inject; {{name}} = that answer")
		fs.StringVar(&p.Exec, "exec", p.Exec, "a command that decides instead (gets JSON on stdin)")
		fs.StringVar(&p.Profile, "profile", p.Profile, "endpoint profile")
		fs.StringVar(&p.Gate, "gate", p.Gate, "Stop: all = judge every turn")
		enable := fs.Bool("enable", false, "enable it now (shadow mode)")
		_ = fs.Parse(args[2:])
		if p.On == "" {
			die("--on is required")
		}
		if p.Ask == "" && p.Exec == "" {
			die("give --ask (question names) or --exec (a command)")
		}
		if p.Ask != "" && p.Deny+p.Warn+p.Block+p.Context == "" && p.Exec == "" {
			die("give what to do: --deny, --warn, --block or --context with a condition such as \"destroys >= 0.8\"")
		}
		_, prof, _ := cfg.Profile(p.Profile)
		for _, n := range strings.Split(p.Ask, ",") {
			if n = strings.TrimSpace(n); n != "" {
				if _, ok := cfg.ResolveQuestion(prof, n); !ok {
					die("no question named %q: add it first (jevx question add %s --noul \"...\") or use a built-in (%s)", n, n, strings.Join(builtinQuestions(prof), ", "))
				}
			}
		}
		if *enable {
			p.Enabled = true
		}
		target[name] = p
		save()
	case "test":
		p := need()
		fs := flag.NewFlagSet("plugin test", flag.ExitOnError)
		payload := fs.String("payload", "", "a hook payload JSON file (default: a sample for the event, or stdin with -)")
		_ = fs.Parse(args[2:])
		var pay core.Payload
		switch {
		case *payload == "-":
			_ = json.Unmarshal([]byte(text("-")), &pay)
		case *payload != "":
			_ = json.Unmarshal([]byte(text("@"+*payload)), &pay)
		default:
			pay = samplePayload(p.Event(), p.Tool(), fs.Args())
		}
		if pay == nil {
			die("payload is not JSON")
		}
		if !p.Matches(p.Event(), pay) {
			pr("would not run: tool %q does not match %q", pay.Str("tool_name"), p.Tool())
			return
		}
		d, rec := cfg.Run(name, p, p.Event(), pay)
		pr("%s", jsonPretty(map[string]any{"decision": d, "record": rec, "output": core.Output(p.Event(), []core.Decision{d}, false)}))
	case "log":
		n := 20
		filter := name
		if filter != "" && !strings.HasPrefix(filter, "-") {
			if _, isNum := fmt.Sscan(filter, &n); isNum == nil && !contains(keys(all), filter) {
				filter = ""
			}
		}
		if len(args) > 2 {
			fmt.Sscan(args[2], &n)
		}
		b, err := os.ReadFile(filepath.Join(core.DataDir(), "verdicts.jsonl"))
		if err != nil || strings.TrimSpace(string(b)) == "" {
			pr("no decisions logged yet (a plugin logs once it is enabled and its event fires)")
			return
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		shown := 0
		for i := len(lines) - 1; i >= 0 && shown < n; i-- {
			var r map[string]any
			if json.Unmarshal([]byte(lines[i]), &r) != nil || (filter != "" && r["plugin"] != filter) {
				continue
			}
			shown++
			ans, _ := json.Marshal(r["answers"])
			pr("%s  %-14s %-16s %-7s %s  %s", r["ts"], r["plugin"], r["event"], orStr(r["decision"], r["skipped"], r["error"]), ans, trunc(fmt.Sprint(orStr(r["reason"], "")), 80))
		}
	default:
		die("usage: jevx plugin list | show | add | remove | enable | disable | mode | test | log")
	}
}

func orStr(vs ...any) string {
	for _, v := range vs {
		if s, _ := v.(string); s != "" {
			return s
		}
	}
	return "-"
}

func builtinQuestions(p core.Profile) []string {
	var out []string
	for _, n := range []string{"accepts", "wanted_more", "reaction", "satisfaction", "destroys", "remote", "irreversible", "injection", "complexity", "kind"} {
		if _, ok := (core.Config{}).ResolveQuestion(p, n); ok {
			out = append(out, n)
		}
	}
	return out
}

// samplePayload is a stand-in event for `plugin test` when no payload is given; extra words become the command / prompt.
func samplePayload(event, tools string, words []string) core.Payload {
	wd, _ := os.Getwd()
	txt := strings.Join(words, " ")
	p := core.Payload{"session_id": "test", "cwd": wd, "hook_event_name": event}
	switch event {
	case "PreToolUse", "PostToolUse":
		// the sample is for the plugin's own tool (the first in its matcher): a Write|Edit guard gets file content, not a
		// shell command it would never see
		tool, _, _ := strings.Cut(strings.Trim(tools, "^$()"), "|")
		if tool == "" || strings.ContainsAny(tool, ".*+?[\\") {
			tool = "Bash"
		}
		var in, out map[string]any
		switch tool {
		case "Bash":
			if txt == "" {
				txt = "rm -rf ./build && git push --force origin main"
			}
			in, out = map[string]any{"command": txt}, map[string]any{"stdout": "ok"}
		case "Write":
			in = map[string]any{"file_path": filepath.Join(wd, "example.txt"), "content": txt}
		case "Edit", "MultiEdit":
			in = map[string]any{"file_path": filepath.Join(wd, "example.txt"), "old_string": "", "new_string": txt}
		case "WebFetch", "WebSearch":
			in, out = map[string]any{"url": "https://example.com/docs", "prompt": "summarise"}, map[string]any{"result": txt}
		default:
			in = map[string]any{"input": txt}
		}
		p["tool_name"], p["tool_input"] = tool, in
		if event == "PostToolUse" {
			if out == nil {
				out = map[string]any{"result": "ok"}
			}
			p["tool_response"] = out
		}
	case "UserPromptSubmit":
		if txt == "" {
			txt = "rename the variable foo to bar in utils.py"
		}
		p["prompt"] = txt
	case "Stop", "SubagentStop":
		p["transcript_path"] = txt
		p["stop_hook_active"] = false
	}
	return p
}

// jsonPretty is MarshalIndent without HTML escaping, so conditions such as "destroys >= 0.8 && remote < 0.5" read as written.
func jsonPretty(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

// syncHooks keeps settings.json in step with the enabled plugins after a plugin change: entries exist only for events
// with an enabled plugin, so enabling is the opt-in and disabling the last one removes them.
func syncHooks(action string) {
	switch action {
	case "enable", "disable", "add", "remove", "rm":
	default:
		return
	}
	cfg := core.LoadConfig()
	if cfg.RememberPluginDir() {
		if err := core.SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "jevx: %v\n", err)
		}
	}
	added, removed, err := cfg.InstallHooks(core.SettingsPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "jevx: %v\n", err)
		return
	}
	if added+removed > 0 {
		pr("hooks    %d added, %d removed in %s", added, removed, core.SettingsPath())
	}
}
