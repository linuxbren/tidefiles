package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/linuxbren/tidefiles/internal/omarchy"
)

const (
	tickInterval = 2 * time.Second
	undoDepth    = 50
	doubleClick  = 400 * time.Millisecond
	wheelStep    = 3
)

var columnRatios = [3]float64{0.20, 0.32, 0.48}

type clipboard struct {
	paths []string
	cut   bool
}

type undoItem struct {
	desc string
	fn   func() error
}

type model struct {
	cwd     string
	cwdMod  time.Time
	all     []entry // sorted, hidden-filtered, before the name filter
	entries []entry // what is listed
	cursor  int
	offset  int
	filter  string

	parent       []entry
	parentCursor int

	cursors  map[string]string // dir -> last selected name, restored on re-entry
	selected map[string]bool   // names in cwd
	clip     clipboard
	undo     []undoItem

	hist []string
	hi   int

	cfg config

	modal      *modal
	picker     tideui.ThemePicker
	pickerOpen bool

	width, height int

	themeMode string
	theme     tideui.Theme
	sig       string
	renderer  tideui.Renderer

	pv       preview
	pvKey    string
	pvPath   string
	pvScroll int

	msg    string
	msgErr bool

	lastClick    time.Time
	lastClickRow int

	cwdFile string
}

type (
	tickMsg   struct{}
	statusMsg struct {
		text string
		err  bool
	}
	opDoneMsg struct{ res opResult }
	undoMsg   struct {
		desc string
		err  error
	}
	reloadMsg struct{}
)

func newModel(dir string, cfg config, cwdFile string) model {
	m := model{
		cwd:       dir,
		cfg:       cfg,
		cursors:   map[string]string{},
		selected:  map[string]bool{},
		themeMode: cfg.Theme,
		cwdFile:   cwdFile,
		hist:      []string{dir},
	}
	m.applyTheme()
	m.reload("")
	return m
}

func (m *model) applyTheme() {
	m.theme = resolveTheme(m.themeMode)
	m.setRenderer(m.theme)
	if m.themeMode == themeOmarchy {
		m.sig = omarchy.Signature()
	}
}

func (m *model) setRenderer(t tideui.Theme) {
	m.renderer = tideui.NewRenderer(t, tideui.StyleOptions{Density: tideui.Compact})
}

func (m model) Init() tea.Cmd {
	return tea.Batch(writeTerm(setTerminalBg(m.theme)), tick())
}

func tick() tea.Cmd { return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} }) }

func writeTerm(s string) tea.Cmd {
	return func() tea.Msg {
		if s != "" {
			fmt.Fprint(os.Stdout, s)
		}
		return nil
	}
}

// ---- geometry ----------------------------------------------------------

// columnWidths mirrors tideui's three-column split.
func columnWidths(width int) [3]int {
	if width <= 2 {
		var out [3]int
		for i := 0; i < width; i++ {
			out[i] = 1
		}
		return out
	}
	r := columnRatios
	total := r[0] + r[1] + r[2]
	avail := width - 3
	a := int(float64(avail) * r[0] / total)
	b := int(float64(avail) * r[1] / total)
	return [3]int{1 + a, 1 + b, 1 + avail - a - b}
}

// geometry returns each pane's inner width and the number of body rows.
func (m model) geometry() (inner [3]int, rows int) {
	for i, w := range columnWidths(m.width) {
		if w > 2 {
			w -= 2
		}
		inner[i] = max(1, w)
	}
	mainH := max(1, m.height-1)
	if mainH > 2 {
		mainH -= 2
	}
	return inner, max(1, mainH-1)
}

// ---- state -------------------------------------------------------------

func (m model) current() (entry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return entry{}, false
	}
	return m.entries[m.cursor], true
}

func (m model) curName() string { e, _ := m.current(); return e.name }

func (m model) inTrash() bool { return m.cwd == trashFilesDir() }

// reload re-reads cwd and its parent and rebuilds the listing, putting the
// cursor on focus (or the remembered selection when focus is empty).
func (m *model) reload(focus string) {
	ents, err := readDir(m.cwd, m.cfg.ShowHidden)
	if err != nil {
		m.setMsg("cannot read "+tildePath(m.cwd)+": "+errText(err), true)
	}
	if fi, serr := os.Stat(m.cwd); serr == nil {
		m.cwdMod = fi.ModTime()
	}
	m.all = ents
	sortEntries(m.all, m.cfg.Sort, m.cfg.SortDesc)
	for name := range m.selected {
		found := false
		for _, e := range m.all {
			if e.name == name {
				found = true
				break
			}
		}
		if !found {
			delete(m.selected, name)
		}
	}
	if focus == "" {
		focus = m.cursors[m.cwd]
	}
	m.applyView(focus)

	m.parent, m.parentCursor = nil, 0
	if pdir := filepath.Dir(m.cwd); pdir != m.cwd {
		m.parent, _ = readDir(pdir, m.cfg.ShowHidden)
		sortEntries(m.parent, m.cfg.Sort, m.cfg.SortDesc)
		base := filepath.Base(m.cwd)
		for i, e := range m.parent {
			if e.name == base {
				m.parentCursor = i
				break
			}
		}
	}
}

// applyView derives the visible list from all (name filter) and restores the
// cursor to focus, or the previously highlighted name.
func (m *model) applyView(focus string) {
	if focus == "" {
		focus = m.curName()
	}
	m.entries = m.entries[:0:0]
	needle := strings.ToLower(m.filter)
	for _, e := range m.all {
		if needle == "" || strings.Contains(strings.ToLower(e.name), needle) {
			m.entries = append(m.entries, e)
		}
	}
	m.cursor = max(0, min(m.cursor, len(m.entries)-1))
	for i, e := range m.entries {
		if e.name == focus {
			m.cursor = i
			break
		}
	}
	m.fixOffset()
	m.pvKey = ""
}

func (m *model) setMsg(text string, isErr bool) { m.msg, m.msgErr = text, isErr }

func (m *model) fixOffset() {
	_, rows := m.geometry()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	m.offset = max(0, min(m.offset, max(0, len(m.entries)-rows)))
}

func (m *model) move(delta int) {
	if len(m.entries) == 0 {
		return
	}
	m.cursor = max(0, min(len(m.entries)-1, m.cursor+delta))
	m.fixOffset()
}

// navigate changes directory. record adds the move to the back/forward history.
func (m *model) navigate(dir, focus string, record bool) {
	if _, err := os.ReadDir(dir); err != nil {
		m.setMsg("cannot open "+tildePath(dir)+": "+errText(err), true)
		return
	}
	if e, ok := m.current(); ok {
		m.cursors[m.cwd] = e.name
	}
	if record && dir != m.cwd {
		m.hist = append(m.hist[:m.hi+1], dir)
		m.hi = len(m.hist) - 1
	}
	m.cwd, m.offset, m.cursor, m.filter = dir, 0, 0, ""
	m.selected = map[string]bool{}
	m.entries = nil
	m.reload(focus)
}

func (m *model) chdir(dir, focus string) { m.navigate(dir, focus, true) }

func (m *model) refreshPreview() {
	e, ok := m.current()
	if !ok {
		m.pv, m.pvKey, m.pvPath = preview{title: "Preview", lines: []string{"  (nothing selected)"}}, "", ""
		return
	}
	inner, _ := m.geometry()
	key := fmt.Sprintf("%s|%s|%d|%t|%t|%d", m.cwd, e.name, inner[2], m.cfg.Wrap, m.cfg.ShowHidden, e.mod.UnixNano())
	if key == m.pvKey {
		return
	}
	path := filepath.Join(m.cwd, e.name)
	if path != m.pvPath {
		m.pvScroll = 0
	}
	m.pvKey, m.pvPath = key, path
	m.pv = buildPreview(m.cwd, e, inner[2], m.cfg.Wrap, m.cfg.ShowHidden)
	m.clampPreviewScroll()
}

func (m *model) clampPreviewScroll() {
	_, rows := m.geometry()
	m.pvScroll = max(0, min(m.pvScroll, len(m.pv.lines)-rows))
}

// run executes fn off the UI goroutine and reports its result as opDoneMsg.
func (m *model) run(fn func() opResult) tea.Cmd {
	m.setMsg("working…", false)
	return func() tea.Msg { return opDoneMsg{fn()} }
}

// ---- update ------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixOffset()
		m.pvKey = ""
	case statusMsg:
		m.setMsg(msg.text, msg.err)
	case reloadMsg:
		m.reload(m.curName())
	case opDoneMsg:
		m.finishOp(msg.res)
	case undoMsg:
		if msg.err != nil {
			m.setMsg("undo failed: "+msg.err.Error(), true)
		} else {
			m.setMsg("Undid: "+msg.desc, false)
		}
		m.reload(m.curName())
	case tickMsg:
		cmd = tick()
		if m.themeMode == themeOmarchy && omarchy.Signature() != m.sig {
			m.applyTheme()
			cmd = tea.Batch(cmd, writeTerm(setTerminalBg(m.theme)))
		}
		if fi, err := os.Stat(m.cwd); err == nil && !fi.ModTime().Equal(m.cwdMod) {
			m.reload(m.curName())
		}
	case tea.MouseMsg:
		m, cmd = m.handleMouse(msg)
	case tea.KeyMsg:
		switch {
		case m.pickerOpen:
			m, cmd = m.handlePickerKey(msg)
		case m.modal != nil:
			m, cmd = m.handleModalKey(msg)
		default:
			m, cmd = m.handleKey(msg)
		}
	}
	m.refreshPreview()
	return m, cmd
}

func (m *model) finishOp(res opResult) {
	switch {
	case res.err != nil:
		text := res.err.Error()
		if res.desc != "" {
			text = res.desc + ", then: " + text
		}
		m.setMsg(text, true)
	case res.desc != "":
		m.setMsg(res.desc, false)
	default:
		m.setMsg("", false)
	}
	if res.undo != nil {
		m.undo = append(m.undo, undoItem{res.desc, res.undo})
		if len(m.undo) > undoDepth {
			m.undo = m.undo[1:]
		}
	}
	if res.desc != "" || res.err != nil {
		m.selected = map[string]bool{}
	}
	m.reload(res.focus)
}

func (m model) handleKey(msg tea.KeyMsg) (model, tea.Cmd) {
	m.msg = ""
	_, rows := m.geometry()
	cur, hasCur := m.current()
	curPath := filepath.Join(m.cwd, cur.name)

	switch keyIndex[msg.String()] {
	case actQuit:
		return m, m.quit()
	case actHelp:
		m.modal = &modal{kind: mHelp, title: "keys"}
	case actUp:
		m.move(-1)
	case actDown:
		m.move(1)
	case actTop:
		m.move(-len(m.entries))
	case actBottom:
		m.move(len(m.entries))
	case actHalfDown:
		m.move(rows / 2)
	case actHalfUp:
		m.move(-rows / 2)
	case actPageDown:
		m.move(rows)
	case actPageUp:
		m.move(-rows)
	case actParent:
		if p := filepath.Dir(m.cwd); p != m.cwd {
			m.chdir(p, filepath.Base(m.cwd))
		}
	case actOpen:
		if !hasCur {
			break
		}
		if cur.isDir {
			m.chdir(curPath, "")
		} else {
			return m, openDefault(curPath)
		}
	case actBack:
		if m.hi > 0 {
			m.hi--
			m.navigate(m.hist[m.hi], "", false)
		}
	case actForward:
		if m.hi < len(m.hist)-1 {
			m.hi++
			m.navigate(m.hist[m.hi], "", false)
		}
	case actHome:
		if home, err := os.UserHomeDir(); err == nil {
			m.chdir(home, "")
		}
	case actGoto:
		m.openInput("goto", "go to", "Type a path (~ works)", tildePath(m.cwd)+"/", nil)
	case actPlaces:
		m.openPlaces()
	case actBookmark:
		m.addBookmark()
	case actSelect:
		if hasCur {
			if m.selected[cur.name] {
				delete(m.selected, cur.name)
			} else {
				m.selected[cur.name] = true
			}
			m.move(1)
		}
	case actSelectAll:
		for _, e := range m.entries {
			m.selected[e.name] = true
		}
	case actInvert:
		for _, e := range m.entries {
			if m.selected[e.name] {
				delete(m.selected, e.name)
			} else {
				m.selected[e.name] = true
			}
		}
	case actEscape:
		switch {
		case m.filter != "":
			m.filter = ""
			m.applyView("")
		case len(m.selected) > 0:
			m.selected = map[string]bool{}
		}
	case actCopy, actCut:
		paths := m.targetPaths()
		if len(paths) == 0 {
			break
		}
		cut := keyIndex[msg.String()] == actCut
		m.clip = clipboard{paths, cut}
		verb := "Copied"
		if cut {
			verb = "Cut"
		}
		m.setMsg(verb+" "+plural(len(paths), "item")+" — press v to paste", false)
		return m, func() tea.Msg { _ = clipWrite(cut, paths); return nil }
	case actPaste:
		dir, internal := m.cwd, m.clip
		return m, m.run(func() opResult {
			paths, cut, ok := clipRead()
			if !ok {
				paths, cut = internal.paths, internal.cut
			}
			if len(paths) == 0 {
				return opResult{desc: "Nothing to paste"}
			}
			res := pasteInto(dir, paths, cut)
			if res.desc == "" && res.err == nil {
				res.desc = "Nothing to paste"
			}
			return res
		})
	case actTrash:
		paths := m.targetPaths()
		if len(paths) == 0 {
			break
		}
		if m.inTrash() {
			m.openConfirm("purge", "delete permanently", []string{"Permanently delete " + describe(m.targets()) + "?", "This cannot be undone."}, paths)
			break
		}
		return m, m.run(func() opResult { return trashItems(paths) })
	case actDelete:
		if paths := m.targetPaths(); len(paths) > 0 {
			m.openConfirm("purge", "delete permanently", []string{"Permanently delete " + describe(m.targets()) + "?", "This cannot be undone."}, paths)
		}
	case actRename:
		if hasCur {
			m.openInput("rename", "rename", "New name for "+cur.name, cur.name, []string{curPath})
		}
	case actNewFile:
		m.openInput("newfile", "new file", "File name", "", nil)
	case actNewFolder:
		m.openInput("newfolder", "new folder", "Folder name", "", nil)
	case actUndo:
		if len(m.undo) == 0 {
			m.setMsg("nothing to undo", false)
			break
		}
		it := m.undo[len(m.undo)-1]
		m.undo = m.undo[:len(m.undo)-1]
		return m, func() tea.Msg { return undoMsg{it.desc, it.fn()} }
	case actRestore:
		if !m.inTrash() {
			m.setMsg("restore works inside the Trash (press b, then Trash)", false)
			break
		}
		var names []string
		for _, e := range m.targets() {
			names = append(names, e.name)
		}
		return m, m.run(func() opResult { return restoreItems(names) })
	case actEmptyTrash:
		m.openConfirm("emptytrash", "empty trash", []string{"Permanently delete everything in the trash?", "This cannot be undone."}, nil)
	case actProps:
		m.openProps()
	case actOpenWith:
		m.openWith()
	case actTerminal:
		return m, terminalHere(m.cwd)
	case actEdit:
		if hasCur && !cur.isDir {
			return m, editFile(curPath)
		}
	case actCopyPath:
		paths := m.targetPaths()
		if len(paths) > 0 {
			text := strings.Join(paths, "\n")
			m.setMsg("Copied path", false)
			return m, func() tea.Msg { _ = textWrite(text); return nil }
		}
	case actHidden:
		m.cfg.ShowHidden = !m.cfg.ShowHidden
		m.cfg.save()
		m.reload(m.curName())
		m.setMsg(map[bool]string{true: "showing hidden files", false: "hiding hidden files"}[m.cfg.ShowHidden], false)
	case actWrap:
		m.cfg.Wrap = !m.cfg.Wrap
		m.cfg.save()
		m.setMsg(map[bool]string{true: "preview wrap on", false: "preview wrap off"}[m.cfg.Wrap], false)
	case actSort:
		keys := []string{"name", "size", "modified", "type"}
		for i, k := range keys {
			if k == m.cfg.Sort {
				m.cfg.Sort = keys[(i+1)%len(keys)]
				break
			}
		}
		m.cfg.save()
		m.reload(m.curName())
		m.setMsg("sorted by "+m.cfg.Sort, false)
	case actSortRev:
		m.cfg.SortDesc = !m.cfg.SortDesc
		m.cfg.save()
		m.reload(m.curName())
	case actFilter:
		m.openInput("filter", "filter", "Show only names containing…", m.filter, nil)
	case actRefresh:
		m.reload(m.curName())
		m.setMsg("refreshed", false)
	case actTheme:
		m.openPicker()
	case actScrollDown:
		m.pvScroll++
		m.clampPreviewScroll()
	case actScrollUp:
		m.pvScroll = max(0, m.pvScroll-1)
	}
	return m, nil
}

func describe(ts []entry) string {
	if len(ts) == 1 {
		return "“" + ts[0].name + "”"
	}
	return plural(len(ts), "item")
}

// ---- theme picker ------------------------------------------------------

func (m *model) openPicker() {
	themes := append(append([]tideui.Theme(nil), tideui.BuiltinThemes...), resolveTheme(themeOmarchy))
	if themes[len(themes)-1].Name != "match-omarchy" {
		themes = themes[:len(themes)-1]
	}
	m.picker = tideui.NewThemePicker(tideui.ThemePickerOptions{Themes: themes, Title: "THEME"})
	current := m.themeMode
	if current == themeOmarchy {
		current = "match-omarchy"
	}
	m.picker.Open(current)
	m.pickerOpen = true
}

func (m model) handlePickerKey(msg tea.KeyMsg) (model, tea.Cmd) {
	switch m.picker.Update(msg) {
	case tideui.ThemePickerConfirm:
		m.pickerOpen = false
		name := m.picker.ConfirmedTheme().Name
		if name == "match-omarchy" {
			name = themeOmarchy
		}
		m.themeMode, m.cfg.Theme = name, name
		m.cfg.save()
		m.applyTheme()
		return m, writeTerm(setTerminalBg(m.theme))
	case tideui.ThemePickerCancel:
		m.pickerOpen = false
		m.applyTheme()
		return m, writeTerm(setTerminalBg(m.theme))
	}
	m.setRenderer(m.picker.PreviewTheme())
	return m, writeTerm(setTerminalBg(m.picker.PreviewTheme()))
}

// ---- mouse -------------------------------------------------------------

func (m model) handleMouse(msg tea.MouseMsg) (model, tea.Cmd) {
	if m.modal != nil || m.pickerOpen {
		return m, nil
	}
	w := columnWidths(m.width)
	_, rows := m.geometry()
	row := msg.Y - 2 // border + header
	inRows := row >= 0 && row < rows
	col := 0
	switch {
	case msg.X >= w[0]+w[1]:
		col = 2
	case msg.X >= w[0]:
		col = 1
	}
	switch {
	case msg.Button == tea.MouseButtonWheelDown && msg.Action == tea.MouseActionPress:
		if col == 2 {
			m.pvScroll += wheelStep
			m.clampPreviewScroll()
		} else {
			m.move(wheelStep)
		}
	case msg.Button == tea.MouseButtonWheelUp && msg.Action == tea.MouseActionPress:
		if col == 2 {
			m.pvScroll = max(0, m.pvScroll-wheelStep)
		} else {
			m.move(-wheelStep)
		}
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && inRows:
		switch col {
		case 0:
			if i := row + max(0, min(m.parentCursor-rows/2, len(m.parent)-rows)); i < len(m.parent) {
				m.chdir(filepath.Join(filepath.Dir(m.cwd), m.parent[i].name), "")
			}
		case 1:
			i := row + m.offset
			if i >= len(m.entries) {
				break
			}
			double := i == m.lastClickRow && time.Since(m.lastClick) < doubleClick
			m.lastClick, m.lastClickRow = time.Now(), i
			m.cursor = i
			m.fixOffset()
			if double {
				e := m.entries[i]
				p := filepath.Join(m.cwd, e.name)
				if e.isDir {
					m.chdir(p, "")
				} else {
					return m, openDefault(p)
				}
			}
		}
	}
	return m, nil
}

// ---- misc --------------------------------------------------------------

func (m model) quit() tea.Cmd {
	if m.cwdFile != "" {
		_ = os.WriteFile(m.cwdFile, []byte(m.cwd), 0o600)
	}
	return tea.Sequence(writeTerm(resetTerminalBg(m.theme)), tea.Quit)
}

func editFile(path string) tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "nvim"
	}
	parts := strings.Fields(editor)
	c := exec.Command(parts[0], append(parts[1:], path)...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return statusMsg{"editor: " + err.Error(), true}
		}
		return reloadMsg{}
	})
}
