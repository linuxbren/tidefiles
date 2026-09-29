package main

// Permissions editor (chmod): a 3×3 grid of owner/group/others × read/write/
// execute bits, plus octal entry. With several targets only the bits the user
// changes are applied, so each file keeps the rest of its own mode.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
)

var (
	permWho  = []string{"owner", "group", "others"}
	permWhat = []string{"read", "write", "execute"}
)

const (
	permBits    = os.FileMode(0o777)
	specialBits = os.ModeSetuid | os.ModeSetgid | os.ModeSticky
)

// permBit is the mode bit for a grid cell.
func permBit(row, col int) os.FileMode { return 1 << (8 - (row*3 + col)) }

type permTarget struct {
	path string
	mode os.FileMode // permission and special bits only
}

type permEdit struct {
	targets []permTarget
	mode    os.FileMode // what the grid shows
	touched os.FileMode // bits the user set explicitly
	mixed   os.FileMode // bits that differ between targets
	row     int
	col     int
	octal   string // digits typed so far
	anyDir  bool
}

func (m *model) openPerms() {
	var p permEdit
	for _, path := range m.targetPaths() {
		fi, err := os.Stat(path) // chmod follows symlinks, so show the target's mode
		if err != nil {
			continue
		}
		mode := fi.Mode() & (permBits | specialBits)
		if len(p.targets) == 0 {
			p.mode = mode
		}
		p.mixed |= mode ^ p.mode
		p.anyDir = p.anyDir || fi.IsDir()
		p.targets = append(p.targets, permTarget{path, mode})
	}
	if len(p.targets) == 0 {
		m.setMsg("nothing to change permissions on", true)
		return
	}
	m.modal = &modal{kind: mPerms, title: "permissions", perm: &p}
}

// set changes grid bits from explicit user input.
func (p *permEdit) set(bits, value os.FileMode) {
	p.mode = p.mode&^bits | value&bits
	p.touched |= bits
}

// toggle flips a bit; a bit that differs between targets turns on first.
func (p *permEdit) toggle(bit os.FileMode) {
	p.octal = ""
	if p.mixed&bit != 0 && p.touched&bit == 0 {
		p.set(bit, bit)
		return
	}
	p.set(bit, ^p.mode)
}

// typeDigit extends the octal entry; three digits set rwx for everyone, a
// fourth (leading) digit also sets setuid/setgid/sticky.
func (p *permEdit) typeDigit(d rune) {
	if len(p.octal) >= 4 {
		p.octal = ""
	}
	p.octal += string(d)
	p.applyOctal()
}

func (p *permEdit) backspace() {
	if p.octal != "" {
		p.octal = p.octal[:len(p.octal)-1]
		p.applyOctal()
	}
}

func (p *permEdit) applyOctal() {
	if len(p.octal) < 3 {
		return
	}
	mode, err := parseOctalMode(p.octal)
	if err != nil {
		return
	}
	bits := permBits
	if len(p.octal) == 4 {
		bits |= specialBits
	}
	p.set(bits, mode)
}

func parseOctalMode(s string) (os.FileMode, error) {
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil || n > 0o7777 {
		return 0, errors.New("not an octal mode")
	}
	mode := os.FileMode(n) & permBits
	if n&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if n&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if n&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode, nil
}

func octalOf(mode os.FileMode) string {
	n := uint32(mode & permBits)
	if mode&os.ModeSetuid != 0 {
		n |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		n |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		n |= 0o1000
	}
	if n > 0o777 {
		return fmt.Sprintf("%04o", n)
	}
	return fmt.Sprintf("%03o", n)
}

// symbolicMode renders ls-style rwx triplets, with s/S/t/T for special bits.
func symbolicMode(mode os.FileMode) string {
	b := []byte("rwxrwxrwx")
	for i := range b {
		if mode&(1<<(8-i)) == 0 {
			b[i] = '-'
		}
	}
	special := func(i int, on bool, set, unset byte) {
		if on {
			b[i] = map[bool]byte{true: set, false: unset}[b[i] == 'x']
		}
	}
	special(2, mode&os.ModeSetuid != 0, 's', 'S')
	special(5, mode&os.ModeSetgid != 0, 's', 'S')
	special(8, mode&os.ModeSticky != 0, 't', 'T')
	return string(b)
}

// result is the mode a target ends up with.
func (p *permEdit) result(t permTarget) os.FileMode { return t.mode&^p.touched | p.mode&p.touched }

func (m model) handlePermsKey(md *modal, key string) (model, tea.Cmd) {
	p := md.perm
	switch key {
	case "esc", "q":
		m.modal = nil
	case "up", "k":
		p.row = max(0, p.row-1)
	case "down", "j":
		p.row = min(2, p.row+1)
	case "left", "h":
		p.col = max(0, p.col-1)
	case "right", "l", "tab":
		p.col = min(2, p.col+1)
	case " ":
		p.toggle(permBit(p.row, p.col))
	case "r", "w", "x":
		p.col = strings.Index("rwx", key)
		p.toggle(permBit(p.row, p.col))
	case "backspace":
		p.backspace()
	case "enter":
		m.modal = nil
		if p.touched == 0 {
			m.setMsg("permissions unchanged", false)
			return m, nil
		}
		ts, edit, focus := p.targets, *p, m.curName()
		return m, m.run(func() opResult {
			res := chmodOp(ts, edit.result)
			res.focus = focus
			return res
		})
	default:
		// Fast typing or a paste arrives as one multi-rune key.
		if strings.Trim(key, "01234567") == "" {
			for _, d := range key {
				p.typeDigit(d)
			}
		}
	}
	return m, nil
}

// chmodOp applies each target's new mode; undo restores the old ones.
func chmodOp(ts []permTarget, result func(permTarget) os.FileMode) opResult {
	var changed []permTarget
	var failed []string
	var firstErr error
	var last os.FileMode
	for _, t := range ts {
		mode := result(t)
		last = mode
		if mode == t.mode {
			continue
		}
		if err := os.Chmod(t.path, mode); err != nil {
			failed = append(failed, filepath.Base(t.path))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		changed = append(changed, t)
	}
	res := opResult{}
	switch {
	case len(changed) == 0 && firstErr == nil:
		res.desc = "permissions unchanged"
	case len(ts) == 1 && len(changed) == 1:
		res.desc = fmt.Sprintf("Permissions of %s set to %s (%s)", filepath.Base(ts[0].path), symbolicMode(last), octalOf(last))
	case len(changed) > 0:
		res.desc = "Changed permissions of " + plural(len(changed), "item")
	}
	if firstErr != nil {
		var pe *os.PathError
		if errors.As(firstErr, &pe) {
			firstErr = pe.Err
		}
		res.err = fmt.Errorf("couldn't change %s: %w", strings.Join(failed, ", "), firstErr)
	}
	if len(changed) > 0 {
		res.undo = func() error {
			var errs []error
			for _, t := range changed {
				errs = append(errs, os.Chmod(t.path, t.mode))
			}
			return errors.Join(errs...)
		}
	}
	return res
}

// ---- rendering ---------------------------------------------------------

func (m model) permsBody(p *permEdit, inner int) []string {
	st := m.renderer.Styles
	var body []string
	if len(p.targets) == 1 {
		body = append(body, filepath.Base(p.targets[0].path))
	} else {
		note := ""
		if p.mixed != 0 {
			note = " — modes differ; only the bits you change apply"
		}
		body = append(body, plural(len(p.targets), "item")+note)
	}
	body = append(body, "")
	header := fmt.Sprintf("%-9s", "")
	for _, w := range permWhat {
		header += fmt.Sprintf("%-10s", w)
	}
	body = append(body, st.DetailMeta.Render(strings.TrimRight(header, " ")))
	for r, who := range permWho {
		line := fmt.Sprintf("%-9s", who)
		for c := range permWhat {
			bit := permBit(r, c)
			mark := "[ ]"
			switch {
			case p.mixed&bit != 0 && p.touched&bit == 0:
				mark = "[~]"
			case p.mode&bit != 0:
				mark = "[✓]"
			}
			if r == p.row && c == p.col {
				mark = st.SearchMatch.Render(mark)
			}
			line += "  " + mark + "     "
		}
		body = append(body, strings.TrimRight(line, " "))
	}
	body = append(body, "")
	summary := symbolicMode(p.mode) + "   " + octalOf(p.mode)
	if p.mixed&^p.touched != 0 {
		summary = "mixed (~ = left as each file has it)"
	}
	if p.octal != "" {
		summary += "   octal " + p.octal + "▏"
	}
	body = append(body, summary)
	if p.anyDir {
		body = append(body, st.DetailMeta.Render("execute on a folder = allowed to open it"))
	}
	r := m.renderer
	body = append(body, "",
		r.RenderSoftHints(inner,
			tideui.SoftHint{Key: "←↑↓→", Label: "move"},
			tideui.SoftHint{Key: "space", Label: "toggle"},
			tideui.SoftHint{Key: "r w x", Label: "toggle in row"}),
		r.RenderSoftHints(inner,
			tideui.SoftHint{Key: "0-7", Label: "type octal (755)"},
			tideui.SoftHint{Key: "enter", Label: "apply"},
			tideui.SoftHint{Key: "esc", Label: "cancel"}))
	return body
}
