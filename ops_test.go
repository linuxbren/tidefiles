package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUniqueDest(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "a.txt"), "x")
	mkfile(t, filepath.Join(dir, "a (copy).txt"), "x")
	mkfile(t, filepath.Join(dir, "b.tar.gz"), "x")
	cases := map[string]string{
		"a.txt":    "a (copy 2).txt",
		"new.txt":  "new.txt",
		"b.tar.gz": "b (copy).tar.gz",
	}
	for in, want := range cases {
		if got := filepath.Base(uniqueDest(dir, in)); got != want {
			t.Errorf("uniqueDest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPasteCopyAndUndo(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	mkfile(t, filepath.Join(src, "d", "f.txt"), "hello")
	mkfile(t, filepath.Join(dst, "d"), "already here")

	res := pasteInto(dst, []string{filepath.Join(src, "d")}, false)
	if res.err != nil {
		t.Fatal(res.err)
	}
	if got := read(t, filepath.Join(dst, "d (copy)", "f.txt")); got != "hello" {
		t.Fatalf("copied content %q", got)
	}
	if read(t, filepath.Join(dst, "d")) != "already here" {
		t.Fatal("existing file was overwritten")
	}
	if err := res.undo(); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, "d (copy)")) {
		t.Fatal("undo left the copy behind")
	}
}

func TestPasteMoveAndUndo(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	mkfile(t, filepath.Join(src, "f.txt"), "data")
	res := pasteInto(dst, []string{filepath.Join(src, "f.txt")}, true)
	if res.err != nil || exists(filepath.Join(src, "f.txt")) || read(t, filepath.Join(dst, "f.txt")) != "data" {
		t.Fatalf("move failed: %+v", res)
	}
	if err := res.undo(); err != nil || read(t, filepath.Join(src, "f.txt")) != "data" {
		t.Fatalf("undo failed: %v", err)
	}
}

func TestCopyIntoItselfRejected(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "d", "f"), "x")
	if res := pasteInto(filepath.Join(dir, "d"), []string{filepath.Join(dir, "d")}, false); res.err == nil {
		t.Fatal("expected error copying a directory into itself")
	}
}

func TestTrashAndRestore(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	dir := t.TempDir()
	p := filepath.Join(dir, "my file.txt")
	mkfile(t, p, "bye")

	res := trashItems([]string{p})
	if res.err != nil || res.undo == nil {
		t.Fatalf("trash: %+v", res)
	}
	if exists(p) {
		t.Fatal("file still present")
	}
	info := read(t, filepath.Join(data, "Trash", "info", "my file.txt.trashinfo"))
	if want := "Path=" + dir + "/my%20file.txt"; !strings.Contains(info, want) {
		t.Fatalf("trashinfo missing %q:\n%s", want, info)
	}
	// a second file of the same name must not clobber the first
	mkfile(t, p, "second")
	if r := trashItems([]string{p}); r.err != nil {
		t.Fatal(r.err)
	}
	if err := res.undo(); err != nil {
		t.Fatal(err)
	}
	if read(t, p) != "bye" {
		t.Fatal("restore returned wrong content")
	}
}

func TestRenameCreateUndo(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "a"), "1")
	mkfile(t, filepath.Join(dir, "b"), "2")
	if r := renameOp(dir, "a", "b"); r.err == nil {
		t.Fatal("rename over existing file should fail")
	}
	if r := renameOp(dir, "a", "x/y"); r.err == nil {
		t.Fatal("slash in name should fail")
	}
	r := renameOp(dir, "a", "c")
	if r.err != nil || !exists(filepath.Join(dir, "c")) {
		t.Fatalf("rename: %+v", r)
	}
	if err := r.undo(); err != nil || !exists(filepath.Join(dir, "a")) {
		t.Fatalf("undo rename: %v", err)
	}
	f := createOp(dir, "newdir", true)
	if f.err != nil || !exists(filepath.Join(dir, "newdir")) {
		t.Fatalf("mkdir: %+v", f)
	}
	if err := f.undo(); err != nil || exists(filepath.Join(dir, "newdir")) {
		t.Fatalf("undo mkdir: %v", err)
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

func TestSortEntries(t *testing.T) {
	ents := []entry{{name: "b.txt", size: 5}, {name: "a", isDir: true}, {name: "c.go", size: 9}}
	sortEntries(ents, "size", true)
	if ents[0].name != "a" || ents[1].name != "c.go" {
		t.Fatalf("got %v", ents)
	}
}
