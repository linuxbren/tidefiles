package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var testSyntax = syntax{fg: "#c0caf5", kw: "#7aa2f7", typ: "#2ac3de", fn: "#7dcfff", builtin: "#bb9af7",
	str: "#9ece6a", num: "#ff9e64", comment: "#565f89", op: "#89ddff", tag: "#e0af68", err: "#f7768e"}

func TestRenderMarkdown(t *testing.T) {
	src := "# Title\n\nSome **bold** text and a [link](https://example.com).\n\n- one\n- two\n\n```go\nfunc main() {}\n```\n"
	out, err := renderMarkdown(src, 40, testSyntax)
	if err != nil {
		t.Fatal(err)
	}
	plain := ansi.Strip(out)
	for _, want := range []string{"# Title", "bold", "• one", "func main() {}", "https://example.com"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "**") {
		t.Error("emphasis markers should be rendered, not shown")
	}
	for _, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("line wider than wrap width (%d): %q", w, ansi.Strip(l))
		}
		if p := ansi.Strip(l); strings.TrimRight(p, " ") != p {
			t.Errorf("trailing padding kept: %q", p)
		}
	}
}

func TestTrimTrailingSpace(t *testing.T) {
	cases := map[string]string{
		"abc   ":                        "abc",
		"\x1b[1mabc\x1b[0m   \x1b[0m  ": "\x1b[1mabc\x1b[0m\x1b[0m",
		"a b  \x1b[38;2;1;2;3m   ":      "a b\x1b[38;2;1;2;3m",
		"   ":                           "",
	}
	for in, want := range cases {
		if got := trimTrailingSpace(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
