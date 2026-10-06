package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestProtoFromReply(t *testing.T) {
	cases := map[string]gfxProto{
		"\x1b_Gi=31;OK\x1b\\\x1b[?62;c":          protoKitty,
		"\x1b[?62;4;9;22c":                       protoSixel,
		"\x1b[?1;2c":                             protoBlocks,
		"":                                       protoBlocks,
		"\x1b_Gi=31;ENOTSUPPORTED\x1b\\\x1b[?6c": protoBlocks,
	}
	for in, want := range cases {
		if got := protoFromReply(in); got != want {
			t.Errorf("protoFromReply(%q) = %v, want %v", in, got, want)
		}
	}
}

// openPty returns the two ends of a new pseudo-terminal.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlock pty: %v", err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("pty number: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open pty: %v", err)
	}
	t.Cleanup(func() { m.Close(); s.Close() })
	return m, s
}

// fakeTerminal answers the probe's queries after delay, like a terminal that
// is still starting up; an empty reply means it never answers.
func fakeTerminal(master *os.File, delay time.Duration, reply string) {
	go func() {
		buf := make([]byte, 256)
		var got []byte
		for !strings.Contains(string(got), "\x1b[c") {
			n, err := master.Read(buf)
			if err != nil {
				return
			}
			got = append(got, buf[:n]...)
		}
		if reply == "" {
			return
		}
		time.Sleep(delay)
		master.WriteString(reply)
	}()
}

func TestProbeWaitsForASlowTerminal(t *testing.T) {
	const sixelDA1 = "\x1b[?62;4;22c" // foot's answer: 4 = sixel
	m, s := openPty(t)
	fakeTerminal(m, 600*time.Millisecond, sixelDA1)
	if got := probeTerminalOn(s, s, probeTimeout); got != protoSixel {
		t.Fatalf("slow foot: %v, want sixel", got)
	}

	m, s = openPty(t)
	fakeTerminal(m, 600*time.Millisecond, sixelDA1)
	if got := probeTerminalOn(s, s, 250*time.Millisecond); got != protoBlocks {
		t.Fatalf("the old 250ms limit should have given up: %v", got)
	}

	m, s = openPty(t)
	fakeTerminal(m, 50*time.Millisecond, "\x1b_Gi=31;OK\x1b\\\x1b[?62;22c")
	if got := probeTerminalOn(s, s, probeTimeout); got != protoKitty {
		t.Fatalf("kitty: %v", got)
	}

	m, s = openPty(t)
	fakeTerminal(m, 0, "")
	start := time.Now()
	if got := probeTerminalOn(s, s, 200*time.Millisecond); got != protoBlocks {
		t.Fatalf("no answer: %v", got)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the wait should end at the timeout")
	}
}
