package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOctalAndSymbolic(t *testing.T) {
	cases := []struct {
		octal, sym string
	}{
		{"755", "rwxr-xr-x"},
		{"644", "rw-r--r--"},
		{"000", "---------"},
		{"4755", "rwsr-xr-x"},
		{"2644", "rw-r-Sr--"},
		{"1777", "rwxrwxrwt"},
	}
	for _, c := range cases {
		mode, err := parseOctalMode(c.octal)
		if err != nil {
			t.Fatalf("%s: %v", c.octal, err)
		}
		if got := symbolicMode(mode); got != c.sym {
			t.Errorf("%s → %s, want %s", c.octal, got, c.sym)
		}
		if got := octalOf(mode); got != c.octal {
			t.Errorf("%s round-trips to %s", c.octal, got)
		}
	}
	for _, bad := range []string{"8", "19999", "abc"} {
		if _, err := parseOctalMode(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestPermEditOctalEntry(t *testing.T) {
	p := permEdit{mode: 0o644 | os.ModeSetgid}
	for _, d := range "75" {
		p.typeDigit(d)
	}
	if p.touched != 0 {
		t.Fatal("two digits must not apply yet")
	}
	p.typeDigit('0')
	if p.mode != 0o750|os.ModeSetgid || p.touched != permBits {
		t.Fatalf("750 on setgid file: mode %o touched %o", p.mode, p.touched)
	}
	p.typeDigit('0') // a fourth digit extends the entry to four-digit octal
	if p.octal != "7500" {
		t.Fatalf("octal buffer %q", p.octal)
	}
	p = permEdit{mode: 0o644 | os.ModeSetgid}
	for _, d := range "0755" {
		p.typeDigit(d)
	}
	if p.mode != 0o755 {
		t.Fatalf("0755 should clear setgid, got %v", p.mode)
	}
	p.backspace()
	if p.octal != "075" {
		t.Fatalf("backspace: %q", p.octal)
	}
}

func TestPermEditMixedTargets(t *testing.T) {
	a := permTarget{"a", 0o644}
	b := permTarget{"b", 0o600}
	p := permEdit{targets: []permTarget{a, b}, mode: a.mode, mixed: a.mode ^ b.mode}
	p.toggle(permBit(0, 2)) // owner execute on
	if got := p.result(a); got != 0o744 {
		t.Errorf("a: %o", got)
	}
	if got := p.result(b); got != 0o700 {
		t.Errorf("b keeps its group/other bits: %o", got)
	}
	p.toggle(permBit(1, 0)) // group read differs (a on, b off): turns on for both
	if p.result(a)&permBit(1, 0) == 0 || p.result(b)&permBit(1, 0) == 0 {
		t.Errorf("mixed bit should turn on: a %o b %o", p.result(a), p.result(b))
	}
}

func TestChmodOpAndUndo(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	mkfile(t, a, "")
	mkfile(t, b, "")
	os.Chmod(a, 0o644)
	os.Chmod(b, 0o600)
	ts := []permTarget{{a, 0o644}, {b, 0o600}}
	res := chmodOp(ts, func(permTarget) os.FileMode { return 0o755 })
	if res.err != nil || res.undo == nil {
		t.Fatalf("chmod: %+v", res)
	}
	for _, p := range []string{a, b} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o755 {
			t.Fatalf("%s is %v", p, fi.Mode())
		}
	}
	if err := res.undo(); err != nil {
		t.Fatal(err)
	}
	if fa, _ := os.Stat(a); fa.Mode().Perm() != 0o644 {
		t.Fatalf("undo a: %v", fa.Mode())
	}
	if fb, _ := os.Stat(b); fb.Mode().Perm() != 0o600 {
		t.Fatalf("undo b: %v", fb.Mode())
	}
	missing := chmodOp([]permTarget{{filepath.Join(dir, "gone"), 0o644}}, func(permTarget) os.FileMode { return 0o600 })
	if missing.err == nil || missing.undo != nil {
		t.Fatalf("missing file should fail without undo: %+v", missing)
	}
}
