package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-sixel"
)

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

func writePNG(t *testing.T, dir, name string, img image.Image) entry {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ents, _ := readDir(dir, true)
	return ents[0]
}

func TestFitSizePreservesAspect(t *testing.T) {
	w, h := fitSize(2000, 1000, 100, 100)
	if w != 100 || h != 50 {
		t.Errorf("got %dx%d", w, h)
	}
	if w, h := fitSize(10, 10, 1000, 1000); w > 30 || h > 30 {
		t.Errorf("upscale not capped: %dx%d", w, h)
	}
}

func TestBlocksFitPane(t *testing.T) {
	lines := renderBlocks(testImage(400, 300), 30, 10, 10, 20, "#000000")
	if len(lines) > 10 {
		t.Errorf("%d rows, want <= 10", len(lines))
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("row width %d > 30", w)
		}
	}
}

func TestSixelDecodesToExpectedSize(t *testing.T) {
	seq, err := renderSixel(testImage(400, 300), 40, 10, 10, 20, "#000000")
	if err != nil {
		t.Fatal(err)
	}
	if seq.cols > 40 || seq.rows > 10 {
		t.Errorf("footprint %dx%d exceeds pane", seq.cols, seq.rows)
	}
	var out image.Image
	if err := sixel.NewDecoder(strings.NewReader(seq.seq)).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() > 400 || b.Dy() > 200 || b.Dx() == 0 {
		t.Errorf("decoded %v", b)
	}
}

func TestKittySequenceIsChunked(t *testing.T) {
	seq, err := renderKitty(testImage(300, 300), 30, 10, 10, 20, "#000000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seq.seq, "\x1b_Ga=T,f=100") || !strings.HasSuffix(seq.seq, "\x1b\\") {
		t.Errorf("unexpected framing: %.40q", seq.seq)
	}
}

func TestImagePreviewBlocks(t *testing.T) {
	dir := t.TempDir()
	e := writePNG(t, dir, "p.png", testImage(64, 32))
	p := buildPreview(dir, e, testOpts(30, true))
	if len(p.lines) == 0 || !strings.Contains(p.meta, "64×32") {
		t.Errorf("meta %q lines %d", p.meta, len(p.lines))
	}
}

func TestGfxOutRedrawsAfterFrameAndErasesOnChange(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	g := newGfxOut(f)
	g.set(&overlay{key: "a", seq: "IMG-A", row: 2, col: 5, clrRow: 2, clrCol: 5, clrCols: 3, clrRows: 2}, "#000000")
	if _, err := g.Write([]byte("FRAME1")); err != nil {
		t.Fatal(err)
	}
	g.set(nil, "#000000")
	if _, err := g.Write([]byte("FRAME2")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, _ := os.ReadFile(f.Name())
	s := string(b)
	i1, ia, i2 := strings.Index(s, "FRAME1"), strings.Index(s, "IMG-A"), strings.Index(s, "FRAME2")
	if !(i1 >= 0 && ia > i1 && i2 > ia) {
		t.Fatalf("image must follow its frame: %q", s)
	}
	if strings.Index(s[ia:i2], "\x1b[3;6H") < 0 {
		t.Errorf("expected erase before FRAME2: %q", s[ia:i2])
	}
}
