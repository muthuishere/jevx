package main

// jevx memory: a folder of distilled markdown that an ask can retrieve from (BM25, no embeddings).
//
//   jevx memory add NAME DIR [--pin PAGE.md ...] [--repo NAME=PATH ...]   register and index
//   jevx memory index NAME                                             rebuild after the pages change
//   jevx memory check NAME                                             mark pages whose cited lines changed (stale pages are not retrieved)
//   jevx memory show NAME "input" [--k 3 --budget 2000]                what an ask would carry, without asking
//   jevx memory list
//   jevx ask --memory NAME ...                                         the item first, then the notes, each with its citation

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muthuishere/jevx/core"
)

func cmdMemory(args []string) {
	if len(args) == 0 {
		die("usage: jevx memory add NAME DIR [--pin PAGE.md] [--repo NAME=PATH] | index NAME | check NAME | show NAME \"input\" | list")
	}
	switch args[0] {
	case "list", "ls":
		for _, n := range core.ListMemories() {
			m, err := core.LoadMemory(n)
			if err != nil {
				pr("%-16s error: %v", n, err)
				continue
			}
			changed, added := m.Drift()
			stale := len(m.Stale)
			for p := range changed {
				if _, ok := m.Stale[p]; !ok {
					stale++
				}
			}
			line := fmt.Sprintf("%-16s %4d sections  %2d stale  %s  indexed %s", n, len(m.Sections), stale, m.Dir, m.Indexed.Format("2006-01-02 15:04"))
			if len(changed) > 0 || len(added) > 0 {
				line += fmt.Sprintf("  (%d changed, %d new since index: jevx memory index %s)", len(changed), len(added), n)
			}
			pr("%s", line)
		}
	case "add":
		fs := flag.NewFlagSet("memory add", flag.ExitOnError)
		var pins, repos multi
		fs.Var(&pins, "pin", "a page always offered first, e.g. notices.md (repeat)")
		fs.Var(&repos, "repo", "NAME=PATH: a checkout for [[NAME:path:lines]] citations, so check can see them change (repeat)")
		names, flags := memArgs(args[1:], map[string]bool{"pin": true, "repo": true})
		_ = fs.Parse(flags)
		if len(names) != 2 {
			die("usage: jevx memory add NAME DIR [--pin PAGE.md] [--repo NAME=PATH]")
		}
		dir, err := filepath.Abs(names[1])
		if err != nil {
			die("%v", err)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			die("%s is not a folder", dir)
		}
		m := &core.Memory{Name: names[0], Dir: dir, Pins: pins, Repos: map[string]string{}}
		for _, r := range repos {
			k, v, ok := strings.Cut(r, "=")
			if !ok {
				die("--repo wants NAME=PATH, got %q", r)
			}
			abs, _ := filepath.Abs(v)
			m.Repos[k] = abs
		}
		indexAndSave(m)
	case "index":
		indexAndSave(mustMemory(args))
	case "check":
		m := mustMemory(args)
		stale := m.Check()
		if err := m.Save(); err != nil {
			die("%v", err)
		}
		pages := make([]string, 0, len(stale))
		for p := range stale {
			pages = append(pages, p)
		}
		sort.Strings(pages)
		for _, p := range pages {
			pr("stale  %-32s %s", p, stale[p])
		}
		printUnverifiable(m)
		pr("%d of %d pages stale; stale pages are left out of retrieval until fixed and re-indexed; %d pages have unverifiable cites",
			len(stale), len(m.PageHash), len(m.Unverifiable))
		if len(stale) > 0 {
			os.Exit(1)
		}
	case "show":
		fs := flag.NewFlagSet("memory show", flag.ExitOnError)
		k := fs.Int("k", 3, "BM25 sections after named and pinned pages")
		budget := fs.Int("budget", 2000, "characters of notes")
		asJSON := fs.Bool("json", false, "the hits as JSON")
		names, flags := memArgs(args[1:], map[string]bool{"k": true, "budget": true})
		_ = fs.Parse(flags)
		if len(names) != 2 {
			die("usage: jevx memory show NAME \"input\" [--k 3 --budget 2000 --json]")
		}
		m, err := core.LoadMemory(names[0])
		if err != nil {
			die("%v", err)
		}
		warnDrift(m)
		hits := m.Retrieve(names[1], *k, *budget)
		if *asJSON {
			b, _ := json.Marshal(map[string]any{"hits": hits, "diff": core.MemDiff(names[1], hits)})
			pr("%s", b)
			return
		}
		pr("%s", core.MemoryState(names[1], hits))
	default:
		die("unknown memory command %q (add, index, check, show, list)", args[0])
	}
}

func mustMemory(args []string) *core.Memory {
	if len(args) != 2 {
		die("usage: jevx memory %s NAME", args[0])
	}
	m, err := core.LoadMemory(args[1])
	if err != nil {
		die("%v", err)
	}
	return m
}

func indexAndSave(m *core.Memory) {
	if err := m.Index(); err != nil {
		die("index %s: %v", m.Dir, err)
	}
	if err := m.Save(); err != nil {
		die("%v", err)
	}
	cites := 0
	for _, cs := range m.Cites {
		cites += len(cs)
	}
	pr("memory %s: %d sections from %d pages, %d citations, %s", m.Name, len(m.Sections), len(m.PageHash), cites, m.Dir)
	printUnverifiable(m)
}

// printUnverifiable warns about cites that could not be read at index time: check cannot vouch for those pages.
// warnDrift leaves pages edited since the index out of retrieval and says so on stderr, so an out-of-date index is
// never served silently.
func warnDrift(m *core.Memory) {
	if w := m.Freshen(); w != "" {
		fmt.Fprintf(os.Stderr, "jevx: %s\n", w)
	}
}

func printUnverifiable(m *core.Memory) {
	pages := make([]string, 0, len(m.Unverifiable))
	for p := range m.Unverifiable {
		pages = append(pages, p)
	}
	sort.Strings(pages)
	total := 0
	for _, p := range pages {
		cs := m.Unverifiable[p]
		total += len(cs)
		more := ""
		if len(cs) > 2 {
			more = fmt.Sprintf(" (+%d more)", len(cs)-2)
		}
		pr("warning: %-28s %3d cites check cannot verify, e.g. %s%s", p, len(cs), strings.Join(cs[:min(2, len(cs))], "; "), more)
	}
	if total > 0 {
		pr("%d unverifiable cites: a --repo checkout at the commit the pages were written from makes them checkable", total)
	}
}

// memAsk is the memory an ask carries (nil: none).
type memAsk struct {
	m         *core.Memory
	k, budget int
	strict    bool // --memory-strict: a fact in the item that the notes lack makes every yes/no answer "no"
}

// apply returns the state with notes after the item, the hits for --json / --raw, and the facts the notes lack.
func (ma *memAsk) apply(state string) (string, []core.MemHit, []string) {
	if ma == nil || ma.m == nil {
		return state, nil, nil
	}
	hits := ma.m.Retrieve(state, ma.k, ma.budget)
	return core.MemoryState(state, hits), hits, core.MemDiff(state, hits)
}

// strictVerdict is the verdict after --memory-strict: a noul becomes "no" when the notes contradict a fact.
func (ma *memAsk) strictVerdict(q core.Question, v string, diff []string) string {
	if ma != nil && ma.strict && len(diff) > 0 && q.Type == "noul" {
		return "no"
	}
	return v
}

// memArgs separates flags from positional arguments. Unlike splitArgs it never splits a positional on commas: an
// input text or a folder path is one argument.
func memArgs(args []string, valued map[string]bool) (pos, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
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
