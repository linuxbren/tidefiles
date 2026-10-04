package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// compressFormat picks the format from the name the user typed.
func compressFormat(name string) (string, error) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip", nil
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz", nil
	}
	return "", errors.New("the name must end in .zip or .tar.gz")
}

// defaultArchiveName suggests "<item>.zip" for one item, "<folder>.zip" for
// several, never one that already exists.
func defaultArchiveName(dir string, items []string) string {
	base := filepath.Base(dir)
	if len(items) == 1 {
		base = filepath.Base(items[0])
		if s, ext := splitExt(base); ext != "" && !strings.HasPrefix(base, ".") {
			if st, err := os.Stat(items[0]); err == nil && !st.IsDir() {
				base = s
			}
		}
	}
	if base == "/" || base == "." || base == "" {
		base = "archive"
	}
	return freeName(dir, base+".zip")
}

// compressJob writes items (paths inside dir) into dir/name. It writes to a
// hidden partial file and renames it at the end, so a cancelled or failed
// job leaves nothing behind and never touches an existing file.
func compressJob(ctx context.Context, p *progress, dir string, items []string, name string) opResult {
	p.setVerb("Compressing")
	format, err := compressFormat(name)
	if err != nil {
		return opResult{err: err}
	}
	final := filepath.Join(dir, name)
	if exists(final) {
		return opResult{err: fmt.Errorf("%s already exists", name)}
	}
	p.counting.Store(true)
	for _, it := range items {
		b, f := measure(ctx, it)
		p.totalBytes.Add(b)
		p.totalFiles.Add(f)
	}
	p.counting.Store(false)

	tmp, err := os.CreateTemp(dir, "."+name+".partial-")
	if err != nil {
		return opResult{err: err}
	}
	fail := func(err error) opResult {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		if ctx.Err() != nil {
			return opResult{desc: "Stopped compressing; nothing was left behind"}
		}
		return opResult{err: err}
	}
	var werr error
	if format == "zip" {
		werr = writeZip(ctx, tmp, dir, items, p)
	} else {
		werr = writeTarGz(ctx, tmp, dir, items, p)
	}
	if werr != nil {
		return fail(werr)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fail(err)
	}
	// Link instead of rename: it fails if the name was taken meanwhile.
	if err := os.Link(tmp.Name(), final); err != nil {
		return fail(err)
	}
	_ = os.Remove(tmp.Name())
	return opResult{
		desc:  fmt.Sprintf("Compressed %s into %s", plural(len(items), "item"), name),
		focus: name,
		undo:  func() error { _, err := trashPath(final); return err },
	}
}

// walkItems visits every entry under each item with its archive name
// (relative to dir, slash-separated).
func walkItems(ctx context.Context, dir string, items []string, fn func(path, rel string, info os.FileInfo) error) error {
	for _, it := range items {
		err := filepath.Walk(it, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			return fn(p, filepath.ToSlash(rel), info)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func writeZip(ctx context.Context, w io.Writer, dir string, items []string, p *progress) error {
	zw := zip.NewWriter(w)
	err := walkItems(ctx, dir, items, func(path, rel string, info os.FileInfo) error {
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		switch {
		case info.IsDir():
			hdr.Name += "/"
			_, err := zw.CreateHeader(hdr)
			return err
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr.Method = zip.Store
			out, err := zw.CreateHeader(hdr)
			if err == nil {
				_, err = io.WriteString(out, target)
			}
			p.files.Add(1)
			return err
		case info.Mode().IsRegular():
			hdr.Method = zip.Deflate
			out, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			if err := copyFileInto(ctx, out, path, p); err != nil {
				return err
			}
			p.files.Add(1)
			return nil
		}
		return nil // sockets, devices: skipped
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func writeTarGz(ctx context.Context, w io.Writer, dir string, items []string, p *progress) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := walkItems(ctx, dir, items, func(path, rel string, info os.FileInfo) error {
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			t, err := os.Readlink(path)
			if err != nil {
				return err
			}
			link = t
		}
		if !info.IsDir() && !info.Mode().IsRegular() && link == "" {
			return nil // sockets, devices: skipped
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			if err := copyFileInto(ctx, tw, path, p); err != nil {
				return err
			}
		}
		if !info.IsDir() {
			p.files.Add(1)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func copyFileInto(ctx context.Context, out io.Writer, path string, p *progress) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return copyData(ctx, out, f, p)
}
