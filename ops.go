package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// opResult is the outcome of a file operation run off the UI goroutine.
type opResult struct {
	desc  string       // status text on success
	undo  func() error // nil when the operation cannot be undone
	focus string       // name to select after reloading
	err   error
}

// ---- naming ------------------------------------------------------------

func splitExt(name string) (stem, ext string) {
	ext = filepath.Ext(name)
	if ext == name { // dotfile such as .bashrc
		return name, ""
	}
	stem = strings.TrimSuffix(name, ext)
	if strings.HasSuffix(stem, ".tar") {
		return strings.TrimSuffix(stem, ".tar"), ".tar" + ext
	}
	return stem, ext
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// uniqueDest returns dir/name, or a "name (copy).ext" variant if taken.
func uniqueDest(dir, name string) string {
	if p := filepath.Join(dir, name); !exists(p) {
		return p
	}
	stem, ext := splitExt(name)
	for i := 1; ; i++ {
		suffix := " (copy)"
		if i > 1 {
			suffix = fmt.Sprintf(" (copy %d)", i)
		}
		if p := filepath.Join(dir, stem+suffix+ext); !exists(p) {
			return p
		}
	}
}

// ---- copy / move -------------------------------------------------------

func copyPath(src, dst string) error { return copyPathCtx(context.Background(), src, dst, nil) }

// movePath renames src to dst, falling back to copy+delete across filesystems.
// It refuses to overwrite an existing dst.
func movePath(src, dst string) error {
	if exists(dst) {
		return fmt.Errorf("%s already exists", filepath.Base(dst))
	}
	err := os.Rename(src, dst)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyPath(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

// pasteInto copies or moves items into dir; see pasteJob.
func pasteInto(dir string, items []string, cut bool) opResult {
	return pasteJob(context.Background(), &progress{}, dir, items, cut)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "ch") || strings.HasSuffix(word, "sh") || strings.HasSuffix(word, "s") || strings.HasSuffix(word, "x") {
		return fmt.Sprintf("%d %ses", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ---- rename / create ---------------------------------------------------

func validName(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return errors.New("invalid name")
	case strings.ContainsRune(name, '/'):
		return errors.New("name cannot contain /")
	}
	return nil
}

func renameOp(dir, oldName, newName string) opResult {
	if err := validName(newName); err != nil {
		return opResult{err: err}
	}
	if newName == oldName {
		return opResult{}
	}
	from, to := filepath.Join(dir, oldName), filepath.Join(dir, newName)
	if err := movePath(from, to); err != nil {
		return opResult{err: err}
	}
	return opResult{desc: "Renamed to " + newName, focus: newName,
		undo: func() error { return movePath(to, from) }}
}

func createOp(dir, name string, folder bool) opResult {
	if err := validName(name); err != nil {
		return opResult{err: err}
	}
	p := filepath.Join(dir, name)
	if exists(p) {
		return opResult{err: fmt.Errorf("%s already exists", name)}
	}
	var err error
	if folder {
		err = os.Mkdir(p, 0o755)
	} else {
		var f *os.File
		if f, err = os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
			err = f.Close()
		}
	}
	if err != nil {
		return opResult{err: err}
	}
	what := "file"
	if folder {
		what = "folder"
	}
	return opResult{desc: "Created " + what + " " + name, focus: name,
		undo: func() error { return os.Remove(p) }}
}

// ---- trash (freedesktop.org Trash spec, home trash) --------------------

func trashDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "Trash")
}

func trashFilesDir() string { return filepath.Join(trashDir(), "files") }

// trashPath moves path to the trash and returns a function that restores it.
// Files on another filesystem fall back to `gio trash`, which cannot be undone
// (restore is nil).
func trashPath(path string) (restore func() error, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	files, infos := filepath.Join(trashDir(), "files"), filepath.Join(trashDir(), "info")
	for _, d := range []string{files, infos} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	name := filepath.Base(path)
	for i := 2; exists(filepath.Join(files, name)) || exists(filepath.Join(infos, name+".trashinfo")); i++ {
		name = fmt.Sprintf("%s.%d", filepath.Base(path), i)
	}
	info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: path}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
	infoPath := filepath.Join(infos, name+".trashinfo")
	if err := os.WriteFile(infoPath, []byte(info), 0o600); err != nil {
		return nil, err
	}
	dst := filepath.Join(files, name)
	if err := os.Rename(path, dst); err != nil {
		_ = os.Remove(infoPath)
		if !errors.Is(err, syscall.EXDEV) {
			return nil, err
		}
		if gio, lerr := exec.LookPath("gio"); lerr == nil {
			if out, gerr := exec.Command(gio, "trash", path).CombinedOutput(); gerr != nil {
				return nil, fmt.Errorf("gio trash: %s", strings.TrimSpace(string(out)))
			}
			return nil, nil
		}
		return nil, errors.New("cannot trash across filesystems")
	}
	return func() error { return restoreTrashed(name) }, nil
}

// restoreTrashed moves a trashed item back to where it came from.
func restoreTrashed(name string) error {
	infoPath := filepath.Join(trashDir(), "info", name+".trashinfo")
	orig, err := trashOrigin(infoPath)
	if err != nil {
		return fmt.Errorf("no restore info for %s", name)
	}
	if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
		return err
	}
	dst := orig
	if exists(dst) {
		dst = uniqueDest(filepath.Dir(orig), filepath.Base(orig))
	}
	if err := movePath(filepath.Join(trashFilesDir(), name), dst); err != nil {
		return err
	}
	_ = os.Remove(infoPath)
	return nil
}

func trashOrigin(infoPath string) (string, error) {
	f, err := os.Open(infoPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "Path="); ok {
			return url.PathUnescape(v)
		}
	}
	return "", errors.New("no Path in trashinfo")
}

func trashItems(paths []string) opResult {
	var restores []func() error
	var firstErr error
	n := 0
	for _, p := range paths {
		restore, err := trashPath(p)
		if err != nil {
			firstErr = err
			break
		}
		n++
		if restore != nil {
			restores = append(restores, restore)
		}
	}
	res := opResult{err: firstErr}
	if n > 0 {
		res.desc = "Moved " + plural(n, "item") + " to trash"
		if len(restores) == n {
			res.undo = func() error {
				for _, r := range restores {
					if err := r(); err != nil {
						return err
					}
				}
				return nil
			}
		}
	}
	return res
}

func restoreItems(names []string) opResult {
	n := 0
	for _, name := range names {
		if err := restoreTrashed(name); err != nil {
			return opResult{err: err, desc: restoredDesc(n)}
		}
		n++
	}
	return opResult{desc: restoredDesc(n)}
}

func restoredDesc(n int) string {
	if n == 0 {
		return ""
	}
	return "Restored " + plural(n, "item")
}

func purgeItems(paths []string) opResult {
	for i, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return opResult{err: err, desc: "Deleted " + plural(i, "item")}
		}
		if filepath.Dir(p) == trashFilesDir() {
			_ = os.Remove(filepath.Join(trashDir(), "info", filepath.Base(p)+".trashinfo"))
		}
	}
	return opResult{desc: "Permanently deleted " + plural(len(paths), "item")}
}

func emptyTrash() opResult {
	n := 0
	for _, sub := range []string{"files", "info"} {
		des, _ := os.ReadDir(filepath.Join(trashDir(), sub))
		for _, de := range des {
			if err := os.RemoveAll(filepath.Join(trashDir(), sub, de.Name())); err != nil {
				return opResult{err: err}
			}
			if sub == "files" {
				n++
			}
		}
	}
	return opResult{desc: "Trash emptied (" + plural(n, "item") + ")"}
}
