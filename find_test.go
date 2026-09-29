package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func findPaths(paths ...string) *findState {
	f := &findState{}
	for _, p := range paths {
		f.paths = append(f.paths, p)
		f.lower = append(f.lower, strings.ToLower(p))
	}
	return f
}

func TestFindRanking(t *testing.T) {
	f := findPaths(
		"docs/old/readme-notes.txt",
		"README.md",
		"src/reader/main.go",
		"internal/omarchy/omarchy.go",
		"vendor/r/e/a/d/m/e.txt",
	)
	f.rescore("readme")
	if len(f.results) < 2 || f.results[0].path != "README.md" {
		t.Fatalf("readme: %+v", f.results)
	}
	for _, r := range f.results {
		if r.path == "src/reader/main.go" {
			t.Fatalf("reader/main.go lacks the letters in order: %+v", f.results)
		}
	}
	f.rescore("om go") // every term must match
	if len(f.results) != 1 || f.results[0].path != "internal/omarchy/omarchy.go" {
		t.Fatalf("om go: %+v", f.results)
	}
	f.rescore("README") // capitals make it case-sensitive
	if len(f.results) != 1 || f.results[0].path != "README.md" {
		t.Fatalf("smart case: %+v", f.results)
	}
	f.rescore("   ")
	if len(f.results) != 0 {
		t.Fatal("blank query lists nothing")
	}
}

func TestFuzzyPositions(t *testing.T) {
	_, pos, ok := fuzzyScore("café/menü.txt", "mü")
	if !ok || len(pos) != 2 || !strings.HasPrefix("café/menü.txt"[pos[0]:], "m") || !strings.HasPrefix("café/menü.txt"[pos[1]:], "ü") {
		t.Fatalf("byte offsets for non-ASCII: %v %v", pos, ok)
	}
	// The tightest window wins: "ab" should match the adjacent pair, not a…b.
	_, pos, _ = fuzzyScore("a-x-ab", "ab")
	if pos[0] != 4 || pos[1] != 5 {
		t.Fatalf("tightest match: %v", pos)
	}
}

func TestHighlightMatchTruncates(t *testing.T) {
	mt, _ := matchPath("some/very/long/directory/name/file.txt", "some/very/long/directory/name/file.txt", []string{"file"})
	out := highlightMatch(mt, 16, func(s ...string) string { return "[" + strings.Join(s, "") + "]" })
	plain := strings.NewReplacer("[", "", "]", "").Replace(out)
	if !strings.HasPrefix(plain, "…") || !strings.HasSuffix(plain, "file.txt") || ansi.StringWidth(plain) > 16 {
		t.Fatalf("truncated: %q", out)
	}
	if !strings.Contains(out, "[f][i][l][e]") {
		t.Fatalf("highlight lost after truncation: %q", out)
	}
}

func TestIndexTree(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a/b/deep.txt", "top.txt", ".hidden/x.txt", "a/.secret"} {
		mkfile(t, filepath.Join(root, p), "")
	}
	collect := func(hidden bool) map[string]bool {
		ch := make(chan findBatchMsg, 4)
		go indexTree(context.Background(), root, hidden, 1, ch)
		got := map[string]bool{}
		for msg := range ch {
			for _, p := range msg.paths {
				got[p] = true
			}
		}
		return got
	}
	got := collect(false)
	for _, want := range []string{"a/", "a/b/", "a/b/deep.txt", "top.txt"} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if got[".hidden/"] || got["a/.secret"] || got[".hidden/x.txt"] {
		t.Errorf("hidden entries indexed: %v", got)
	}
	if got := collect(true); !got[".hidden/x.txt"] || !got["a/.secret"] {
		t.Errorf("hidden on: %v", got)
	}
}
