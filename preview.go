package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	previewMaxBytes = 128 * 1024
	previewMaxLines = 2000
	tabWidth        = 4
)

// preview is the rendered content of the right-hand pane.
type preview struct {
	title string
	meta  string
	lines []string
}

// buildPreview renders e (inside dir) to lines no wider than width. With wrap
// set, long text lines are word-wrapped; otherwise they are truncated.
func buildPreview(dir string, e entry, width int, wrap, showHidden bool) preview {
	path := filepath.Join(dir, e.name)
	p := preview{title: e.name}
	if width < 1 {
		width = 1
	}
	if e.isDir {
		p.meta = "dir"
		ents, err := readDir(path, showHidden)
		switch {
		case err != nil:
			p.lines = []string{"  cannot read: " + errText(err)}
		case len(ents) == 0:
			p.lines = []string{"  (empty)"}
		default:
			for i, c := range ents {
				if i >= previewMaxLines {
					break
				}
				name := c.name
				if c.isDir {
					name += "/"
				}
				p.lines = append(p.lines, ansi.Truncate(" "+sanitize(name), width, "…"))
			}
		}
		return p
	}

	p.meta = humanSize(e.size)
	f, err := os.Open(path)
	if err != nil {
		p.lines = []string{"  cannot open: " + errText(err)}
		return p
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, previewMaxBytes))
	if err != nil {
		p.lines = []string{"  cannot read: " + errText(err)}
		return p
	}
	if len(buf) == 0 {
		p.lines = []string{"  (empty file)"}
		return p
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		p.lines = []string{"  binary file", "  " + humanSize(e.size)}
		return p
	}

	text := strings.ToValidUTF8(string(buf), "�")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	src := strings.Split(text, "\n")
	if n := len(src); n > 1 && src[n-1] == "" {
		src = src[:n-1]
	}
	for _, line := range src {
		line = expandTabs(sanitize(line))
		switch {
		case line == "":
			p.lines = append(p.lines, "")
		case wrap:
			p.lines = append(p.lines, strings.Split(ansi.Wrap(line, width, ""), "\n")...)
		default:
			p.lines = append(p.lines, ansi.Truncate(line, width, "…"))
		}
		if len(p.lines) >= previewMaxLines {
			break
		}
	}
	if e.size > previewMaxBytes || len(p.lines) >= previewMaxLines {
		p.lines = append(p.lines, "", "… truncated")
	}
	return p
}

// sanitize drops control characters (notably ESC) so file content can never
// inject terminal escape sequences; tabs are kept for expandTabs.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || (r >= 0x20 && r != 0x7f && !(r >= 0x80 && r < 0xa0)) {
			return r
		}
		return -1
	}, s)
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

func errText(err error) string {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return fmt.Sprint(err)
}
