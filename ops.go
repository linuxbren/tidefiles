package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

func copyPath(src, dst string) error {
	if dst == src || strings.HasPrefix(dst, src+string(filepath.Separator)) {
		return fmt.Errorf("cannot copy %s into itself", filepath.Base(src))
	}
	return copyTree(src, dst)
}

func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := copyTree(filepath.Join(src, de.Name()), filepath.Join(dst, de.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, info.Mode().Perm())
	case info.Mode().IsRegular():
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chtimes(dst, time.Now(), info.ModTime())
	default:
		return fmt.Errorf("%s: unsupported file type", filepath.Base(src))
	}
}

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

// pasteInto copies or moves items into dir. Name conflicts are resolved by
// renaming the incoming item ("name (copy)"), never by overwriting.
func pasteInto(dir string, items []string, cut bool) opResult {
	type pair struct{ src, dst string }
	var done []pair
	var firstErr error
	for _, src := range items {
		base := filepath.Base(src)
		if !exists(src) || (cut && filepath.Dir(src) == dir) {
			continue
		}
		dst := uniqueDest(dir, base)
		var err error
		if cut {
			err = movePath(src, dst)
		} else {
			err = copyPath(src, dst)
		}
		if err != nil {
			firstErr = err
			break
		}
		done = append(done, pair{src, dst})
	}
	verb := "Pasted"
	if cut {
		verb = "Moved"
	}
	res := opResult{err: firstErr}
	if len(done) > 0 {
		res.focus = filepath.Base(done[0].dst)
		res.desc = fmt.Sprintf("%s %s", verb, plural(len(done), "item"))
		res.undo = func() error {
			for _, p := range done {
				if cut {
					back := p.src
					if exists(back) {
						back = uniqueDest(filepath.Dir(p.src), filepath.Base(p.src))
					}
					if err := movePath(p.dst, back); err != nil {
						return err
					}
				} else if _, err := trashPath(p.dst); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return res
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
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

// ---- system clipboard (Nautilus/Thunar compatible file lists) ----------

const clipType = "x-special/gnome-copied-files"

// Variables so tests can stub the desktop.
var (
	clipWrite = func(cut bool, paths []string) error {
		verb := "copy"
		if cut {
			verb = "cut"
		}
		var b strings.Builder
		b.WriteString(verb)
		for _, p := range paths {
			b.WriteString("\nfile://" + (&url.URL{Path: p}).EscapedPath())
		}
		return runWithStdin(b.String(), "wl-copy", "--type", clipType)
	}
	clipRead = func() (paths []string, cut bool, ok bool) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", clipType).Output()
		if err != nil {
			return nil, false, false
		}
		return parseClip(string(out))
	}
	textWrite = func(s string) error { return runWithStdin(s, "wl-copy") }
)

func runWithStdin(input, name string, args ...string) error {
	bin, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.Run()
}

func parseClip(s string) (paths []string, cut bool, ok bool) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) < 2 || (lines[0] != "copy" && lines[0] != "cut") {
		return nil, false, false
	}
	for _, l := range lines[1:] {
		u, err := url.Parse(strings.TrimSpace(l))
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		paths = append(paths, u.Path)
	}
	return paths, lines[0] == "cut", len(paths) > 0
}
