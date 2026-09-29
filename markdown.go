package main

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
)

var markdownExts = setOf(".md", ".markdown", ".mdown", ".mkd", ".mdx")

// markdownPreview renders a markdown file, falling back to the plain text
// preview for anything unusual (unreadable, binary, a render error).
func markdownPreview(p preview, path string, e entry, o previewOpts) preview {
	f, err := os.Open(path)
	if err != nil {
		return textPreview(p, path, e, o)
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, previewMaxBytes))
	if err != nil || len(buf) == 0 || bytes.IndexByte(buf, 0) >= 0 {
		return textPreview(p, path, e, o)
	}
	src := strings.Split(strings.ReplaceAll(strings.ToValidUTF8(string(buf), "�"), "\r\n", "\n"), "\n")
	for i, l := range src {
		src[i] = expandTabs(sanitize(l)) // content can't inject escape sequences
	}
	// Rendered markdown is prose, so it always wraps to the pane (glamour has no
	// "don't wrap" mode); the w toggle still applies to source view (m).
	out, err := renderMarkdown(strings.Join(src, "\n"), o.width, o.syn)
	if err != nil {
		return textPreview(p, path, e, o)
	}
	p.meta = "markdown · " + humanSize(e.size)
	o.wrap = true
	p.lines = fitLines(strings.Split(out, "\n"), o, previewMaxLines)
	if e.size > previewMaxBytes || len(p.lines) >= previewMaxLines {
		p.lines = append(p.lines, "", "… truncated")
	}
	return p
}

// renderMarkdown renders markdown source for the preview pane in the given
// palette, word-wrapped to width.
func renderMarkdown(src string, width int, s syntax) (string, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle(s)),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(termenv.TrueColor),
	)
	if err != nil {
		return "", err
	}
	out, err := r.Render(src)
	if err != nil {
		return "", err
	}
	// glamour pads every line to the wrap width; the pane draws its own
	// background, so the padding only gets in the way of truncation.
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, l := range lines {
		lines[i] = trimTrailingSpace(l)
	}
	return strings.Join(lines, "\n"), nil
}

// markdownStyle is glamour's dark style recoloured from the preview palette,
// so rendered markdown follows the Omarchy theme like highlighted code does.
func markdownStyle(s syntax) gansi.StyleConfig {
	c := func(v string) *string { return &v }
	on := func() *bool { v := true; return &v }
	zero := func() *uint { var v uint; return &v }
	prim := func(color string) gansi.StylePrimitive { return gansi.StylePrimitive{Color: c(color)} }

	st := styles.DarkStyleConfig
	st.Document = gansi.StyleBlock{StylePrimitive: prim(s.fg), Margin: zero()}
	st.Paragraph = gansi.StyleBlock{}
	st.BlockQuote = gansi.StyleBlock{StylePrimitive: prim(s.comment), Indent: func() *uint { v := uint(1); return &v }(), IndentToken: c("│ ")}
	st.Heading = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{BlockSuffix: "\n", Color: c(s.kw), Bold: on()}}
	st.H1 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "# ", Color: c(s.kw), Bold: on()}}
	st.H2 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "## ", Color: c(s.fn)}}
	st.H3 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "### ", Color: c(s.typ)}}
	st.H4 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "#### ", Color: c(s.typ)}}
	st.H5 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "##### ", Color: c(s.typ)}}
	st.H6 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "###### ", Color: c(s.comment)}}
	st.HorizontalRule = gansi.StylePrimitive{Color: c(s.comment), Format: "\n────────\n"}
	st.Item = gansi.StylePrimitive{BlockPrefix: "• "}
	st.Enumeration = gansi.StylePrimitive{BlockPrefix: ". "}
	st.Link = gansi.StylePrimitive{Color: c(s.comment), Underline: on()}
	st.LinkText = gansi.StylePrimitive{Color: c(s.fn), Bold: on()}
	st.Image = gansi.StylePrimitive{Color: c(s.comment), Underline: on()}
	st.ImageText = gansi.StylePrimitive{Color: c(s.builtin), Format: "Image: {{.text}} →"}
	st.Code = gansi.StyleBlock{StylePrimitive: prim(s.str)}
	st.Table = gansi.StyleTable{StyleBlock: gansi.StyleBlock{StylePrimitive: prim(s.fg)}}
	st.CodeBlock = gansi.StyleCodeBlock{
		StyleBlock: gansi.StyleBlock{StylePrimitive: prim(s.fg), Margin: func() *uint { v := uint(2); return &v }()},
		Chroma: &gansi.Chroma{
			Text:            prim(s.fg),
			Error:           prim(s.err),
			Comment:         gansi.StylePrimitive{Color: c(s.comment), Italic: on()},
			CommentPreproc:  prim(s.builtin),
			Keyword:         prim(s.kw),
			KeywordReserved: prim(s.kw),
			KeywordType:     prim(s.typ),
			Operator:        prim(s.op),
			Punctuation:     prim(s.fg),
			Name:            prim(s.fg),
			NameBuiltin:     prim(s.builtin),
			NameTag:         prim(s.tag),
			NameAttribute:   prim(s.fn),
			NameClass:       gansi.StylePrimitive{Color: c(s.typ), Bold: on()},
			NameDecorator:   prim(s.builtin),
			NameFunction:    prim(s.fn),
			LiteralNumber:   prim(s.num),
			LiteralString:   prim(s.str),
			GenericDeleted:  prim(s.err),
			GenericInserted: prim(s.str),
		},
	}
	return st
}

// trimTrailingSpace drops the spaces after a line's last visible character,
// keeping any ANSI sequences there so styles are still reset.
func trimTrailingSpace(l string) string {
	end := 0 // byte offset just past the last visible non-space character
	for i := 0; i < len(l); {
		if l[i] == 0x1b && i+1 < len(l) && l[i+1] == '[' {
			j := i + 2
			for j < len(l) && (l[j] < 0x40 || l[j] > 0x7e) {
				j++
			}
			i = j + 1
			continue
		}
		if l[i] != ' ' {
			end = i + 1
		}
		i++
	}
	return l[:end] + strings.ReplaceAll(l[end:], " ", "")
}
