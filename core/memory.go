package core

// Memory: retrieval without embeddings, so an ask can carry the facts it needs.
//
// A memory is a folder of distilled markdown (a cited wiki). `index` cuts it into heading sections with line ranges;
// a question's input is the BM25 query; the best sections are appended AFTER the item, within a character budget,
// each with its citation (page#heading Lstart-Lend). Pages named in the input come first, then pinned pages (notices),
// then BM25.
//
// BM25 is the CiteNexus BM25-lite formula (k1=1.5, b=0.75, idf = ln(1 + (N-n+0.5)/(n+0.5)), query terms as a set),
// with this tokenizer: lowercase word runs, English stopwords out, a light suffix stemmer, heading terms counted twice.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// MemSection is one heading section of one page.
type MemSection struct {
	Page  string `json:"page"`
	Head  string `json:"head"`
	Start int    `json:"start"` // 1-based line range in the page
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// MemCite is one [[repo:path:a-b]] citation inside a page, with the hash of those lines when the memory was indexed.
type MemCite struct {
	Repo, Path string
	From, To   int
	Hash       string
}

// Memory is a registered folder and its index.
type Memory struct {
	Name         string               `json:"name"`
	Dir          string               `json:"dir"`
	Pins         []string             `json:"pins,omitempty"`  // pages always offered first (e.g. notices.md)
	Repos        map[string]string    `json:"repos,omitempty"` // repo name in [[repo:path:lines]] -> local checkout, for `check`
	Indexed      time.Time            `json:"indexed"`
	Sections     []MemSection         `json:"sections"`
	Cites        map[string][]MemCite `json:"cites,omitempty"`        // page -> its citations
	Stale        map[string]string    `json:"stale,omitempty"`        // page -> why; stale pages are not retrieved
	Unverifiable map[string][]string  `json:"unverifiable,omitempty"` // page -> cites that could not be read at index time
	PageHash     map[string]string    `json:"page_hash,omitempty"`
}

// MemHit is one retrieved section, as reported in --json / --raw.
type MemHit struct {
	Page  string  `json:"page"`
	Head  string  `json:"heading"`
	Lines string  `json:"lines"`
	Score float64 `json:"score"`
	Why   string  `json:"why"` // named | pinned | bm25
	text  string
}

func MemoryDir() string { return filepath.Join(DataDir(), "memory") }

var memName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func memPath(name string) (string, error) {
	if !memName.MatchString(name) {
		return "", fmt.Errorf("memory name %q: use letters, digits, . _ -", name)
	}
	return filepath.Join(MemoryDir(), name+".json"), nil
}

// LoadMemory reads a registered memory.
func LoadMemory(name string) (*Memory, error) {
	p, err := memPath(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no memory %q (jevx memory add %s DIR)", name, name)
	}
	if err != nil {
		return nil, err
	}
	var m Memory
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("memory %q: %v", name, err)
	}
	return &m, nil
}

// Save writes the memory (0600: pages may be private).
func (m *Memory) Save() error {
	p, err := memPath(m.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(MemoryDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(m)
	return os.WriteFile(p, b, 0o600)
}

// ListMemories returns the registered memory names.
func ListMemories() []string {
	es, _ := os.ReadDir(MemoryDir())
	var out []string
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out
}

var (
	mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdCite    = regexp.MustCompile(`\[\[([A-Za-z0-9._-]+):([^\]:]+):(\d+)(?:-(\d+))?\]\]`)
)

// Sections cuts a markdown page into heading sections (code fences are not headings); a long body is cut into
// pieces of about maxc characters on line boundaries, each keeping its own line range.
func Sections(page, md string, maxc int) []MemSection {
	var out []MemSection
	head, start, fence := page, 1, false
	var buf []string
	flush := func(end int) {
		var piece []string
		pStart, n := start, 0
		emit := func(last int) {
			t := strings.TrimSpace(strings.Join(piece, "\n"))
			if t == "" {
				return
			}
			first := pStart // the range covers the text, not the blank lines around it
			for j := 0; j < len(piece) && strings.TrimSpace(piece[j]) == ""; j++ {
				first++
			}
			for j := len(piece) - 1; j >= 0 && strings.TrimSpace(piece[j]) == ""; j-- {
				last--
			}
			out = append(out, MemSection{Page: page, Head: head, Start: first, End: last, Text: t})
		}
		for i, l := range buf {
			if n > 0 && n+len(l) > maxc {
				emit(start + i - 1)
				piece, pStart, n = nil, start+i, 0
			}
			piece, n = append(piece, l), n+len(l)+1
		}
		emit(end)
	}
	lines := strings.Split(md, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fence = !fence
		}
		if m := mdHeading.FindStringSubmatch(l); m != nil && !fence {
			flush(i)
			head, start, buf = strings.TrimSpace(m[2]), i+2, nil
			continue
		}
		buf = append(buf, l)
	}
	flush(len(lines))
	return out
}

// IndexMemory (re)builds the index from the folder: sections, page hashes and the hash of every cited line range.
func (m *Memory) Index() error {
	var secs []MemSection
	cites, hashes, unver := map[string][]MemCite{}, map[string]string{}, map[string][]string{}
	err := filepath.WalkDir(m.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(m.Dir, p)
		rel = filepath.ToSlash(rel)
		secs = append(secs, Sections(rel, string(b), 900)...)
		hashes[rel] = sum(b)
		for _, c := range parseCites(string(b)) {
			h, problem := m.citeHash(c)
			if problem != "" { // never stored as a hash: a cite we could not read must not look verified later
				unver[rel] = append(unver[rel], fmt.Sprintf("%s:%s:%d-%d %s", c.Repo, c.Path, c.From, c.To, problem))
				h = ""
			}
			c.Hash = h
			cites[rel] = append(cites[rel], c)
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.Sections, m.Cites, m.PageHash, m.Stale, m.Unverifiable, m.Indexed = secs, cites, hashes, nil, unver, time.Now().UTC()
	return nil
}

func parseCites(md string) []MemCite {
	seen := map[string]bool{}
	var out []MemCite
	for _, s := range mdCite.FindAllStringSubmatch(md, -1) {
		from, _ := strconv.Atoi(s[3])
		to := from
		if s[4] != "" {
			to, _ = strconv.Atoi(s[4])
		}
		k := s[1] + ":" + s[2] + ":" + s[3] + "-" + strconv.Itoa(to)
		if !seen[k] {
			seen[k] = true
			out = append(out, MemCite{Repo: s[1], Path: s[2], From: from, To: to})
		}
	}
	return out
}

// citeHash hashes the cited lines in the repo checkout. It returns ("", "") when the repo is not configured (nothing to
// check), and a problem instead of a hash when the path leaves the repo root, is missing, or the range is out of bounds.
func (m *Memory) citeHash(c MemCite) (string, string) {
	root, ok := m.Repos[c.Repo]
	if !ok {
		return "", ""
	}
	full := filepath.Join(root, filepath.FromSlash(c.Path))
	if rel, err := filepath.Rel(root, full); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "escapes the repo root"
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", "missing"
	}
	ls := strings.Split(string(b), "\n")
	if c.From < 1 || c.To > len(ls) || c.From > c.To {
		return "", "out of range"
	}
	return sum([]byte(strings.Join(ls[c.From-1:c.To], "\n"))), ""
}

// citeMoved is true when the cited lines still exist, unchanged, somewhere else in the file (lines were inserted or
// removed above them): the fact is still there, only its line numbers moved.
func (m *Memory) citeMoved(c MemCite) bool {
	root, ok := m.Repos[c.Repo]
	if !ok || c.Hash == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.Path)))
	if err != nil {
		return false
	}
	ls, n := strings.Split(string(b), "\n"), c.To-c.From+1
	for i := 0; i+n <= len(ls); i++ {
		if sum([]byte(strings.Join(ls[i:i+n], "\n"))) == c.Hash {
			return true
		}
	}
	return false
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:8]) }

// Check marks a page stale when the page itself changed since `index`, or when any line range it cites changed in
// the configured repo. Stale pages are left out of retrieval until the page is fixed and re-indexed.
func (m *Memory) Check() map[string]string {
	stale, _ := m.Drift()
	for page, cs := range m.Cites {
		if _, done := stale[page]; done {
			continue
		}
		for _, c := range cs {
			if c.Hash == "" {
				continue
			}
			if now, _ := m.citeHash(c); now != c.Hash && !m.citeMoved(c) {
				stale[page] = fmt.Sprintf("cited %s:%s:%d-%d changed", c.Repo, c.Path, c.From, c.To)
				break
			}
		}
	}
	m.Stale = stale
	return stale
}

// Drift compares the folder with the index: pages edited or removed since `index`, and .md pages the index has not
// seen. It reads every page, so the answer is never older than the files.
func (m *Memory) Drift() (changed map[string]string, added []string) {
	changed = map[string]string{}
	for page, h := range m.PageHash {
		b, err := os.ReadFile(filepath.Join(m.Dir, filepath.FromSlash(page)))
		if err != nil {
			changed[page] = "page missing"
		} else if sum(b) != h {
			changed[page] = "page changed since index (run jevx memory index)"
		}
	}
	_ = filepath.WalkDir(m.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(m.Dir, p)
		if _, ok := m.PageHash[filepath.ToSlash(rel)]; !ok {
			added = append(added, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(added)
	return changed, added
}

// Freshen leaves pages that changed on disk since `index` out of retrieval (their indexed text is out of date), without
// saving, and returns a one-line warning naming what drifted, or "" when the index matches the folder.
func (m *Memory) Freshen() string {
	changed, added := m.Drift()
	if len(changed) == 0 && len(added) == 0 {
		return ""
	}
	if m.Stale == nil {
		m.Stale = map[string]string{}
	}
	for p, why := range changed {
		m.Stale[p] = why
	}
	return fmt.Sprintf("memory %s: %d pages changed or removed and %d new since index %s; changed pages are left out until: jevx memory index %s",
		m.Name, len(changed), len(added), m.Indexed.Format("2006-01-02 15:04"), m.Name)
}

var memStop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the and or but if then else of to in on at by for with from as is are was were be been being it its
this that these those do does did not no yes can could should would will shall may might must i you he she we they them our your what which
who whom whose when where why how all any each some such than too very just also into over under about after before so there here out up
down off again further once more most other own same only both few nor s t don now`) {
		memStop[w] = true
	}
}

// memStem is a light suffix stemmer: enough to match plural and verb forms, never below 3 letters. "es" goes only
// after s/x/z/ch/sh (boxes, classes), so "licences" and "licence" meet at "licence".
func memStem(w string) string {
	cut := func(suf string) (string, bool) {
		if strings.HasSuffix(w, suf) && len(w)-len(suf) >= 3 {
			return strings.TrimSuffix(w, suf), true
		}
		return w, false
	}
	if r, ok := cut("ing"); ok {
		return r
	}
	if r, ok := cut("ed"); ok {
		return r
	}
	if r, ok := cut("es"); ok {
		for _, end := range []string{"s", "x", "z", "ch", "sh"} {
			if strings.HasSuffix(r, end) {
				return r
			}
		}
	}
	if strings.HasSuffix(w, "ss") {
		return w
	}
	r, _ := cut("s")
	return r
}

// MemTokens lowercases, splits on non-word runes, drops stopwords and 1-rune tokens, and stems.
func MemTokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
	out := f[:0]
	for _, w := range f {
		if len([]rune(w)) > 1 && !memStop[w] {
			out = append(out, memStem(w))
		}
	}
	return out
}

// bm25 ranks the sections for a query (CiteNexus formula); heading terms count twice. Zero scores are dropped.
func bm25(secs []MemSection, query string) []float64 {
	q := map[string]bool{}
	for _, t := range MemTokens(query) {
		q[t] = true
	}
	docs := make([]map[string]int, len(secs))
	lens, total := make([]float64, len(secs)), 0.0
	df := map[string]int{}
	for i, s := range secs {
		toks := append(MemTokens(s.Head), MemTokens(s.Head)...)
		toks = append(toks, MemTokens(s.Text)...)
		c := map[string]int{}
		for _, t := range toks {
			c[t]++
		}
		for t := range q {
			if c[t] > 0 {
				df[t]++
			}
		}
		docs[i], lens[i] = c, float64(len(toks))
		total += lens[i]
	}
	n := float64(len(secs))
	avg := total / math.Max(n, 1)
	if avg == 0 {
		avg = 1
	}
	const k1, b = 1.5, 0.75
	scores := make([]float64, len(secs))
	for i, c := range docs {
		for t := range q {
			tf := float64(c[t])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			scores[i] += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*lens[i]/avg))
		}
	}
	return scores
}

// Retrieve picks sections for an input: pages named in it, then pinned pages (each its best-scoring section, or its
// first), then the top k by BM25, all within budget characters of "[page#heading] text". Stale pages are skipped.
func (m *Memory) Retrieve(input string, k, budget int) []MemHit {
	var live []MemSection
	for _, s := range m.Sections {
		if _, bad := m.Stale[s.Page]; !bad {
			live = append(live, s)
		}
	}
	scores := bm25(live, input)
	toks := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(input), func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') }) {
		toks[w] = true
	}
	best := func(page string) int {
		bi := -1
		for i, s := range live {
			if s.Page == page && (bi < 0 || scores[i] > scores[bi]) {
				bi = i
			}
		}
		return bi
	}
	pages := map[string]bool{}
	for _, s := range live {
		pages[s.Page] = true
	}
	var order []int
	why := map[int]string{}
	add := func(i int, w string) {
		if i < 0 {
			return
		}
		if _, dup := why[i]; !dup {
			order, why[i] = append(order, i), w
		}
	}
	var named []string
	for p := range pages {
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(p), ".md"))
		first := strings.SplitN(stem, "-", 2)[0] // "marketing-team.md" is named by "marketing"; a short first part ("go-", "how-") is not enough
		if toks[stem] || (len([]rune(first)) >= 4 && toks[first]) {
			named = append(named, p)
		}
	}
	sort.Strings(named)
	for _, p := range named {
		add(best(p), "named")
	}
	for _, p := range m.Pins {
		if pages[p] {
			add(best(p), "pinned")
		}
	}
	idx := make([]int, len(live))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	for j := 0; j < len(idx) && j < k; j++ {
		if scores[idx[j]] > 0 {
			add(idx[j], "bm25")
		}
	}
	var hits []MemHit
	used := 0
	for _, i := range order {
		s := live[i]
		chunk := []rune(fmt.Sprintf("[%s#%s] %s", s.Page, s.Head, s.Text)) // the budget counts runes, like MaxContextChars
		if used+len(chunk) > budget {                                      // a cut chunk must still say something; a short whole section is kept as it is
			chunk = chunk[:max(0, budget-used)]
			if len(chunk) < 80 {
				break
			}
		}
		hits = append(hits, MemHit{Page: s.Page, Head: s.Head, Lines: fmt.Sprintf("L%d-L%d", s.Start, s.End),
			Score: math.Round(scores[i]*1000) / 1000, Why: why[i], text: string(chunk)})
		used += len(chunk) + 1
	}
	return hits
}

// MemoryState puts the item FIRST and the notes after it: the server keeps only the first max_len tokens, so notes
// may be cut, never the item. A JSON object state keeps its fields in order and gains a last "memory" field (WithContext
// then adds "context" after it), the same rule WithContext follows.
func MemoryState(item string, hits []MemHit) string {
	if len(hits) == 0 {
		return item
	}
	notes := make([]string, len(hits))
	for i, h := range hits {
		notes[i] = h.text
	}
	trimmed := strings.TrimSpace(item)
	var obj map[string]any
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") && json.Unmarshal([]byte(trimmed), &obj) == nil {
		merged := strings.Join(notes, "\n")
		raw, taken := obj["memory"]
		if !taken {
			nb, _ := json.Marshal(merged)
			body := strings.TrimSpace(strings.TrimSuffix(trimmed, "}"))
			sep := ","
			if strings.HasSuffix(body, "{") {
				sep = ""
			}
			return body + sep + `"memory":` + string(nb) + "}"
		}
		// rare: the state carried its own memory; merge (new notes first, the old after) and re-emit it as the last field
		if old, ok := raw.(string); ok {
			if old != "" {
				merged += "\n" + old
			}
		} else if raw != nil {
			ob, _ := json.Marshal(raw)
			merged += "\n" + string(ob)
		}
		delete(obj, "memory")
		b, _ := json.Marshal(obj)
		body := strings.TrimSuffix(string(b), "}")
		if body != "{" {
			body += ","
		}
		nb, _ := json.Marshal(merged)
		return body + `"memory":` + string(nb) + "}"
	}
	return item + "\n\nRelevant notes (memory):\n" + strings.Join(notes, "\n")
}

var (
	diffNum  = regexp.MustCompile(`\d+(?:[.,]\d+)*`)
	diffCode = regexp.MustCompile("`([^`]+)`")
	diffName = regexp.MustCompile(`\b(?:[A-Za-z]*[a-z][A-Z][A-Za-z0-9]*|[A-Z][A-Za-z]*\d[A-Za-z0-9]*|[A-Z]{2,}[0-9]*)\b`)
)

// MemDiff lists the facts in the item that the retrieved notes do not contain: numbers (versions, ports, 1,024),
// `code spans` and name-like tokens (ModernBERT, Qwen3, A100, MIT). It only judges when the notes are about the item
// (at least 2 shared content terms); otherwise it returns nothing, because a missing fact proves nothing about notes
// that are off-topic. With --memory-strict, any listed fact makes a yes/no answer "no".
func MemDiff(item string, hits []MemHit) []string {
	if len(hits) == 0 {
		return nil
	}
	var notes strings.Builder
	for _, h := range hits {
		notes.WriteString(h.text)
		notes.WriteString("\n")
	}
	n := notes.String()
	nl := strings.ToLower(n)
	nt := map[string]bool{}
	for _, t := range MemTokens(n) {
		nt[t] = true
	}
	shared := 0
	for t := range uniq(MemTokens(item)) {
		if nt[t] {
			shared++
		}
	}
	if shared < 2 {
		return nil
	}
	nums := map[string]bool{}
	for _, x := range diffNum.FindAllString(n, -1) {
		nums[strings.ReplaceAll(x, ",", "")] = true
	}
	var out []string
	seen := map[string]bool{}
	miss := func(kind, v string) {
		if !seen[kind+v] {
			seen[kind+v] = true
			out = append(out, kind+" "+v+" is not in the notes")
		}
	}
	for _, x := range diffNum.FindAllString(item, -1) {
		if v := strings.TrimRight(strings.ReplaceAll(x, ",", ""), "."); !nums[v] {
			miss("number", x)
		}
	}
	for _, m := range diffCode.FindAllStringSubmatch(item, -1) {
		if !strings.Contains(nl, strings.ToLower(m[1])) {
			miss("code", "`"+m[1]+"`")
		}
	}
	for _, x := range diffName.FindAllString(item, -1) {
		if !strings.Contains(nl, strings.ToLower(x)) {
			miss("name", x)
		}
	}
	return out
}

func uniq(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}
