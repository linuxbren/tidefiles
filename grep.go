package main

// Content search: find text inside the files below the current folder. Uses
// ripgrep when it's installed and a built-in Go search otherwise; both search
// for the literal text (case-insensitive unless it has a capital), skip
// binary and very large files, respect .gitignore and the show-hidden
// setting, and never look inside .git.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const (
	grepMaxResults  = 1000
	grepMaxPerFile  = 100
	grepMaxFileSize = 5 << 20 // bytes; bigger files are skipped
	grepMinQuery    = 2
	grepDebounce    = 200 * time.Millisecond
)

type grepHit struct {
	path  string   // relative to the search root
	line  int      // 1-based
	text  string   // the matching line, without its newline
	spans [][2]int // byte ranges of the matches within text
}

type grepState struct {
	root    string
	hidden  bool
	engine  string // "ripgrep" or "built-in"
	pending int    // input generation the debounce tick must match
	run     int    // id of the running search
	query   string // what the results are for
	hits    []grepHit
	files   map[string]bool
	done    bool
	capped  bool
	err     string
	cancel  context.CancelFunc
	ch      chan grepBatchMsg
}

type grepBatchMsg struct {
	run    int
	hits   []grepHit
	done   bool
	capped bool
	err    string
}

type grepTickMsg struct{ pending int }

var grepRunSeq int

func (m *model) openGrep() {
	g := &grepState{root: m.cwd, hidden: m.cfg.ShowHidden, engine: "built-in", files: map[string]bool{}}
	if _, err := exec.LookPath("rg"); err == nil {
		g.engine = "ripgrep"
	}
	m.modal = &modal{kind: mGrep, title: "search inside files below " + tildePath(m.cwd), in: newInput(""), grep: g}
}

// stop cancels the running search, if any.
func (g *grepState) stop() {
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
}

func (g *grepState) start(query string) tea.Cmd {
	g.stop()
	g.hits, g.files, g.done, g.capped, g.err, g.query = nil, map[string]bool{}, false, false, "", query
	if len([]rune(query)) < grepMinQuery {
		g.done = true
		return nil
	}
	grepRunSeq++
	g.run = grepRunSeq
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	g.ch = make(chan grepBatchMsg, 4)
	if g.engine == "ripgrep" {
		go ripgrep(ctx, g.root, query, g.hidden, g.run, g.ch)
	} else {
		go grepFiles(ctx, g.root, query, g.hidden, g.run, g.ch)
	}
	return g.next()
}

func (g *grepState) next() tea.Cmd {
	ch := g.ch
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// grepSender batches hits and stops the search at the result cap.
type grepSender struct {
	ctx   context.Context
	run   int
	ch    chan<- grepBatchMsg
	batch []grepHit
	total int
	last  time.Time
}

// add queues a hit and reports whether the search should go on.
func (s *grepSender) add(h grepHit) bool {
	s.batch = append(s.batch, h)
	s.total++
	if s.total >= grepMaxResults {
		return false
	}
	if time.Since(s.last) >= findBatchEvery {
		return s.flush()
	}
	return true
}

func (s *grepSender) flush() bool {
	if len(s.batch) == 0 {
		return s.ctx.Err() == nil
	}
	select {
	case s.ch <- grepBatchMsg{run: s.run, hits: s.batch}:
		s.batch, s.last = nil, time.Now()
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *grepSender) finish(err string) {
	select {
	case s.ch <- grepBatchMsg{run: s.run, hits: s.batch, done: true, capped: s.total >= grepMaxResults, err: err}:
	case <-s.ctx.Done():
	}
}

// smartCase reports whether query should match case-sensitively.
func smartCase(query string) bool {
	for _, r := range query {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// ---- ripgrep -------------------------------------------------------------

type rgText struct {
	Text  *string `json:"text"`
	Bytes *string `json:"bytes"`
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       rgText `json:"path"`
		Lines      rgText `json:"lines"`
		LineNumber int    `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

func ripgrepArgs(query string, hidden bool) []string {
	args := []string{"--json", "--no-config", "--fixed-strings", "--smart-case",
		"--max-filesize", strconv.Itoa(grepMaxFileSize), "--max-count", strconv.Itoa(grepMaxPerFile),
		"--glob", "!.git"}
	if hidden {
		args = append(args, "--hidden")
	}
	return append(args, "--", query, ".")
}

func ripgrep(ctx context.Context, root, query string, hidden bool, run int, ch chan<- grepBatchMsg) {
	defer close(ch)
	s := &grepSender{ctx: ctx, run: run, ch: ch, last: time.Now()}
	cctx, kill := context.WithCancel(ctx)
	defer kill()
	cmd := exec.CommandContext(cctx, "rg", ripgrepArgs(query, hidden)...)
	cmd.Dir = root
	out, err := cmd.StdoutPipe()
	if err != nil {
		s.finish(err.Error())
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		s.finish(err.Error())
		return
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	more := true
	for more && sc.Scan() {
		if h, ok := parseRgMatch(sc.Bytes()); ok {
			more = s.add(h)
		}
	}
	kill() // stops rg early when we hit the cap
	_ = cmd.Wait()
	if ctx.Err() != nil {
		return
	}
	// rg exits 1 for "no matches" and 2 for unreadable files it skipped; only
	// report stderr when nothing came back at all.
	msg := ""
	if s.total == 0 && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 2 {
		msg = strings.TrimSpace(strings.SplitN(stderr.String(), "\n", 2)[0])
	}
	s.finish(msg)
}

func parseRgMatch(line []byte) (grepHit, bool) {
	var ev rgEvent
	if json.Unmarshal(line, &ev) != nil || ev.Type != "match" || ev.Data.Path.Text == nil || ev.Data.Lines.Text == nil {
		return grepHit{}, false // non-UTF-8 paths and lines come as base64 "bytes"; skip them
	}
	text := strings.TrimRight(*ev.Data.Lines.Text, "\r\n")
	h := grepHit{path: strings.TrimPrefix(*ev.Data.Path.Text, "./"), line: ev.Data.LineNumber, text: text}
	for _, sm := range ev.Data.Submatches {
		if sm.Start >= 0 && sm.End <= len(text) && sm.Start < sm.End {
			h.spans = append(h.spans, [2]int{sm.Start, sm.End})
		}
	}
	return h, true
}

// ---- built-in search -----------------------------------------------------

func grepFiles(ctx context.Context, root, query string, hidden bool, run int, ch chan<- grepBatchMsg) {
	defer close(ch)
	s := &grepSender{ctx: ctx, run: run, ch: ch, last: time.Now()}
	cctx, stop := context.WithCancel(ctx)
	defer stop()
	sensitive := smartCase(query)
	needle := query
	if !sensitive {
		needle = strings.ToLower(query)
	}
	// List files on one goroutine, search them on a pool, and collect here so
	// the result caps are applied in one place.
	paths := make(chan string, 256)
	results := make(chan []grepHit, 64)
	var wg sync.WaitGroup
	for range min(16, max(4, runtime.NumCPU())) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range paths {
				if hits := grepFile(filepath.Join(root, rel), rel, needle, sensitive); len(hits) > 0 {
					select {
					case results <- hits:
					case <-cctx.Done():
						return
					}
				}
			}
		}()
	}
	go func() {
		listFiles(cctx, root, hidden, func(rel string) bool {
			select {
			case paths <- rel:
				return true
			case <-cctx.Done():
				return false
			}
		})
		close(paths)
		wg.Wait()
		close(results)
	}()
	more := true
	for hits := range results {
		for _, h := range hits {
			if more = more && s.add(h); !more {
				stop() // capped or cancelled: let the workers wind down
			}
		}
	}
	if ctx.Err() == nil {
		s.finish("")
	}
}

// listFiles calls fn with each candidate file (relative to root) until fn
// returns false. Git work trees (the root itself, or repos found while
// walking) are listed by git, which applies .gitignore exactly; anything
// else is walked.
func listFiles(ctx context.Context, root string, hidden bool, fn func(string) bool) {
	skip := func(rel string) bool {
		for _, part := range strings.Split(rel, "/") {
			if part == ".git" || (!hidden && strings.HasPrefix(part, ".")) {
				return true
			}
		}
		return false
	}
	gitFiles := func(dir, prefix string) (ok, more bool) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
		if err != nil {
			return false, true
		}
		seen := map[string]bool{}
		for _, rel := range strings.Split(string(out), "\x00") {
			if rel == "" || strings.HasSuffix(rel, "/") || seen[rel] || skip(rel) {
				continue // "dir/" is an untracked nested repo; repeats come from merge conflicts
			}
			seen[rel] = true
			if ctx.Err() != nil || !fn(prefix+rel) {
				return true, false
			}
		}
		return true, true
	}
	if ok, _ := gitFiles(root, ""); ok {
		return
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if err != nil || skip(rel) {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				if ok, more := gitFiles(p, rel+"/"); ok {
					if !more {
						return filepath.SkipAll
					}
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type().IsRegular() && !fn(rel) {
			return filepath.SkipAll
		}
		return nil
	})
}

// grepFile returns the matching lines of one file, up to grepMaxPerFile.
// needle must be lower-case unless sensitive.
func grepFile(path, rel, needle string, sensitive bool) []grepHit {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > grepMaxFileSize {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64*1024)
	if head, _ := br.Peek(8 * 1024); bytes.IndexByte(head, 0) >= 0 {
		return nil // binary
	}
	var hits []grepHit
	for n := 1; ; n++ {
		line, err := br.ReadString('\n')
		if line == "" && err != nil {
			return hits
		}
		text := strings.TrimRight(line, "\r\n")
		if spans := findAll(text, needle, sensitive); len(spans) > 0 {
			if hits = append(hits, grepHit{path: rel, line: n, text: text, spans: spans}); len(hits) >= grepMaxPerFile {
				return hits
			}
		}
		if err != nil {
			return hits
		}
	}
}

// findAll returns the byte ranges of needle in text. Without sensitive,
// needle must already be lower-case and matching ignores case.
func findAll(text, needle string, sensitive bool) [][2]int {
	hay := text
	if !sensitive {
		hay = strings.ToLower(text)
		if len(hay) != len(text) {
			// Lower-casing changed byte lengths (rare non-ASCII): report the
			// match without positions rather than wrong ones.
			if strings.Contains(hay, needle) {
				return [][2]int{{0, 0}}
			}
			return nil
		}
	}
	var spans [][2]int
	for off := 0; ; {
		i := strings.Index(hay[off:], needle)
		if i < 0 {
			return spans
		}
		spans = append(spans, [2]int{off + i, off + i + len(needle)})
		off += i + len(needle)
	}
}

// ---- modal ---------------------------------------------------------------

func (m model) handleGrepBatch(msg grepBatchMsg) tea.Cmd {
	if m.modal == nil || m.modal.kind != mGrep || m.modal.grep.run != msg.run || m.modal.grep.cancel == nil {
		return nil // closed, or superseded by a newer query
	}
	g := m.modal.grep
	for _, h := range msg.hits {
		g.hits = append(g.hits, h)
		g.files[h.path] = true
	}
	g.done, g.capped, g.err = msg.done, msg.capped, msg.err
	if msg.done {
		g.cancel = nil
		return nil
	}
	return g.next()
}

func (m model) handleGrepTick(msg grepTickMsg) tea.Cmd {
	if m.modal == nil || m.modal.kind != mGrep || m.modal.grep.pending != msg.pending {
		return nil
	}
	m.modal.sel = 0
	return m.modal.grep.start(strings.TrimSpace(m.modal.in.value()))
}

func (m model) handleGrepKey(md *modal, msg tea.KeyMsg) (model, tea.Cmd) {
	g := md.grep
	switch msg.String() {
	case "esc":
		g.stop()
		m.modal = nil
	case "up", "ctrl+p":
		md.sel = max(0, md.sel-1)
	case "down", "ctrl+n", "tab":
		md.sel = min(max(0, len(g.hits)-1), md.sel+1)
	case "pgup":
		md.sel = max(0, md.sel-10)
	case "pgdown":
		md.sel = min(max(0, len(g.hits)-1), md.sel+10)
	case "enter":
		if md.sel >= len(g.hits) {
			break
		}
		g.stop()
		m.modal = nil
		h := g.hits[md.sel]
		full := filepath.Join(g.root, h.path)
		m.jump = jumpTarget{path: full, line: h.line}
		m.chdir(filepath.Dir(full), filepath.Base(full))
	default:
		if md.in.handle(msg) {
			g.pending++
			pending := g.pending
			return m, tea.Tick(grepDebounce, func(time.Time) tea.Msg { return grepTickMsg{pending} })
		}
	}
	return m, nil
}

func (m model) grepBody(md *modal, inner int) []string {
	g := md.grep
	r := m.renderer
	st := r.Styles
	body := []string{r.RenderSoftRow(tideui.SoftRow{Text: md.in.view(), Selected: true}, inner)}
	q := strings.TrimSpace(md.in.value())
	var status string
	switch {
	case len([]rune(q)) < grepMinQuery:
		status = "type at least 2 characters · " + g.engine
	case g.query != q || (!g.done && len(g.hits) == 0):
		status = "searching… · " + g.engine
	default:
		status = plural(len(g.hits), "match") + " in " + plural(len(g.files), "file")
		if g.capped {
			status = "first " + status
		}
		if !g.done {
			status += ", searching…"
		}
		status += " · " + g.engine
	}
	body = append(body, st.DetailMeta.Render(status), "")
	rows := max(3, m.height-14)
	first := max(0, min(md.sel-rows/2, len(g.hits)-rows))
	for i := first; i < min(len(g.hits), first+rows); i++ {
		body = append(body, r.RenderSoftRow(tideui.SoftRow{
			Text: grepRow(g.hits[i], inner-2, st.DetailMeta.Render, st.Badge.Render), Selected: i == md.sel}, inner))
	}
	switch {
	case g.err != "":
		body = append(body, "  "+sanitize(g.err))
	case g.done && g.query == q && len(g.hits) == 0 && len([]rune(q)) >= grepMinQuery:
		body = append(body, "  no matches")
	}
	body = append(body, "", r.RenderSoftHints(inner,
		tideui.SoftHint{Key: "↑↓", Label: "choose"},
		tideui.SoftHint{Key: "enter", Label: "open at line"},
		tideui.SoftHint{Key: "esc", Label: "close"}))
	return body
}

// grepRow renders "path:line  snippet": the location dimmed and cut from
// the left when long, the snippet shifted so the first match is visible.
func grepRow(h grepHit, width int, dim, hi func(...string) string) string {
	loc := sanitize(h.path) + ":" + strconv.Itoa(h.line)
	maxLoc := max(12, width*2/5)
	if w := ansi.StringWidth(loc); w > maxLoc {
		loc = "…" + ansi.TruncateLeft(loc, w-maxLoc+1, "")
	}
	room := width - ansi.StringWidth(loc) - 2
	if room < 8 {
		return dim(loc)
	}
	return dim(loc) + "  " + snippet(h, room, hi)
}

// snippet fits a hit's line into width cells, keeping the first match in
// view and emphasising every match.
func snippet(h grepHit, width int, hi func(...string) string) string {
	text := h.text
	spans := h.spans
	// Drop leading indentation, shifting the spans to match.
	trim := len(text) - len(strings.TrimLeft(text, " \t"))
	text = text[trim:]
	start := 0
	if len(spans) > 0 && spans[0][1] > spans[0][0] && spans[0][0] >= trim {
		if pre := spans[0][0] - trim; ansi.StringWidth(text[:pre]) > width/3 {
			// Start a little before the first match so it's on screen.
			start = pre
			for back := 0; start > 0 && back < width/4; back++ {
				_, size := lastRune(text[:start])
				start -= size
			}
		}
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	inSpan := func(i int) bool {
		for _, sp := range spans {
			if i+trim >= sp[0] && i+trim < sp[1] {
				return true
			}
		}
		return false
	}
	w := ansi.StringWidth(b.String())
	for i, r := range text[start:] {
		if r == '\t' {
			r = ' '
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			continue // never let file content emit control characters
		}
		cw := ansi.StringWidth(string(r))
		if w+cw > width {
			break
		}
		w += cw
		if inSpan(start + i) {
			b.WriteString(hi(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func lastRune(s string) (rune, int) {
	r := []rune(s)
	if len(r) == 0 {
		return 0, 0
	}
	return r[len(r)-1], len(string(r[len(r)-1]))
}
