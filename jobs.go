package main

// Background jobs: long file operations (paste, extract, compress) run off
// the UI goroutine with a progress bar in the status line and can be
// cancelled; a cancelled or failed job removes what it half-made.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const jobTickEvery = 250 * time.Millisecond

// progress is shared between a job's goroutine and the UI.
type progress struct {
	bytes, files           atomic.Int64
	totalBytes, totalFiles atomic.Int64
	counting               atomic.Bool // still measuring the work
	mu                     sync.Mutex
	verb                   string // "Copying", "Moving", … (set once the job knows)
	what                   string // "3 items"
}

func (p *progress) setVerb(v string) { p.mu.Lock(); p.verb = v; p.mu.Unlock() }
func (p *progress) getVerb() string  { p.mu.Lock(); defer p.mu.Unlock(); return p.verb }
func (p *progress) setWhat(w string) { p.mu.Lock(); p.what = w; p.mu.Unlock() }
func (p *progress) getWhat() string  { p.mu.Lock(); defer p.mu.Unlock(); return p.what }

// label is "Copying 3 items".
func (p *progress) label() string { return strings.TrimSpace(p.getVerb() + " " + p.getWhat()) }

type job struct {
	id     int
	cancel context.CancelFunc
	prog   *progress
	start  time.Time
}

type (
	jobDoneMsg struct {
		id  int
		res opResult
	}
	jobTickMsg struct{ id int }
)

var jobSeq int

// startJob runs fn in the background as the current job.
func (m *model) startJob(verb, what string, fn func(ctx context.Context, p *progress) opResult) tea.Cmd {
	jobSeq++
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{id: jobSeq, cancel: cancel, prog: &progress{verb: verb, what: what}, start: time.Now()}
	j.prog.counting.Store(true) // until the job has measured its work
	m.job = j
	m.setMsg("", false)
	return tea.Batch(func() tea.Msg { return jobDoneMsg{j.id, fn(ctx, j.prog)} }, jobTick(j.id))
}

func jobTick(id int) tea.Cmd {
	return tea.Tick(jobTickEvery, func(time.Time) tea.Msg { return jobTickMsg{id} })
}

// jobStatus is the status-line text for a running job.
func (j *job) status(now time.Time) string {
	p := j.prog
	head := p.label()
	if p.counting.Load() {
		return head + " · counting… · esc cancels"
	}
	done, total := p.bytes.Load(), p.totalBytes.Load()
	fdone, ftotal := p.files.Load(), p.totalFiles.Load()
	frac := 0.0
	switch {
	case total > 0:
		frac = float64(done) / float64(total)
	case ftotal > 0:
		frac = float64(fdone) / float64(ftotal)
	}
	frac = min(1, max(0, frac))
	s := fmt.Sprintf("%s %s %3.0f%%  %s / %s · %d/%d files", head, progressBar(frac, 14), frac*100,
		humanSize(done), humanSize(total), fdone, ftotal)
	if elapsed := now.Sub(j.start); frac > 0.02 && elapsed > time.Second {
		left := time.Duration(float64(elapsed) * (1 - frac) / frac)
		s += " · " + shortDuration(left) + " left"
	}
	return s + " · esc cancels"
}

func progressBar(frac float64, width int) string {
	full := int(frac*float64(width) + 0.5)
	return "▕" + strings.Repeat("█", full) + strings.Repeat("░", width-full) + "▏"
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// ---- measuring and copying with progress --------------------------------

// measure returns the bytes in regular files and the number of non-folder
// entries (files, links) under path.
func measure(ctx context.Context, path string) (bytes, files int64) {
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil || d.IsDir() {
			return nil
		}
		files++
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				bytes += fi.Size()
			}
		}
		return nil
	})
	return bytes, files
}

// copyData copies in to out in chunks, counting bytes and stopping when ctx
// is cancelled.
func copyData(ctx context.Context, out io.Writer, in io.Reader, p *progress) error {
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			if p != nil {
				p.bytes.Add(int64(n))
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// copyTreeCtx copies src to dst (which must not exist), recording progress.
func copyTreeCtx(ctx context.Context, src, dst string, p *progress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := copyTreeCtx(ctx, filepath.Join(src, de.Name()), filepath.Join(dst, de.Name()), p); err != nil {
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
		if err := copyData(ctx, out, in, p); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := os.Chtimes(dst, time.Now(), info.ModTime()); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s: unsupported file type", filepath.Base(src))
	}
	if p != nil {
		p.files.Add(1)
	}
	return nil
}

// ---- paste as a job -------------------------------------------------------

// pasteJob copies or moves items into dir. Name conflicts become
// "name (copy)", never overwrites. A cancelled copy removes everything it
// made; a cancelled move keeps the items already moved (their originals are
// gone) and leaves the rest untouched; either way the item in flight is
// removed from the destination, so nothing half-copied is left behind.
func pasteJob(ctx context.Context, p *progress, dir string, items []string, cut bool) opResult {
	verb, past := "Copying", "Pasted"
	if cut {
		verb, past = "Moving", "Moved"
	}
	p.setVerb(verb)
	type pair struct{ src, dst string }
	type plan struct {
		src          string
		bytes, files int64
	}
	var plans []plan
	p.counting.Store(true)
	for _, src := range items {
		if !exists(src) || (cut && filepath.Dir(src) == dir) {
			continue
		}
		if !cut || !sameDevice(src, dir) {
			b, f := measure(ctx, src)
			plans = append(plans, plan{src, b, f})
			p.totalBytes.Add(b)
			p.totalFiles.Add(f)
		} else {
			plans = append(plans, plan{src: src}) // a rename: instant, nothing to count
		}
	}
	p.counting.Store(false)
	p.setWhat(plural(len(plans), "item"))

	var done []pair
	var firstErr error
	stopped := false
	for _, pl := range plans {
		dst := uniqueDest(dir, filepath.Base(pl.src))
		var err error
		copied := false // dst is a complete copy of src
		if cut {
			err = os.Rename(pl.src, dst)
			if errors.Is(err, syscall.EXDEV) {
				if err = copyPathCtx(ctx, pl.src, dst, p); err == nil {
					copied = true
					err = os.RemoveAll(pl.src)
				}
			}
		} else {
			err = copyPathCtx(ctx, pl.src, dst, p)
		}
		if err != nil {
			if copied {
				// The copy is whole but removing the original failed part-way:
				// keep the copy, since some originals may already be gone.
				firstErr = fmt.Errorf("moved %s, but couldn't remove all of the original: %w", filepath.Base(pl.src), err)
				done = append(done, pair{pl.src, dst})
				break
			}
			_ = os.RemoveAll(dst) // the item in flight: never leave half of it
			if ctx.Err() != nil {
				stopped = true
			} else {
				firstErr = err
			}
			break
		}
		done = append(done, pair{pl.src, dst})
	}

	if stopped && !cut {
		var leftover []string
		for _, d := range done {
			if err := os.RemoveAll(d.dst); err != nil {
				leftover = append(leftover, filepath.Base(d.dst))
			}
		}
		res := opResult{desc: "Stopped copying; nothing was left behind"}
		if len(leftover) > 0 {
			res.err = fmt.Errorf("stopped copying, but couldn't remove %s", strings.Join(leftover, ", "))
		}
		return res
	}

	res := opResult{err: firstErr}
	if len(done) > 0 {
		res.focus = filepath.Base(done[0].dst)
		res.desc = fmt.Sprintf("%s %s", past, plural(len(done), "item"))
		if stopped {
			res.desc = fmt.Sprintf("Stopped moving: moved %d of %d items; the rest are untouched", len(done), len(plans))
		}
		res.undo = func() error {
			for _, pr := range done {
				if cut {
					back := pr.src
					if exists(back) {
						back = uniqueDest(filepath.Dir(pr.src), filepath.Base(pr.src))
					}
					if err := movePath(pr.dst, back); err != nil {
						return err
					}
				} else if _, err := trashPath(pr.dst); err != nil {
					return err
				}
			}
			return nil
		}
	} else if stopped {
		res.desc = "Stopped moving; nothing was moved"
	}
	return res
}

func copyPathCtx(ctx context.Context, src, dst string, p *progress) error {
	if dst == src || strings.HasPrefix(dst, src+string(filepath.Separator)) {
		return fmt.Errorf("cannot copy %s into itself", filepath.Base(src))
	}
	return copyTreeCtx(ctx, src, dst, p)
}

// sameDevice reports whether a and b are on the same filesystem (so a move
// between them is a rename).
func sameDevice(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Lstat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}

// stopNote says what stopping the job leaves behind.
func (j *job) stopNote() string {
	switch j.prog.getVerb() {
	case "Moving":
		return "Items already moved stay moved; the rest are untouched."
	case "Copying":
		return "Everything copied so far is removed."
	}
	return "Nothing half-done is left behind."
}
