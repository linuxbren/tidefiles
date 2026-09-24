package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func writeTemp(t *testing.T, name, content string) (string, entry) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ents, err := readDir(dir, true)
	if err != nil || len(ents) != 1 {
		t.Fatalf("readDir: %v %v", ents, err)
	}
	return dir, ents[0]
}

func TestPreviewWrapsAtWordBoundaries(t *testing.T) {
	dir, e := writeTemp(t, "a.txt", "the quick brown fox jumps over the lazy dog\n")
	p := buildPreview(dir, e, 16, true, false)
	if len(p.lines) < 3 {
		t.Fatalf("expected wrapped lines, got %q", p.lines)
	}
	for _, l := range p.lines {
		if ansi.StringWidth(l) > 16 {
			t.Errorf("line %q wider than 16", l)
		}
	}
	if strings.Join(p.lines, " ") != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("words split mid-word: %q", p.lines)
	}
}

func TestPreviewNoWrapTruncates(t *testing.T) {
	dir, e := writeTemp(t, "a.txt", strings.Repeat("x", 100)+"\n")
	p := buildPreview(dir, e, 20, false, false)
	if len(p.lines) != 1 || ansi.StringWidth(p.lines[0]) > 20 {
		t.Errorf("got %q", p.lines)
	}
}

func TestPreviewStripsEscapesAndDetectsBinary(t *testing.T) {
	dir, e := writeTemp(t, "a.txt", "hi \x1b[31mred\x1b[0m\n")
	p := buildPreview(dir, e, 40, true, false)
	if strings.ContainsRune(strings.Join(p.lines, ""), 0x1b) {
		t.Errorf("escape leaked into preview: %q", p.lines)
	}
	dir, e = writeTemp(t, "b.bin", "abc\x00def")
	if p := buildPreview(dir, e, 40, true, false); !strings.Contains(p.lines[0], "binary") {
		t.Errorf("expected binary notice, got %q", p.lines)
	}
}
