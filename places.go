package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type place struct {
	name, path string
	bookmark   bool
}

func (m model) places() []place {
	var out []place
	seen := map[string]bool{}
	add := func(name, path string, bookmark bool) {
		if path == "" || seen[path] {
			return
		}
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			return
		}
		seen[path] = true
		out = append(out, place{name, path, bookmark})
	}
	home, _ := os.UserHomeDir()
	add("Home", home, false)
	for _, d := range []string{"Desktop", "Documents", "Downloads", "Music", "Pictures", "Videos"} {
		add(d, filepath.Join(home, d), false)
	}
	if !seen[trashFilesDir()] { // listed even before anything was deleted; opening it creates it
		seen[trashFilesDir()] = true
		out = append(out, place{"Trash", trashFilesDir(), false})
	}
	for _, b := range m.cfg.Bookmarks {
		add(filepath.Base(b), b, true)
	}
	for _, mp := range mountPoints() {
		add(filepath.Base(mp), mp, false)
	}
	add("Computer", "/", false)
	return out
}

// mountPoints lists removable/user mounts (udisks, /media, /mnt).
func mountPoints() []string {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		mp := strings.ReplaceAll(fields[1], `\040`, " ")
		if strings.HasPrefix(mp, "/run/media/") || strings.HasPrefix(mp, "/media/") || strings.HasPrefix(mp, "/mnt/") {
			out = append(out, mp)
		}
	}
	return out
}

func (m *model) addBookmark() {
	for _, b := range m.cfg.Bookmarks {
		if b == m.cwd {
			m.setMsg("already bookmarked", false)
			return
		}
	}
	m.cfg.Bookmarks = append(m.cfg.Bookmarks, m.cwd)
	m.cfg.save()
	m.setMsg("bookmarked "+tildePath(m.cwd), false)
}

func (m *model) removeBookmark(path string) {
	var keep []string
	for _, b := range m.cfg.Bookmarks {
		if b != path {
			keep = append(keep, b)
		}
	}
	m.cfg.Bookmarks = keep
	m.cfg.save()
}

// ---- open with / terminal ---------------------------------------------

type app struct{ id, name, path string }

// appsFor lists applications registered for path's content type (via gio).
func appsFor(path string) []app {
	mime := mimeOf(path)
	out, err := exec.Command("gio", "mime", mime).Output()
	if err != nil {
		return nil
	}
	var apps []app
	seen := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(l, "\t") {
			continue
		}
		id := strings.TrimSpace(l)
		if !strings.HasSuffix(id, ".desktop") || seen[id] {
			continue
		}
		seen[id] = true
		p, name := findDesktop(id)
		if p != "" {
			apps = append(apps, app{id: id, name: name, path: p})
		}
	}
	return apps
}

func findDesktop(id string) (path, name string) {
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, ".local/share/applications"), "/usr/local/share/applications", "/usr/share/applications"}
	for _, d := range dirs {
		p := filepath.Join(d, id)
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		name = strings.TrimSuffix(id, ".desktop")
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "Name="); ok {
				name = v
				break
			}
		}
		f.Close()
		return p, name
	}
	return "", ""
}

func launchApp(desktopPath, file string) tea.Cmd {
	return spawn("", "gio", "launch", desktopPath, file)
}

func terminalHere(dir string) tea.Cmd {
	if bin, err := exec.LookPath("xdg-terminal-exec"); err == nil {
		return spawn(dir, bin)
	}
	if t := os.Getenv("TERMINAL"); t != "" {
		return spawn(dir, t)
	}
	return func() tea.Msg { return statusMsg{"no terminal launcher found (xdg-terminal-exec or $TERMINAL)", true} }
}

func openDefault(path string) tea.Cmd { return spawn("", "xdg-open", path) }

// spawn starts a detached process and reports the outcome in the status bar.
func spawn(dir, name string, args ...string) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command(name, args...)
		c.Dir = dir
		c.SysProcAttr = spawnAttr()
		if err := c.Start(); err != nil {
			return statusMsg{fmt.Sprintf("%s: %v", name, err), true}
		}
		go func() { _ = c.Wait() }()
		return statusMsg{"", false}
	}
}
