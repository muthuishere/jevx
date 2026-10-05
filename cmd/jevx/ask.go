package main

// jevx ask: the one call. Questions come by NAME from config (`jevx question add`) and/or inline
// (--noul / --choice / --score); inputs from stdin, --in, --lines or --states. Every threshold and knob comes from
// config with defaults (core.Builtin < config "defaults" < profile "defaults" < flags).
//
// Output: one input -> one line per question "NAME  VERDICT  P" (or --json); a batch -> JSONL, one line per input.
// Exit (one input): 0 all yes/decided, 1 a "no", 3 an "unsure", 4 an error. A batch exits 0, or 4 if any input failed.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/muthuishere/jevx/core"
)

func text(v string) string {
	switch {
	case v == "-":
		b, _ := io.ReadAll(os.Stdin)
		return string(b)
	case strings.HasPrefix(v, "@"):
		b, err := os.ReadFile(v[1:])
		if err != nil {
			die("%v", err)
		}
		return string(b)
	}
	return v
}

func resolve(prof string) (string, core.Profile) {
	name, p, err := core.LoadConfig().Profile(prof)
	if err != nil {
		die("%v", err)
	}
	return name, p
}

// normState trims a state; a structured state travels as its compact JSON text, as the API expects.
func normState(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return s, nil
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return "", errors.New("looks like JSON but does not parse")
	}
	b, _ := json.Marshal(v)
	return string(b), nil
}

// askChunks asks every question about one state, in chunks the server accepts; the model name comes from the reply.
func askChunks(p core.Profile, state string, qs map[string]core.Question, set core.Settings) (map[string]core.Answer, any, error) {
	out, model := map[string]core.Answer{}, any(nil)
	ttl := time.Duration(*set.CacheTTLDays) * 24 * time.Hour
	dir, hashes := "", map[string]string{}
	if *set.Cache {
		dir = cacheDir
	}
	var miss []string
	hit := map[string]any{}
	for _, k := range keys(qs) {
		hashes[k] = core.CacheKey(p, state, qs[k].Wire())
		if a, m, ok := core.CacheGet(dir, hashes[k], ttl); ok {
			out[k], model, hit[k] = a, m, qs[k].Wire()
			continue
		}
		miss = append(miss, k)
	}
	if len(hit) > 0 { // a cached hosted answer was given on redacted text: say so, as a fresh call would
		core.NoteRedacted(core.Redactions(p, state, hit))
	}
	for i := 0; i < len(miss); i += *set.Chunk {
		part := map[string]any{}
		for _, k := range miss[i:min(i+*set.Chunk, len(miss))] {
			part[k] = qs[k].Wire()
		}
		ans, raw, err := core.AskRaw(p, state, part, time.Duration(*set.TimeoutS)*time.Second)
		if err != nil {
			return nil, nil, err
		}
		var v map[string]any
		if json.Unmarshal(raw, &v) == nil {
			model = v["model"]
		}
		for k, a := range ans {
			out[k] = a
			core.CachePut(dir, hashes[k], a, model)
		}
	}
	return out, model, nil
}

// verdict turns an answer into yes | no | unsure (noul), the key or unsure (choice), the level or unsure (score).
func verdict(q core.Question, a core.Answer, set core.Settings) string {
	yes, no, minC := *set.Yes, *set.No, *set.MinConfidence
	if q.Yes != nil {
		yes = *q.Yes
	}
	if q.No != nil {
		no = *q.No
	}
	if q.MinConfidence != nil {
		minC = *q.MinConfidence
	}
	conf := 1.0
	if a.Confidence != nil {
		conf = *a.Confidence
	}
	switch q.Type {
	case "noul":
		switch p := a.P(); {
		case p >= yes:
			return "yes"
		case p <= no:
			return "no"
		}
		return "unsure"
	case "choice":
		if conf < minC {
			return "unsure"
		}
		return a.Choice
	case "score":
		if conf < minC || a.Score == nil {
			return "unsure"
		}
		if lv, ok := q.Criteria.([]string); ok {
			if i := int(math.Round(*a.Score)); i >= 0 && i < len(lv) {
				return lv[i]
			}
		}
		return strconv.FormatFloat(*a.Score, 'f', 2, 64)
	}
	return ""
}

// value is the number shown next to a verdict: P(yes) for a noul, the confidence otherwise.
func value(a core.Answer) float64 {
	if a.Type == "noul" {
		return a.P()
	}
	if a.Confidence != nil {
		return *a.Confidence
	}
	return 0
}

func inlineQ(kind, spec string) (string, core.Question) {
	name, rest, ok := strings.Cut(spec, "=")
	if !ok || strings.TrimSpace(name) == "" {
		die("--%s: want NAME=\"QUESTION\", got %q", kind, spec)
	}
	return strings.TrimSpace(name), parseQ(kind, rest)
}

// parseQ reads the compact question form: noul "Q" or "Q|true text|false text"; choice "Q|k=desc;k2=desc";
// score "Q|low;mid;high".
func parseQ(kind, spec string) core.Question {
	parts := strings.Split(spec, "|")
	q := core.Question{Type: kind, Instructions: strings.TrimSpace(parts[0])}
	switch kind {
	case "noul":
		if len(parts) == 3 {
			q.Criteria = map[string]string{"true": strings.TrimSpace(parts[1]), "false": strings.TrimSpace(parts[2])}
		}
	case "choice":
		if len(parts) != 2 {
			die("choice: want \"QUESTION|key=desc;key2=desc\", got %q", spec)
		}
		crit := map[string]string{}
		for _, o := range strings.Split(parts[1], ";") {
			k, v, _ := strings.Cut(o, "=")
			if v == "" {
				v = k
			}
			crit[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		q.Criteria = crit
	case "score":
		if len(parts) != 2 {
			die("score: want \"QUESTION|level0;level1;...\", got %q", spec)
		}
		var lv []string
		for _, l := range strings.Split(parts[1], ";") {
			lv = append(lv, strings.TrimSpace(l))
		}
		q.Criteria = lv
	}
	return q
}

// normQ gives a question loaded from JSON the concrete criteria types the rest of the code expects.
func normQ(q core.Question) core.Question {
	switch c := q.Criteria.(type) {
	case []any:
		var lv []string
		for _, v := range c {
			lv = append(lv, fmt.Sprint(v))
		}
		q.Criteria = lv
	case map[string]any:
		m := map[string]string{}
		for k, v := range c {
			m[k] = fmt.Sprint(v)
		}
		q.Criteria = m
	}
	return q
}

// splitArgs separates positional NAMES from flags so both `ask urgent --in x` and `ask --in x urgent` work.
func splitArgs(args []string, valued map[string]bool) (names, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			for _, n := range strings.Split(a, ",") {
				if n = strings.TrimSpace(n); n != "" {
					names = append(names, n)
				}
			}
			continue
		}
		flags = append(flags, a)
		if f := strings.TrimLeft(a, "-"); valued[f] && !strings.Contains(f, "=") && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return
}

// outMode is set by the shortcut verbs: "bare" (one question, print VERDICT P), "filter" / "filter-v" (print the
// matching inputs), "rank" (print P and input, best first; topN limits it).
var (
	outMode  string
	topN     int
	outRaw   bool
	cacheDir string // "" until cmdAsk resolves it from config.json
)

func cmdAsk(args []string) {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	in := fs.String("in", "", "one input: text, a JSON object, @file or - (default: stdin)")
	lines := fs.String("lines", "", "batch: a text file (or -), one input per line")
	states := fs.String("states", "", "batch: a JSONL file (or -), one JSON object or string per line")
	qfile := fs.String("questions", "", "a question-set file: {NAME: {type, instructions, criteria}}")
	prof := fs.String("profile", "", "endpoint profile")
	asJSON := fs.Bool("json", false, "one input: print JSON instead of lines")
	fs.BoolVar(&core.CacheFresh, "fresh", false, "ask the model again and refresh the stored answers (the cache is otherwise used)")
	raw := fs.Bool("raw", false, "the full result as it is: the server's response for one input, JSONL (line, input, every answer) for a batch")
	yes := fs.Float64("yes", -1, "override: noul P at or above is yes; -1 = the value from jevx defaults")
	no := fs.Float64("no", -1, "override: noul P at or below is no; -1 = the value from jevx defaults")
	minC := fs.Float64("min", -1, "override: choice / score confidence below is unsure; -1 = the value from jevx defaults")
	par := fs.Int("parallel", 0, "override: concurrent requests in a batch")
	memName := fs.String("memory", "", "a jevx memory: append its best-matching notes after the item (jevx memory list)")
	memK := fs.Int("memory-k", 3, "with --memory: BM25 sections after named and pinned pages")
	memBudget := fs.Int("memory-budget", 2000, "with --memory: characters of notes")
	memStrict := fs.Bool("memory-strict", false, "with --memory: a number, code span or name in the input that the retrieved notes lack makes a yes/no answer no")
	var nouls, choices, scores, ctxs multi
	fs.Var(&ctxs, "context", "background for this call: TEXT or @file (repeat); added after the ## Jev sections")
	fs.BoolVar(&core.NoContext, "no-context", false, "skip the ## Jev sections of the global and folder agent files")
	fs.Var(&nouls, "noul", `NAME="QUESTION" (repeat); or NAME="QUESTION|true means|false means"`)
	fs.Var(&choices, "choice", `NAME="QUESTION|key=desc;key2=desc" (repeat)`)
	fs.Var(&scores, "score", `NAME="QUESTION|low;mid;high" (repeat, lowest first)`)
	valued := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
			valued[f.Name] = true
		}
	})
	names, flags := splitArgs(args, valued)
	_ = fs.Parse(flags)
	outRaw = *raw

	cfg := core.LoadConfig()
	name, p, err := cfg.Profile(*prof)
	if err != nil {
		die("%v", err)
	}
	set := cfg.Settings(p)
	cacheDir = cfg.CacheDirPath()
	if *yes >= 0 {
		set.Yes = yes
	}
	if *no >= 0 {
		set.No = no
	}
	if *minC >= 0 {
		set.MinConfidence = minC
	}
	if *par > 0 {
		set.Parallel = par
	}
	var mem *memAsk
	if *memName != "" {
		m, err := core.LoadMemory(*memName)
		if err != nil {
			die("%v", err)
		}
		warnDrift(m)
		mem = &memAsk{m: m, k: *memK, budget: *memBudget, strict: *memStrict}
	}
	core.Retries, core.LedgerOn = *set.Retries, *set.Ledger

	qs := map[string]core.Question{}
	for _, n := range names {
		all := allQuestions(cfg)
		q, ok := all[n]
		if !ok {
			die("no question named %q (have: %s). Add one: jevx question add %s --noul \"...\"", n, strings.Join(keys(all), ", "), n)
		}
		qs[n] = normQ(q)
	}
	if *qfile != "" {
		var m map[string]core.Question
		if err := json.Unmarshal([]byte(text("@"+strings.TrimPrefix(*qfile, "@"))), &m); err != nil {
			die("--questions %s: %v", *qfile, err)
		}
		for k, q := range m {
			qs[k] = normQ(q)
		}
	}
	for kind, specs := range map[string]multi{"noul": nouls, "choice": choices, "score": scores} {
		for _, sp := range specs {
			n, q := inlineQ(kind, sp)
			qs[n] = q
		}
	}
	if len(qs) == 0 {
		die("usage: jevx ask NAME[,NAME...] | --noul NAME=\"QUESTION\" ...  [--in X | --lines FILE | --states FILE.jsonl | < stdin]\n  named questions: jevx question list")
	}

	// A batch: --lines or --states.
	if *lines != "" || *states != "" {
		var inputs []string
		var nums []int
		src := *lines + *states
		if src != "-" {
			src = "@" + strings.TrimPrefix(src, "@")
		}
		for i, l := range strings.Split(text(src), "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if *states != "" {
				var s string
				if strings.HasPrefix(strings.TrimSpace(l), `"`) && json.Unmarshal([]byte(l), &s) == nil {
					l = s
				}
			}
			inputs, nums = append(inputs, l), append(nums, i+1)
		}
		os.Exit(batch(cfg, p, qs, set, inputs, nums, callCtx(ctxs), mem))
	}

	// One input.
	src := *in
	if src == "" {
		src = "-"
	}
	state, err := normState(text(src))
	if err != nil {
		die("input: %v", err)
	}
	state, hits, diff := mem.apply(state)
	ans, model, err := askChunks(p, cfg.WithContext(p, state, callCtx(ctxs)...), qs, set)
	if errors.Is(err, core.ErrNeedKey) {
		die("%s", core.NeedKeyHelp(name, p))
	}
	if err != nil {
		die("%v", err)
	}
	if *raw {
		out := map[string]any{"model": model, "answers": ans}
		if mem != nil {
			out["memory"], out["memory_diff"] = hits, diff
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		pr("%s", b)
		return
	}
	code, rows := 0, map[string]any{}
	for _, k := range keys(qs) {
		v := mem.strictVerdict(qs[k], verdict(qs[k], ans[k], set), diff)
		switch {
		case v == "unsure":
			code = 3
		case v == "no" && code == 0:
			code = 1
		}
		if *asJSON {
			rows[k] = map[string]any{"verdict": v, "answer": ans[k]}
		} else if outMode == "bare" {
			pr("%s %.2f", v, value(ans[k]))
		} else {
			pr("%-16s %-10s %.2f", k, v, value(ans[k]))
		}
	}
	if *asJSON {
		out := map[string]any{"profile": name, "model": model, "answers": rows}
		if mem != nil {
			out["memory"], out["memory_diff"] = hits, diff
		}
		b, _ := json.Marshal(out)
		pr("%s", b)
	}
	os.Exit(code)
}

// batch asks every question of every input, set.Parallel at a time, and prints a readable table (--raw: one JSONL line per input) in input
// order. A failed input prints its error and the rest go on; it returns 4 if any failed.
func batch(cfg core.Config, p core.Profile, qs map[string]core.Question, set core.Settings, inputs []string, nums []int, extra []string, mem *memAsk) int {
	type row struct {
		Line    int            `json:"line"`
		Input   string         `json:"input"`
		Answers map[string]any `json:"answers,omitempty"`
		Memory  []core.MemHit  `json:"memory,omitempty"`
		Diff    []string       `json:"memory_diff,omitempty"`
		Error   string         `json:"error,omitempty"`
	}
	rows := make([]row, len(inputs))
	sem := make(chan struct{}, max(1, *set.Parallel))
	var wg sync.WaitGroup
	for i, l := range inputs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			rows[i] = row{Line: nums[i], Input: trunc(l, 120)}
			state, err := normState(l)
			var ans map[string]core.Answer
			if err == nil {
				state, rows[i].Memory, rows[i].Diff = mem.apply(state)
				ans, _, err = askChunks(p, cfg.WithContext(p, state, extra...), qs, set)
			}
			if errors.Is(err, core.ErrNeedKey) {
				err = errors.New(core.NeedKeyHelp("", p))
			}
			if err != nil {
				rows[i].Error = err.Error()
				return
			}
			rows[i].Answers = map[string]any{}
			for k, a := range ans {
				rows[i].Answers[k] = map[string]any{"verdict": mem.strictVerdict(qs[k], verdict(qs[k], a, set), rows[i].Diff), "p": round2(value(a)), "answer": a}
			}
		}()
	}
	wg.Wait()
	code := 0
	for _, r := range rows {
		if r.Error != "" {
			code = 4
			fmt.Fprintf(os.Stderr, "jevx: line %d: %s\n", r.Line, r.Error)
		}
	}
	if outMode == "filter" || outMode == "filter-v" || outMode == "rank" {
		type hit struct {
			p    float64
			v, s string
		}
		var hits []hit
		for i, r := range rows {
			for _, a := range r.Answers { // the shortcut verbs ask exactly one question
				m := a.(map[string]any)
				hits = append(hits, hit{m["p"].(float64), m["verdict"].(string), inputs[i]})
			}
		}
		if outMode == "rank" {
			sort.SliceStable(hits, func(i, j int) bool { return hits[i].p > hits[j].p })
			if topN > 0 && topN < len(hits) {
				hits = hits[:topN]
			}
		}
		for _, h := range hits {
			switch {
			case outMode == "rank":
				pr("%.2f  %s", h.p, h.s)
			case (h.v == "yes") == (outMode == "filter"):
				pr("%s", h.s)
			}
		}
		return code
	}
	if !outRaw {
		names := keys(qs)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		head := []string{"VERDICT", "P"}
		if len(names) > 1 {
			head = head[:0]
			for _, k := range names {
				head = append(head, k, "P")
			}
		}
		fmt.Fprintln(w, strings.Join(append(head, "INPUT"), "\t"))
		for i, r := range rows {
			cols := make([]string, 0, 2*len(names)+1)
			for _, k := range names {
				if m, ok := r.Answers[k].(map[string]any); ok {
					cols = append(cols, m["verdict"].(string), fmt.Sprintf("%.2f", m["p"].(float64)))
				} else {
					cols = append(cols, "error", "-")
				}
			}
			fmt.Fprintln(w, strings.Join(append(cols, inputs[i]), "\t"))
		}
		w.Flush()
		return code
	}
	for _, r := range rows {
		b, _ := json.Marshal(r)
		pr("%s", b)
	}
	return code
}

// callCtx reads each --context value (TEXT or @file).
func callCtx(vs []string) []string {
	var out []string
	for _, v := range vs {
		out = append(out, text(v))
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ------------------------------------------------------------------ named questions + settings

// allQuestions merges global questions with the nearest .jevx folder's (the folder wins on a name clash).
func allQuestions(cfg core.Config) map[string]core.Question {
	m := map[string]core.Question{}
	for k, q := range cfg.Questions {
		m[k] = q
	}
	for k, q := range core.LocalQuestions() {
		m[k] = q
	}
	return m
}

// takeLocal removes --local from args; with it, question / context commands write to ./.jevx (created if missing).
func takeLocal(args []string) ([]string, bool) {
	var out []string
	local := false
	for _, a := range args {
		if a == "--local" || a == "-local" {
			local = true
			continue
		}
		out = append(out, a)
	}
	return out, local
}

// localDir is the folder --local writes to: the nearest existing .jevx, else ./.jevx.
func localDir() string {
	if d := core.LocalDir(); d != "" {
		return d
	}
	wd, _ := os.Getwd()
	d := filepath.Join(wd, ".jevx")
	if err := os.MkdirAll(d, 0o755); err != nil {
		die("%v", err)
	}
	return d
}

func cmdQuestion(args []string) {
	args, local := takeLocal(args)
	cfg := core.LoadConfig()
	if len(args) == 0 || args[0] == "list" {
		lq := core.LocalQuestions()
		if len(cfg.Questions)+len(lq) == 0 {
			pr("no named questions yet: jevx question add NAME --noul \"QUESTION\" [--local]")
			return
		}
		for _, k := range keys(cfg.Questions) {
			if _, shadowed := lq[k]; !shadowed {
				q := cfg.Questions[k]
				pr("%-16s %-7s %-7s %s", k, q.Type, "global", q.Instructions)
			}
		}
		for _, k := range keys(lq) {
			q := lq[k]
			pr("%-16s %-7s %-7s %s", k, q.Type, "folder", q.Instructions)
		}
		return
	}
	if local { // the folder's questions.json is edited instead of the global config
		lq := core.LocalQuestions()
		cfg.Questions = lq
	}
	switch args[0] {
	case "show":
		if len(args) < 2 {
			die("usage: jevx question show NAME")
		}
		q, ok := allQuestions(cfg)[args[1]]
		if !ok {
			die("no question named %q", args[1])
		}
		b, _ := json.MarshalIndent(q, "", "  ")
		pr("%s", b)
		return
	case "remove", "rm":
		if len(args) < 2 {
			die("usage: jevx question remove NAME")
		}
		delete(cfg.Questions, args[1])
	case "add":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			die(`usage: jevx question add NAME --noul "QUESTION" | --choice "QUESTION|k=desc;k2=desc" | --score "QUESTION|low;mid;high" [--yes 0.85 --no 0.15 --min 0.6]`)
		}
		fs := flag.NewFlagSet("question add", flag.ExitOnError)
		nq, cq, sq := fs.String("noul", "", "a yes/no question"), fs.String("choice", "", "QUESTION|key=desc;..."), fs.String("score", "", "QUESTION|level0;level1;...")
		yes, no, minC := fs.Float64("yes", -1, "yes at or above"), fs.Float64("no", -1, "no at or below"), fs.Float64("min", -1, "min confidence")
		qctx := fs.String("context", "", "background for this question only (TEXT or @file, read now)")
		_ = fs.Parse(args[2:])
		var q core.Question
		switch {
		case *nq != "":
			q = parseQ("noul", *nq)
		case *cq != "":
			q = parseQ("choice", *cq)
		case *sq != "":
			q = parseQ("score", *sq)
		default:
			die("give --noul, --choice or --score")
		}
		if *yes >= 0 {
			q.Yes = yes
		}
		if *no >= 0 {
			q.No = no
		}
		if *minC >= 0 {
			q.MinConfidence = minC
		}
		if *qctx != "" {
			q.Context = strings.TrimSpace(text(*qctx))
		}
		if cfg.Questions == nil {
			cfg.Questions = map[string]core.Question{}
		}
		cfg.Questions[args[1]] = q
	default:
		die("usage: jevx question list | show NAME | add NAME --noul|--choice|--score ... | remove NAME   [--local]")
	}
	if local {
		path := filepath.Join(localDir(), "questions.json")
		b, _ := json.MarshalIndent(cfg.Questions, "", "  ")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			die("%v", err)
		}
		pr("saved %s", path)
		return
	}
	if err := core.SaveConfig(cfg); err != nil {
		die("%v", err)
	}
	pr("saved %s", core.ConfigPath())
}

// settingKeys maps the config keys to their Settings fields.
var settingKeys = []string{"yes", "no", "min_confidence", "parallel", "retries", "timeout_s", "chunk", "ledger", "accept_min", "more_max", "cache", "cache_ttl_days"}

// cmdDefaults: jevx defaults [--profile P] | set KEY VALUE [--profile P] | unset KEY [--profile P].
func cmdDefaults(args []string) {
	cfg := core.LoadConfig()
	prof := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--profile" && i+1 < len(args) {
			prof, i = args[i+1], i+1
			continue
		}
		rest = append(rest, args[i])
	}
	target := &cfg.Defaults
	var p core.Profile
	if prof != "" {
		var ok bool
		if p, ok = cfg.Profiles[prof]; !ok {
			die("no profile %q", prof)
		}
		target = &p.Defaults
	}
	if len(rest) == 0 || rest[0] == "show" {
		eff := cfg.Settings(p)
		m, _ := json.Marshal(eff)
		own, _ := json.Marshal(*target)
		var em, om map[string]any
		_ = json.Unmarshal(m, &em)
		_ = json.Unmarshal(own, &om)
		for _, k := range settingKeys {
			src := "default"
			if _, ok := om[k]; ok {
				src = map[bool]string{true: "profile " + prof, false: "config"}[prof != ""]
			}
			pr("%-16s %-8v %s", k, em[k], src)
		}
		return
	}
	if len(rest) < 2 || (rest[0] == "set" && len(rest) < 3) {
		die("usage: jevx defaults [show] | set KEY VALUE | unset KEY  [--profile P]   keys: %s", strings.Join(settingKeys, ", "))
	}
	b, _ := json.Marshal(*target)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{}
	}
	k := rest[1]
	if !contains(settingKeys, k) {
		die("unknown key %q (keys: %s)", k, strings.Join(settingKeys, ", "))
	}
	switch rest[0] {
	case "set":
		var v any
		if json.Unmarshal([]byte(rest[2]), &v) != nil {
			die("%s: want a number or true/false, got %q", k, rest[2])
		}
		m[k] = v
	case "unset":
		delete(m, k)
	default:
		die("usage: jevx defaults set KEY VALUE | unset KEY")
	}
	b, _ = json.Marshal(m)
	var ns core.Settings
	if err := json.Unmarshal(b, &ns); err != nil {
		die("%s: %v", k, err)
	}
	*target = ns
	if prof != "" {
		cfg.Profiles[prof] = p
	}
	if err := core.SaveConfig(cfg); err != nil {
		die("%v", err)
	}
	pr("saved %s", core.ConfigPath())
}

// cmdContext shows the context sent with every call and where each part comes from. jevx never writes it: the user
// edits the "## Jev" section of their own agent files by hand.
func cmdContext(args []string) {
	if len(args) > 0 && args[0] != "show" {
		die("jevx only reads context. Edit the \"## Jev\" section of your AGENTS.md / CLAUDE.md (global or in the repo); see: jevx context")
	}
	cfg := core.LoadConfig()
	sect := cfg.LocalSection()
	show := func(label, body, from string) {
		if body == "" {
			pr("%-16s (none)  %s", label, from)
			return
		}
		pr("%-16s %s\n%-16s %s", label, trunc(strings.ReplaceAll(body, "\n", " "), 160), "", "from "+from)
	}
	g, gf := cfg.GlobalContext()
	if gf == "" {
		gf = "add a \"## " + sect + "\" section to one of: " + strings.Join(cfg.GlobalContextFiles(), ", ")
	}
	show("global", g, gf)
	l, lf := cfg.LocalContext()
	if lf == "" {
		lf = "add a \"## " + sect + "\" section to this repo's " + strings.Join(cfg.LocalContextFiles(), " or ")
	}
	show("folder", l, lf)
	for _, k := range keys(allQuestions(cfg)) {
		if c := allQuestions(cfg)[k].Context; c != "" {
			show("question "+k, c, "its saved question")
		}
	}
	pr("\norder sent: the item first, then global, folder and --context on the call; a saved question's own context goes with that question")
}

// cmdVerb is the shortcut layer: each verb is one question through cmdAsk, with the same config, context and settings.
//
//	jevx is "QUESTION"|NAME [input]           yes / no / unsure + P; exit 0 / 1 / 3 (4 on error)
//	jevx pick "QUESTION" key=desc ... [input]  the chosen key + confidence; exit 3 when unsure
//	jevx filter "QUESTION"|NAME [lines] [-v]   the input lines whose answer is yes (-v: the rest), like grep
//	jevx rank "QUESTION"|NAME [lines] [--top N] every line with P(yes), best first
//
// Input: stdin, --in, --lines or --states, as for ask. filter / rank read lines from stdin when none is given.
func cmdVerb(verb string, args []string) {
	valued := map[string]bool{"in": true, "lines": true, "states": true, "questions": true, "profile": true, "yes": true,
		"no": true, "min": true, "parallel": true, "context": true, "top": true, "memory": true, "memory-k": true, "memory-budget": true}
	var pos, flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			f := strings.TrimLeft(a, "-")
			switch {
			case f == "v" && verb == "filter":
				outMode = "filter-v"
				continue
			case f == "top" && i+1 < len(args):
				topN, _ = strconv.Atoi(args[i+1])
				i++
				continue
			}
			flags = append(flags, a)
			if valued[f] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) == 0 {
		die("usage: jevx %s \"QUESTION\"|NAME ...  (see: jevx help)", verb)
	}
	q := pos[0]
	var qflag []string
	if saved, ok := allQuestions(core.LoadConfig())[q]; ok && (verb != "pick") {
		if saved.Type != "noul" && verb != "is" {
			die("%s needs a yes/no question; %q is a %s", verb, q, saved.Type)
		}
		qflag = []string{q}
	} else if verb == "pick" {
		if len(pos) < 3 {
			die(`usage: jevx pick "QUESTION" key=desc key2=desc ...`)
		}
		qflag = []string{"--choice", "pick=" + q + "|" + strings.Join(pos[1:], ";")}
	} else {
		qflag = []string{"--noul", verb + "=" + q}
	}
	has := func(names ...string) bool {
		for _, f := range flags {
			for _, n := range names {
				if strings.TrimLeft(f, "-") == n || strings.HasPrefix(strings.TrimLeft(f, "-"), n+"=") {
					return true
				}
			}
		}
		return false
	}
	switch verb {
	case "is", "pick":
		outMode = "bare"
	case "filter", "rank":
		if outMode == "" {
			outMode = verb
		}
		if !has("lines", "states", "in") {
			flags = append(flags, "--lines", "-")
		}
	}
	cmdAsk(append(qflag, flags...))
}

// cmdCache: jevx cache stat | clear | dir.
func cmdCache(args []string) {
	cfg := core.LoadConfig()
	dir := cfg.CacheDirPath()
	switch {
	case len(args) == 0 || args[0] == "stat":
		n, b := core.CacheStat(dir)
		on := *cfg.Settings(core.Profile{}).Cache
		pr("cache    %s (%s)\nanswers  %d  (%.1f KB)\nttl      %d days   change: jevx cache enable|disable|ttl DAYS|dir PATH", dir, map[bool]string{true: "on", false: "off"}[on], n, float64(b)/1024, *cfg.Settings(core.Profile{}).CacheTTLDays)
	case args[0] == "clear":
		pr("removed %d stored answers from %s", core.CacheClear(dir), dir)
	case args[0] == "enable" || args[0] == "disable" || args[0] == "ttl":
		switch args[0] {
		case "ttl":
			d, err := strconv.Atoi(strings.Join(args[1:], ""))
			if err != nil || d < 1 {
				die("usage: jevx cache ttl DAYS")
			}
			cfg.Defaults.CacheTTLDays = &d
		default:
			on := args[0] == "enable"
			cfg.Defaults.Cache = &on
		}
		if err := core.SaveConfig(cfg); err != nil {
			die("%v", err)
		}
		cmdCache([]string{"stat"})
	case args[0] == "dir" && len(args) > 1:
		cfg.CacheDir = args[1]
		if err := core.SaveConfig(cfg); err != nil {
			die("%v", err)
		}
		pr("cache folder: %s", cfg.CacheDirPath())
	case args[0] == "dir":
		pr("%s", dir)
	default:
		die("usage: jevx cache [stat] | enable | disable | ttl DAYS | dir [PATH] | clear   (also: jevx config cache …; saved in %s)", core.ConfigPath())
	}
}
