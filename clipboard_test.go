package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func offerMap(offers []clipOffer) map[string]string {
	m := map[string]string{}
	for _, o := range offers {
		m[o.MimeType] = string(o.Data)
	}
	return m
}

func TestFileOffersFormats(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a b.txt"), filepath.Join(dir, "c.txt")
	mkfile(t, a, "x")
	mkfile(t, b, "y")
	m := offerMap(fileOffers(true, []string{a, b}))
	uriA := "file://" + strings.ReplaceAll(a, " ", "%20")
	if want := "cut\n" + uriA + "\nfile://" + b; m[clipType] != want {
		t.Fatalf("gnome = %q, want %q", m[clipType], want)
	}
	if want := uriA + "\r\nfile://" + b + "\r\n"; m[uriListType] != want {
		t.Fatalf("uri-list = %q", m[uriListType])
	}
	for _, typ := range []string{"text/plain", "text/plain;charset=utf-8", "UTF8_STRING"} {
		if m[typ] != a+"\n"+b {
			t.Fatalf("%s = %q", typ, m[typ])
		}
	}
	if _, ok := m["image/png"]; ok {
		t.Fatal("file copy must not offer image/png")
	}
}

func TestFileOffersImageHasNoPixels(t *testing.T) {
	// Nautilus pastes any clipboard image as a new "Pasted image.png" ahead of
	// the file list, so copying an image file must not carry its pixels.
	p := filepath.Join(t.TempDir(), "shot.png")
	mkfile(t, p, "\x89PNG\r\n\x1a\n")
	for _, o := range fileOffers(false, []string{p}) {
		if strings.HasPrefix(o.MimeType, "image/") {
			t.Fatalf("file copy offers %s", o.MimeType)
		}
	}
}

func TestOffersRoundTrip(t *testing.T) {
	in := []clipOffer{{MimeType: "text/plain", Data: []byte("a\x00b")}, {MimeType: "image/png", Data: nil}}
	var b bytes.Buffer
	if err := writeOffers(&b, in); err != nil {
		t.Fatal(err)
	}
	out, err := readOffers(&b)
	if err != nil || len(out) != 2 || out[0].MimeType != "text/plain" || string(out[0].Data) != "a\x00b" || out[1].MimeType != "image/png" || len(out[1].Data) != 0 {
		t.Fatalf("round trip: %v %+v", err, out)
	}
	if _, err := readOffers(strings.NewReader("text/plain\x00-5\x00")); err == nil {
		t.Fatal("negative length should fail")
	}
}

func TestParseClip(t *testing.T) {
	paths, cut, ok := parseClip("cut\nfile:///tmp/a%20b\nfile:///tmp/c")
	if !ok || !cut || len(paths) != 2 || paths[0] != "/tmp/a b" {
		t.Fatalf("got %v %v %v", paths, cut, ok)
	}
	if _, _, ok := parseClip("garbage"); ok {
		t.Fatal("garbage should not parse")
	}
}

func TestParseURIListAndPathText(t *testing.T) {
	paths, ok := parseURIList("# comment\r\nfile:///tmp/x%20y\r\nhttps://example.com/\r\n")
	if !ok || len(paths) != 1 || paths[0] != "/tmp/x y" {
		t.Fatalf("uri-list: %v %v", paths, ok)
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	mkfile(t, a, "")
	if paths, ok := parsePathText(a + "\n" + dir + "\n"); !ok || len(paths) != 2 {
		t.Fatalf("path text: %v %v", paths, ok)
	}
	for _, s := range []string{"hello world", "relative/path", a + "\n/does/not/exist", ""} {
		if _, ok := parsePathText(s); ok {
			t.Fatalf("%q should not parse as paths", s)
		}
	}
}

func TestClipBackend(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	have := func(tools ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, x := range tools {
				if x == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", os.ErrNotExist
		}
	}
	cases := []struct {
		env   map[string]string
		tools []string
		want  string
	}{
		{map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"}, []string{"xclip"}, "wayland"},
		{map[string]string{"DISPLAY": ":0"}, []string{"xclip", "xsel"}, "xclip"},
		{map[string]string{"DISPLAY": ":0"}, []string{"xsel"}, "xsel"},
		{map[string]string{"DISPLAY": ":0"}, nil, ""},
		{map[string]string{}, []string{"xclip"}, ""},
	}
	for _, c := range cases {
		if got := clipBackend(env(c.env), have(c.tools...)); got != c.want {
			t.Errorf("%v %v: %q, want %q", c.env, c.tools, got, c.want)
		}
	}
}

// fakeTool puts a shell script named name first on PATH.
func fakeTool(t *testing.T, name, script string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(bin, 0o755)
	mkfile(t, filepath.Join(bin, name), "#!/bin/sh\n"+script)
	os.Chmod(filepath.Join(bin, name), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func TestX11ClipboardWithXclip(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":0")
	log := filepath.Join(t.TempDir(), "log")
	dir := t.TempDir()
	a := filepath.Join(dir, "a b.txt")
	mkfile(t, a, "")
	// Copy: xclip gets the paths as text.
	fakeTool(t, "xclip", `echo "$@" >> `+log+`; cat >> `+log+`.in`+"\n")
	if err := clipWrite(false, []string{a}); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(log)
	in, _ := os.ReadFile(log + ".in")
	if !strings.Contains(string(args), "-selection clipboard -in") || string(in) != a {
		t.Fatalf("xclip copy: args %q stdin %q", args, in)
	}
	// Paste: a file manager's cut (GNOME list) is read back as files, and cut.
	fakeTool(t, "xclip", `case "$*" in
*TARGETS*) printf 'TARGETS\nx-special/gnome-copied-files\nUTF8_STRING\n' ;;
*x-special*) printf '%s\n%s' cut 'file://`+strings.ReplaceAll(a, " ", "%20")+`' ;;
esac
`)
	paths, cut, ok := readClipboardFiles()
	if !ok || !cut || len(paths) != 1 || paths[0] != a {
		t.Fatalf("xclip paste: %v %v %v", paths, cut, ok)
	}
}

func TestX11ClipboardTextOnlyAndNone(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":0")
	dir := t.TempDir()
	a := filepath.Join(dir, "x.txt")
	mkfile(t, a, "")
	t.Setenv("PATH", "/nonexistent")
	fakeTool(t, "xsel", `case "$*" in *--output*) printf '%s' '`+a+`' ;; *) cat > /dev/null ;; esac`+"\n")
	paths, cut, ok := readClipboardFiles() // xsel: text paths only
	if !ok || cut || len(paths) != 1 || paths[0] != a {
		t.Fatalf("xsel paste: %v %v %v", paths, cut, ok)
	}
	t.Setenv("DISPLAY", "")
	if err := clipWrite(false, []string{a}); err != errNoClipboard {
		t.Fatalf("no clipboard: %v", err)
	}
}
