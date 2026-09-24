package main

import (
	"fmt"
	"strings"

	"github.com/allisonhere/tideui"
)

// statusHints is the always-visible shortcut strip. "? all keys" leads so it
// survives truncation on narrow terminals.
const statusHints = "? all keys  ↑↓ move  ← back  →/enter open  e edit  . hidden  w wrap  q quit"

type helpGroup struct {
	title string
	keys  [][2]string
}

var helpGroups = []helpGroup{
	{"Move", [][2]string{
		{"↑ / ↓", "up / down  (also k / j)"},
		{"←  backspace", "parent directory  (also h)"},
		{"→  enter", "open directory or file  (also l)"},
		{"g / G", "top / bottom"},
		{"ctrl+d / ctrl+u", "half page down / up"},
		{"pgdn / pgup", "full page"},
		{"~", "home directory"},
	}},
	{"Files", [][2]string{
		{"enter / l", "open with default app"},
		{"e", "edit in $EDITOR"},
		{"r", "refresh"},
	}},
	{"View", [][2]string{
		{".", "toggle hidden files"},
		{"w", "toggle preview word wrap"},
		{"J / K", "scroll preview"},
	}},
	{"App", [][2]string{
		{"?", "toggle this help"},
		{"q  ctrl+c", "quit"},
	}},
}

func (m model) helpContent() string {
	var b strings.Builder
	for i, g := range helpGroups {
		if i > 0 && m.height >= 30 {
			b.WriteString("\n")
		}
		b.WriteString(m.renderer.Styles.DetailMeta.Render(g.title) + "\n")
		for _, k := range g.keys {
			fmt.Fprintf(&b, "  %-18s %s\n", k[0], k[1])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	inner, rows := m.geometry()

	parentTitle := ""
	if len(m.parent) > 0 || m.cwd != "/" {
		parentTitle = tildePathBase(m.cwd)
	}
	hint := ""
	if len(m.entries) > 0 {
		hint = fmt.Sprintf("%d/%d", m.cursor+1, len(m.entries))
	}
	pvTitle := m.pv.title
	if pvTitle == "" {
		pvTitle = "Preview"
	}

	layout := tideui.Layout{
		Width: m.width, Height: m.height, Mode: tideui.ThreeColumn,
		ColumnRatios: columnRatios,
		Panes: [3]tideui.Pane{
			{Title: parentTitle, Content: m.renderList(m.parent, m.parentCursor, m.parentOffset(rows), inner[0], rows, false)},
			{Title: tildePath(m.cwd), Hint: hint, Focused: true,
				Content: m.renderList(m.entries, m.cursor, m.offset, inner[1], rows, true)},
			{Title: pvTitle, Hint: m.pv.meta, Content: strings.Join(m.pv.lines, "\n"), ScrollOffset: m.pvScroll},
		},
		Status: &tideui.StatusBar{Left: m.statusLeft(), Right: statusHints},
	}
	if m.help {
		layout.Modal = &tideui.Overlay{
			Visible: true, Title: "Keybindings", Content: m.helpContent(),
			Footer: "? or esc to close", Width: 58,
		}
	}
	return m.renderer.Render(layout)
}

func (m model) parentOffset(rows int) int {
	return max(0, min(m.parentCursor-rows/2, len(m.parent)-rows))
}

func (m model) statusLeft() string {
	if m.msg != "" {
		if m.msgErr {
			return m.renderer.Styles.StatusError.Render(m.msg)
		}
		return m.msg
	}
	e, ok := m.selected()
	if !ok {
		return ""
	}
	if e.isDir {
		return fmt.Sprintf("%s  %s", e.mode, e.mod.Format("2006-01-02 15:04"))
	}
	return fmt.Sprintf("%s  %s  %s", e.mode, humanSize(e.size), e.mod.Format("2006-01-02 15:04"))
}

func (m model) renderList(ents []entry, cursor, offset, width, rows int, sizes bool) string {
	if len(ents) == 0 {
		return "  (empty)"
	}
	end := min(len(ents), offset+rows)
	lines := make([]string, 0, end-offset)
	for i := offset; i < end; i++ {
		e := ents[i]
		name := e.name
		if e.isDir {
			name += "/"
		}
		row := tideui.Row{Prefix: " ", Text: sanitize(name), Selected: i == cursor, Muted: e.hidden()}
		if sizes && !e.isDir && width >= 28 {
			row.Suffix = humanSize(e.size) + " "
		}
		lines = append(lines, m.renderer.RenderRow(row, width))
	}
	return strings.Join(lines, "\n")
}

func tildePathBase(cwd string) string {
	parent := cwd
	if i := strings.LastIndex(cwd, "/"); i > 0 {
		parent = cwd[:i]
	} else {
		parent = "/"
	}
	return tildePath(parent)
}
