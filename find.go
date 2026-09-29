package main

// Find: fuzzy search for file and folder names below the current folder. The
// tree is indexed in the background and results update as batches arrive.

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const (
	maxFindEntries = 200_000 // stop indexing here so a huge tree can't stall us
	maxFindResults = 200
	findBatchEvery = 100 * time.Millisecond
)

type findState struct {
	id        int
	root      string
	paths     []string // relative to root; folders end in "/"
	lower     []string // lowercased paths, for case-insensitive matching
	results   []findMatch
	done      bool
	truncated bool
	cancel    context.CancelFunc
	ch        chan findBatchMsg
}

type findMatch struct {
	path  string
	score int
	pos   []int // byte offsets of matched characters, for highlighting
}

type findBatchMsg struct {
	id        int
	paths     []string
	done      bool
	truncated bool
}

var findSeq int

func (m *model) openFind() {
	findSeq++
	ctx, cancel := context.WithCancel(context.Background())
	f := &findState{id: findSeq, root: m.cwd, cancel: cancel, ch: make(chan findBatchMsg, 4)}
	go indexTree(ctx, f.root, m.cfg.ShowHidden, f.id, f.ch)
	m.modal = &modal{kind: mFind, title: "find below " + tildePath(m.cwd), in: newInput(""), find: f}
}

func (f *findState) next() tea.Cmd {
	ch := f.ch
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// indexTree walks root and sends the relative paths it finds in batches.
// Symlinks aren't followed, so the walk can't loop.
func indexTree(ctx context.Context, root string, hidden bool, id int, ch chan<- findBatchMsg) {
	defer close(ch)
	send := func(msg findBatchMsg) bool {
		select {
		case ch <- msg:
			return true
		case <-ctx.Done():
			return false
		}
	}
	var batch []string
	n, last, truncated := 0, time.Now(), false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if p == root {
			return nil
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !hidden && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			rel += "/"
		}
		batch = append(batch, rel)
		if n++; n >= maxFindEntries {
			truncated = true
			return filepath.SkipAll
		}
		if time.Since(last) >= findBatchEvery {
			if !send(findBatchMsg{id: id, paths: batch}) {
				return filepath.SkipAll
			}
			batch, last = nil, time.Now()
		}
		return nil
	})
	send(findBatchMsg{id: id, paths: batch, done: true, truncated: truncated})
}

func (m model) handleFindBatch(msg findBatchMsg) tea.Cmd {
	if m.modal == nil || m.modal.kind != mFind || m.modal.find.id != msg.id {
		return nil // a closed or replaced search
	}
	f := m.modal.find
	for _, p := range msg.paths {
		f.paths = append(f.paths, p)
		f.lower = append(f.lower, strings.ToLower(p))
	}
	f.done, f.truncated = msg.done, msg.truncated
	f.rescore(m.modal.in.value())
	m.modal.sel = min(m.modal.sel, max(0, len(f.results)-1))
	if msg.done {
		return nil
	}
	return f.next()
}

func (f *findState) rescore(query string) {
	terms := strings.Fields(query)
	f.results = f.results[:0]
	if len(terms) == 0 {
		return
	}
	for i, p := range f.paths {
		if mt, ok := matchPath(p, f.lower[i], terms); ok {
			f.results = append(f.results, mt)
		}
	}
	sort.Slice(f.results, func(i, j int) bool {
		a, b := f.results[i], f.results[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	if len(f.results) > maxFindResults {
		f.results = f.results[:maxFindResults]
	}
}

// matchPath scores path against every term (all must match). Matching is
// case-insensitive unless the term has a capital letter.
func matchPath(path, lower string, terms []string) (findMatch, bool) {
	mt := findMatch{path: path}
	for _, t := range terms {
		hay, needle := lower, t
		if strings.ToLower(t) != t {
			hay = path
		} else {
			needle = strings.ToLower(t)
		}
		score, pos, ok := fuzzyScore(hay, needle)
		if !ok {
			return mt, false
		}
		mt.score += score
		mt.pos = append(mt.pos, pos...)
	}
	sort.Ints(mt.pos)
	return mt, true
}

// fuzzyScore finds needle's characters in order in hay and scores the match:
// characters at word starts and in a row count for more, gaps for less, and a
// match inside the last path segment (the file or folder name) wins. It
// returns the byte offsets of the matched characters.
func fuzzyScore(hay, needle string) (int, []int, bool) {
	nr := []rune(needle)
	if len(nr) == 0 {
		return 0, nil, true
	}
	k := 0 // cheap reject before allocating anything
	for _, r := range hay {
		if r == nr[k] {
			if k++; k == len(nr) {
				break
			}
		}
	}
	if k < len(nr) {
		return 0, nil, false
	}
	var hr []rune
	var off []int
	for i, r := range hay {
		hr = append(hr, r)
		off = append(off, i)
	}
	// Leftmost end of a match, then walk back for the tightest start.
	end := 0
	for i, k := 0, 0; i < len(hr); i++ {
		if hr[i] == nr[k] {
			if k++; k == len(nr) {
				end = i
				break
			}
		}
	}
	pos := make([]int, len(nr))
	for i, k := end, len(nr)-1; i >= 0 && k >= 0; i-- {
		if hr[i] == nr[k] {
			pos[k] = i
			k--
		}
	}
	name := 0 // first rune of the last path segment
	for i := len(hr) - 2; i >= 0; i-- {
		if hr[i] == '/' {
			name = i + 1
			break
		}
	}
	score := 0
	for j, p := range pos {
		score += 16
		prev := rune(0)
		if p > 0 {
			prev = hr[p-1]
		}
		switch {
		case p == 0 || prev == '/':
			score += 12
		case prev == '_' || prev == '-' || prev == '.' || prev == ' ':
			score += 8
		case unicode.IsLower(prev) && unicode.IsUpper(hr[p]):
			score += 6
		}
		if j > 0 {
			if gap := p - pos[j-1] - 1; gap == 0 {
				score += 10
			} else {
				score -= min(gap, 8)
			}
		}
		if p >= name {
			score += 6
		}
	}
	if strings.Contains(string(hr[name:]), needle) {
		score += 20
	}
	bytePos := make([]int, len(pos))
	for j, p := range pos {
		bytePos[j] = off[p]
	}
	return score, bytePos, true
}

func (m model) handleFindKey(md *modal, msg tea.KeyMsg) (model, tea.Cmd) {
	f := md.find
	switch msg.String() {
	case "esc":
		f.cancel()
		m.modal = nil
	case "up", "ctrl+p":
		md.sel = max(0, md.sel-1)
	case "down", "ctrl+n", "tab":
		md.sel = min(max(0, len(f.results)-1), md.sel+1)
	case "pgup":
		md.sel = max(0, md.sel-10)
	case "pgdown":
		md.sel = min(max(0, len(f.results)-1), md.sel+10)
	case "enter":
		if md.sel >= len(f.results) {
			break
		}
		f.cancel()
		m.modal = nil
		rel := f.results[md.sel].path
		full := filepath.Join(f.root, rel)
		if strings.HasSuffix(rel, "/") {
			m.chdir(full, "") // folders open
		} else {
			m.chdir(filepath.Dir(full), filepath.Base(full)) // files are selected in their folder
		}
	default:
		if md.in.handle(msg) {
			f.rescore(md.in.value())
			md.sel = 0
		}
	}
	return m, nil
}

func (m model) findBody(md *modal, inner int) []string {
	f := md.find
	r := m.renderer
	body := []string{r.RenderSoftRow(tideui.SoftRow{Text: md.in.view(), Selected: true}, inner)}
	status := plural(len(f.paths), "item")
	switch {
	case !f.done:
		status += " so far, indexing…"
	case f.truncated:
		status += " (stopped there)"
	}
	if q := strings.TrimSpace(md.in.value()); q != "" {
		n := len(f.results)
		shown := plural(n, "match")
		if n == maxFindResults {
			shown = "best " + shown
		}
		status = shown + " · " + status
	}
	body = append(body, m.renderer.Styles.DetailMeta.Render(status), "")
	rows := max(3, m.height-14)
	first := max(0, min(md.sel-rows/2, len(f.results)-rows))
	for i := first; i < min(len(f.results), first+rows); i++ {
		body = append(body, r.RenderSoftRow(tideui.SoftRow{
			Text: highlightMatch(f.results[i], inner-2, m.renderer.Styles.Badge.Render), Selected: i == md.sel}, inner))
	}
	if strings.TrimSpace(md.in.value()) != "" && len(f.results) == 0 && f.done {
		body = append(body, "  no matches")
	}
	body = append(body, "", r.RenderSoftHints(inner,
		tideui.SoftHint{Key: "↑↓", Label: "choose"},
		tideui.SoftHint{Key: "enter", Label: "go to"},
		tideui.SoftHint{Key: "esc", Label: "close"}))
	return body
}

// highlightMatch renders a result with its matched characters emphasised,
// dropping leading characters (shown as "…") when it's wider than width.
func highlightMatch(mt findMatch, width int, hi func(...string) string) string {
	p := sanitize(mt.path)
	if p != mt.path {
		return ansi.Truncate(p, width, "…") // unusual bytes: skip highlighting
	}
	cut := 0
	if w := ansi.StringWidth(p); w > width && width > 1 {
		for cut < len(p) && ansi.StringWidth(p[cut:]) > width-1 {
			_, size := firstRune(p[cut:])
			cut += size
		}
	}
	matched := map[int]bool{}
	for _, i := range mt.pos {
		matched[i] = true
	}
	var b strings.Builder
	if cut > 0 {
		b.WriteString("…")
	}
	for i, r := range p[cut:] {
		if matched[cut+i] {
			b.WriteString(hi(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 1
}
