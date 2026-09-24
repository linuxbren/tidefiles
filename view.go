package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/allisonhere/tideui"
)

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	inner, rows := m.geometry()

	parentTitle := tildePath(filepath.Dir(m.cwd))
	hint := fmt.Sprintf("%d/%d", min(m.cursor+1, len(m.entries)), len(m.entries))
	if m.cfg.Sort != "name" || m.cfg.SortDesc {
		arrow := "↑"
		if m.cfg.SortDesc {
			arrow = "↓"
		}
		hint = m.cfg.Sort + arrow + "  " + hint
	}
	if m.filter != "" {
		hint = "/" + m.filter + "  " + hint
	}
	title := tildePath(m.cwd)
	if m.inTrash() {
		title = "Trash"
	}
	pvTitle := m.pv.title
	if pvTitle == "" {
		pvTitle = "Preview"
	}

	layout := tideui.Layout{
		Width: m.width, Height: m.height, Mode: tideui.ThreeColumn,
		ColumnRatios: columnRatios,
		Panes: [3]tideui.Pane{
			{Title: parentTitle, Content: m.renderList(m.parent, m.parentCursor, max(0, min(m.parentCursor-rows/2, len(m.parent)-rows)), inner[0], rows, false)},
			{Title: title, Hint: hint, Focused: true, Content: m.renderList(m.entries, m.cursor, m.offset, inner[1], rows, true)},
			{Title: pvTitle, Hint: m.pv.meta, Content: strings.Join(m.pv.lines, "\n"), ScrollOffset: m.pvScroll},
		},
		Status: &tideui.StatusBar{Left: m.statusLeft(), Right: statusHints},
	}
	switch {
	case m.pickerOpen:
		h := max(6, m.height-4)
		ov := m.picker.SoftModal(m.renderer, min(40, m.width-4), h, "tidefiles")
		layout.Modal = &ov
	case m.modal != nil:
		ov := m.modalOverlay()
		layout.Modal = &ov
	}
	return m.renderer.Render(layout)
}

func (m model) statusLeft() string {
	if m.msg != "" {
		if m.msgErr {
			return m.renderer.Styles.StatusError.Render(m.msg)
		}
		return m.msg
	}
	if n := len(m.selected); n > 0 {
		var total int64
		for _, e := range m.all {
			if m.selected[e.name] && !e.isDir {
				total += e.size
			}
		}
		return fmt.Sprintf("%d selected  %s", n, humanSize(total))
	}
	e, ok := m.current()
	if !ok {
		if m.filter != "" {
			return "no matches — esc clears the filter"
		}
		return ""
	}
	if e.isDir {
		return fmt.Sprintf("%s  %s", e.mode, e.mod.Format("2006-01-02 15:04"))
	}
	return fmt.Sprintf("%s  %s  %s", e.mode, humanSize(e.size), e.mod.Format("2006-01-02 15:04"))
}

func (m model) renderList(ents []entry, cursor, offset, width, rows int, main bool) string {
	if len(ents) == 0 {
		if main && m.filter != "" {
			return "  (no matches)"
		}
		return "  (empty)"
	}
	cutSet := map[string]bool{}
	if main && m.clip.cut {
		for _, p := range m.clip.paths {
			if filepath.Dir(p) == m.cwd {
				cutSet[filepath.Base(p)] = true
			}
		}
	}
	end := min(len(ents), offset+rows)
	lines := make([]string, 0, end-offset)
	for i := offset; i < end; i++ {
		e := ents[i]
		name := e.name
		if e.isDir {
			name += "/"
		}
		prefix := " "
		if main && m.selected[e.name] {
			prefix = "✓"
		}
		row := tideui.Row{Prefix: prefix + " ", Text: sanitize(name), Selected: i == cursor, Muted: e.hidden() || cutSet[e.name]}
		if main && !e.isDir && width >= 28 {
			row.Suffix = humanSize(e.size) + " "
		}
		lines = append(lines, m.renderer.RenderRow(row, width))
	}
	return strings.Join(lines, "\n")
}
