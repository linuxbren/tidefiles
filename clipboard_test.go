package main

import (
	"bytes"
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
