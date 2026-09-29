package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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

func writeTestImage(t *testing.T, path string, enc func(*os.File, image.Image) error) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := enc(f, img); err != nil {
		t.Fatal(err)
	}
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
	if _, ok := m[pngType]; ok {
		t.Fatal("non-image copy must not offer image/png")
	}
}

func TestFileOffersImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	writeTestImage(t, p, func(f *os.File, img image.Image) error { return png.Encode(f, img) })
	raw, _ := os.ReadFile(p)
	if m := offerMap(fileOffers(false, []string{p})); m[pngType] != string(raw) {
		t.Fatal("single png should be offered as its own bytes")
	}
	q := filepath.Join(dir, "other.png")
	writeTestImage(t, q, func(f *os.File, img image.Image) error { return png.Encode(f, img) })
	if _, ok := offerMap(fileOffers(false, []string{p, q}))[pngType]; ok {
		t.Fatal("multi-file copy must not offer image/png")
	}
}

func TestClipPNGConvertsJPEG(t *testing.T) {
	p := filepath.Join(t.TempDir(), "photo.jpg")
	writeTestImage(t, p, func(f *os.File, img image.Image) error { return jpeg.Encode(f, img, nil) })
	data, ok := clipPNG(p)
	if !ok {
		t.Fatal("jpeg should convert")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 4 || img.Bounds().Dy() != 3 {
		t.Fatalf("converted png: %v %v", err, img)
	}
	txt := filepath.Join(t.TempDir(), "notes.txt")
	mkfile(t, txt, "hi")
	if _, ok := clipPNG(txt); ok {
		t.Fatal("text file is not an image")
	}
}

func TestOffersRoundTrip(t *testing.T) {
	in := []clipOffer{{MimeType: "text/plain", Data: []byte("a\x00b")}, {MimeType: pngType, Data: nil}}
	var b bytes.Buffer
	if err := writeOffers(&b, in); err != nil {
		t.Fatal(err)
	}
	out, err := readOffers(&b)
	if err != nil || len(out) != 2 || out[0].MimeType != "text/plain" || string(out[0].Data) != "a\x00b" || out[1].MimeType != pngType || len(out[1].Data) != 0 {
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
