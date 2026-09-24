package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/linuxbren/tidefiles/internal/omarchy"
)

const themePollInterval = 2 * time.Second

var columnRatios = [3]float64{0.20, 0.32, 0.48}

type model struct {
	cwd     string
	entries []entry
	cursor  int
	offset  int

	parent       []entry
	parentCursor int

	cursors map[string]string // dir -> last selected name, restored on re-entry

	showHidden bool
	wrap       bool
	help       bool

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

	cwdFile string
}

type (
	themeTickMsg struct{}
	statusMsg    struct {
		text string
		err  bool
	}
	reloadMsg struct{}
)

func newModel(dir, themeMode, cwdFile string) model {
	m := model{
		cwd:       dir,
		cursors:   map[string]string{},
		wrap:      true,
		themeMode: themeMode,
		cwdFile:   cwdFile,
	}
	m.applyTheme()
	m.reload("")
	return m
}

func (m *model) applyTheme() {
	m.theme = resolveTheme(m.themeMode)
	m.renderer = tideui.NewRenderer(m.theme, tideui.StyleOptions{Density: tideui.Compact})
	if m.themeMode == themeOmarchy {
		m.sig = omarchy.Signature()
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{writeTerm(setTerminalBg(m.theme))}
	if m.themeMode == themeOmarchy {
		cmds = append(cmds, tickTheme())
	}
	return tea.Batch(cmds...)
}

func tickTheme() tea.Cmd {
	return tea.Tick(themePollInterval, func(time.Time) tea.Msg { return themeTickMsg{} })
}

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

func (m model) selected() (entry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return entry{}, false
	}
	return m.entries[m.cursor], true
}

// reload re-reads cwd and its parent, placing the cursor on focus (or the
// remembered selection when focus is empty).
func (m *model) reload(focus string) {
	ents, err := readDir(m.cwd, m.showHidden)
	if err != nil {
		m.setMsg("cannot read "+tildePath(m.cwd)+": "+errText(err), true)
	}
	m.entries = ents
	if focus == "" {
		focus = m.cursors[m.cwd]
	}
	m.cursor = 0
	for i, e := range ents {
		if e.name == focus {
			m.cursor = i
			break
		}
	}

	m.parent, m.parentCursor = nil, 0
	if pdir := filepath.Dir(m.cwd); pdir != m.cwd {
		m.parent, _ = readDir(pdir, m.showHidden)
		base := filepath.Base(m.cwd)
		for i, e := range m.parent {
			if e.name == base {
				m.parentCursor = i
				break
			}
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

func (m *model) remember() {
	if e, ok := m.selected(); ok {
		m.cursors[m.cwd] = e.name
	}
}

func (m *model) chdir(dir, focus string) {
	if _, err := os.ReadDir(dir); err != nil {
		m.setMsg("cannot open "+tildePath(dir)+": "+errText(err), true)
		return
	}
	m.remember()
	m.cwd = dir
	m.offset = 0
	m.reload(focus)
}

func (m *model) refreshPreview() {
	e, ok := m.selected()
	if !ok {
		m.pv, m.pvKey, m.pvPath = preview{title: "Preview", lines: []string{"  (nothing selected)"}}, "", ""
		return
	}
	inner, _ := m.geometry()
	key := fmt.Sprintf("%s|%s|%d|%t|%t|%d", m.cwd, e.name, inner[2], m.wrap, m.showHidden, e.mod.UnixNano())
	if key == m.pvKey {
		return
	}
	path := filepath.Join(m.cwd, e.name)
	if path != m.pvPath {
		m.pvScroll = 0
	}
	m.pvKey, m.pvPath = key, path
	m.pv = buildPreview(m.cwd, e, inner[2], m.wrap, m.showHidden)
	m.clampPreviewScroll()
}

func (m *model) clampPreviewScroll() {
	_, rows := m.geometry()
	m.pvScroll = max(0, min(m.pvScroll, len(m.pv.lines)-rows))
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
		m.reload(func() string { e, _ := m.selected(); return e.name }())
	case themeTickMsg:
		cmd = tickTheme()
		if sig := omarchy.Signature(); sig != m.sig {
			m.applyTheme()
			cmd = tea.Batch(cmd, writeTerm(setTerminalBg(m.theme)))
		}
	case tea.KeyMsg:
		m, cmd = m.handleKey(msg)
	}
	m.refreshPreview()
	return m, cmd
}

func (m model) handleKey(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	if m.help {
		if key == "?" || key == "esc" || key == "q" || key == "enter" {
			m.help = false
		}
		return m, nil
	}
	m.msg = ""
	_, rows := m.geometry()
	switch key {
	case "ctrl+c", "q":
		return m, m.quit()
	case "?":
		m.help = true
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.move(-len(m.entries))
	case "G", "end":
		m.move(len(m.entries))
	case "ctrl+d":
		m.move(rows / 2)
	case "ctrl+u":
		m.move(-rows / 2)
	case "pgdown", "ctrl+f":
		m.move(rows)
	case "pgup", "ctrl+b":
		m.move(-rows)
	case "h", "left", "backspace":
		if p := filepath.Dir(m.cwd); p != m.cwd {
			m.chdir(p, filepath.Base(m.cwd))
		}
	case "l", "right", "enter":
		if e, ok := m.selected(); ok {
			path := filepath.Join(m.cwd, e.name)
			if e.isDir {
				m.chdir(path, "")
			} else {
				return m, openFile(path)
			}
		}
	case "~":
		if home, err := os.UserHomeDir(); err == nil {
			m.chdir(home, "")
		}
	case ".":
		e, _ := m.selected()
		m.showHidden = !m.showHidden
		m.remember()
		m.reload(e.name)
		m.setMsg(map[bool]string{true: "showing hidden files", false: "hiding hidden files"}[m.showHidden], false)
	case "w":
		m.wrap = !m.wrap
		m.setMsg(map[bool]string{true: "preview wrap on", false: "preview wrap off"}[m.wrap], false)
	case "J":
		m.pvScroll++
		m.clampPreviewScroll()
	case "K":
		m.pvScroll = max(0, m.pvScroll-1)
	case "r":
		m.reload(func() string { e, _ := m.selected(); return e.name }())
		m.setMsg("refreshed", false)
	case "e":
		if e, ok := m.selected(); ok && !e.isDir {
			return m, editFile(filepath.Join(m.cwd, e.name))
		}
	}
	return m, nil
}

func (m model) quit() tea.Cmd {
	if m.cwdFile != "" {
		_ = os.WriteFile(m.cwdFile, []byte(m.cwd), 0o600)
	}
	return tea.Sequence(writeTerm(resetTerminalBg(m.theme)), tea.Quit)
}

func openFile(path string) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command("xdg-open", path)
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err != nil {
			return statusMsg{"open failed: " + err.Error(), true}
		}
		go func() { _ = c.Wait() }()
		return statusMsg{"opened " + filepath.Base(path), false}
	}
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
