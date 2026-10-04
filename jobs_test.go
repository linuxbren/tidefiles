package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPasteJobProgressAndCancel(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	mkfile(t, filepath.Join(src, "small/a.txt"), "aaa")
	mkfile(t, filepath.Join(src, "small/b.txt"), "bb")
	p := &progress{}
	res := pasteJob(context.Background(), p, dst, []string{filepath.Join(src, "small")}, false)
	if res.err != nil || p.files.Load() != 2 || p.bytes.Load() != 5 || p.totalBytes.Load() != 5 || p.getWhat() != "1 item" {
		t.Fatalf("copy: %v files=%d bytes=%d/%d what=%q", res.err, p.files.Load(), p.bytes.Load(), p.totalBytes.Load(), p.getWhat())
	}

	// A big (sparse) file, cancelled part-way: nothing is left in dst.
	big := filepath.Join(src, "big.bin")
	f, _ := os.Create(big)
	f.Truncate(512 << 20)
	f.Close()
	before := dirNames(t, dst)
	ctx, cancel := context.WithCancel(context.Background())
	p = &progress{}
	done := make(chan opResult)
	go func() { done <- pasteJob(ctx, p, dst, []string{filepath.Join(src, "small"), big}, false) }()
	deadline := time.Now().Add(10 * time.Second)
	for p.bytes.Load() < 8<<20 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	res = <-done
	if res.err != nil || !strings.HasPrefix(res.desc, "Stopped copying") {
		t.Fatalf("cancel: %v %q", res.err, res.desc)
	}
	if got := dirNames(t, dst); got != before {
		t.Fatalf("left behind after cancel: %q (was %q)", got, before)
	}
}

func TestPasteJobMoveIsRename(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	mkfile(t, filepath.Join(src, "m.txt"), "move me")
	res := pasteJob(context.Background(), &progress{}, dst, []string{filepath.Join(src, "m.txt")}, true)
	if res.err != nil || exists(filepath.Join(src, "m.txt")) || !exists(filepath.Join(dst, "m.txt")) {
		t.Fatalf("move: %v", res.err)
	}
}

func TestJobStatus(t *testing.T) {
	j := &job{prog: &progress{verb: "Copying", what: "3 items"}, start: time.Now().Add(-10 * time.Second)}
	j.prog.counting.Store(true)
	if s := j.status(time.Now()); !strings.Contains(s, "counting") {
		t.Errorf("counting: %q", s)
	}
	j.prog.counting.Store(false)
	j.prog.totalBytes.Store(1000)
	j.prog.bytes.Store(250)
	j.prog.totalFiles.Store(4)
	j.prog.files.Store(1)
	s := j.status(time.Now())
	for _, want := range []string{"Copying 3 items", "25%", "1/4 files", "0:30 left", "esc cancels", "█"} {
		if !strings.Contains(s, want) {
			t.Errorf("status %q lacks %q", s, want)
		}
	}
}

func TestCompressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "proj/a.txt"), "alpha")
	mkfile(t, filepath.Join(dir, "proj/sub/b.txt"), "beta")
	mkfile(t, filepath.Join(dir, "solo.txt"), "solo")
	os.Symlink("a.txt", filepath.Join(dir, "proj/link"))
	items := []string{filepath.Join(dir, "proj"), filepath.Join(dir, "solo.txt")}

	p := &progress{}
	res := compressJob(context.Background(), p, dir, items, "out.zip")
	if res.err != nil || res.focus != "out.zip" {
		t.Fatalf("zip: %v", res.err)
	}
	if p.files.Load() != p.totalFiles.Load() || p.bytes.Load() != 5+4+4 {
		t.Errorf("progress: files %d/%d bytes %d", p.files.Load(), p.totalFiles.Load(), p.bytes.Load())
	}
	zr, err := zip.OpenReader(filepath.Join(dir, "out.zip"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
		if f.Name == "proj/link" && f.Mode()&os.ModeSymlink == 0 {
			t.Error("symlink not stored as a link")
		}
	}
	zr.Close()
	if got["proj/a.txt"] != "alpha" || got["proj/sub/b.txt"] != "beta" || got["solo.txt"] != "solo" || got["proj/link"] != "a.txt" {
		t.Fatalf("zip contents: %v", got)
	}

	res = compressJob(context.Background(), &progress{}, dir, items, "out.tar.gz")
	if res.err != nil {
		t.Fatal(res.err)
	}
	f, _ := os.Open(filepath.Join(dir, "out.tar.gz"))
	gz, _ := gzip.NewReader(f)
	tr := tar.NewReader(gz)
	got = map[string]string{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b)
		if h.Name == "proj/link" && h.Linkname != "a.txt" {
			t.Errorf("tar link: %q", h.Linkname)
		}
	}
	f.Close()
	if got["proj/sub/b.txt"] != "beta" || got["solo.txt"] != "solo" {
		t.Fatalf("tar contents: %v", got)
	}
}

func TestCompressNeverOverwritesOrLeavesPartials(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "a.txt"), "a")
	mkfile(t, filepath.Join(dir, "taken.zip"), "precious")
	items := []string{filepath.Join(dir, "a.txt")}
	if res := compressJob(context.Background(), &progress{}, dir, items, "taken.zip"); res.err == nil {
		t.Fatal("overwrote an existing file")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "taken.zip")); string(b) != "precious" {
		t.Fatal("existing file changed")
	}
	if res := compressJob(context.Background(), &progress{}, dir, items, "a.rar"); res.err == nil {
		t.Fatal("unsupported format accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := compressJob(ctx, &progress{}, dir, items, "new.zip")
	if !strings.HasPrefix(res.desc, "Stopped") || dirNames(t, dir) != "a.txt taken.zip" {
		t.Fatalf("cancel: %q, dir %q", res.desc, dirNames(t, dir))
	}
}

func TestDefaultArchiveName(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "report.pdf"), "")
	os.Mkdir(filepath.Join(dir, "photos"), 0o755)
	mkfile(t, filepath.Join(dir, "photos.zip"), "")
	if got := defaultArchiveName(dir, []string{filepath.Join(dir, "report.pdf")}); got != "report.zip" {
		t.Errorf("file: %q", got)
	}
	if got := defaultArchiveName(dir, []string{filepath.Join(dir, "photos")}); got != "photos (2).zip" {
		t.Errorf("taken: %q", got)
	}
	if got := defaultArchiveName(dir, []string{filepath.Join(dir, "report.pdf"), filepath.Join(dir, "photos")}); got != filepath.Base(dir)+".zip" {
		t.Errorf("several: %q", got)
	}
}

func TestStoppedMoveDesc(t *testing.T) {
	if got := stoppedMoveDesc(12, 40); !strings.HasPrefix(got, "Moved 12 of 40 — stopped") {
		t.Errorf("%q", got)
	}
	if got := stoppedMoveDesc(0, 3); !strings.HasPrefix(got, "Moved 0 of 3 — stopped") {
		t.Errorf("%q", got)
	}
}
