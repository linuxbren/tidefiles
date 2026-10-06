package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.7.1", "v0.7.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.7.0", "v0.7.0", false},
		{"v0.6.9", "v0.7.0", false},
		{"v0.8.0", "(devel)", false},
		{"v0.8.0", "v0.7.0-0.20261006041954-5f76a2df6047", false},
		{"nightly", "v0.7.0", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestDetectInstall(t *testing.T) {
	writable := t.TempDir()
	for _, c := range []struct {
		name, exe, ld, bi string
		want              installKind
	}{
		{"release in ~/.local/bin", filepath.Join(writable, "tidefiles"), "v0.7.0", "", installSelf},
		{"package in /usr/bin", "/usr/bin/tidefiles", "v0.7.0", "", installPackage},
		{"root-owned /usr/local/bin", "/usr/local/bin/tidefiles", "v0.7.0", "", installPackage},
		{"nix", "/nix/store/abc-tidefiles-0.7.0/bin/tidefiles", "v0.7.0", "", installNix},
		{"go install", filepath.Join(writable, "tidefiles"), "", "v0.7.0", installGo},
		{"local build", filepath.Join(writable, "tidefiles"), "", "v0.7.1-0.20261006041954-5f76a2df6047", installDev},
		{"go run", filepath.Join(writable, "tidefiles"), "", "(devel)", installDev},
	} {
		got, hint := detectInstall(c.exe, c.ld, c.bi)
		if got != c.want {
			t.Errorf("%s: kind %v, want %v", c.name, got, c.want)
		}
		if (got == installPackage || got == installNix || got == installGo) && hint == "" {
			t.Errorf("%s: no hint for updating", c.name)
		}
	}
}

// fakeRelease serves a GitHub "latest release" whose archive holds a
// tidefiles that prints tag; tamper corrupts the archive after its checksum.
func fakeRelease(t *testing.T, tag string, tamper bool) {
	t.Helper()
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	bin := []byte("#!/bin/sh\necho 'tidefiles " + tag + "'\n")
	for _, f := range []struct {
		name string
		body []byte
	}{{"README.md", []byte("readme")}, {"tidefiles", bin}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		tw.Write(f.body)
	}
	tw.Close()
	gz.Close()
	name := "tidefiles_linux_" + runtime.GOARCH + ".tar.gz"
	sum := sha256.Sum256(tgz.Bytes())
	archive := tgz.Bytes()
	if tamper {
		archive = append([]byte{}, archive...)
		archive[len(archive)/2] ^= 0xff
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": []map[string]string{
				{"name": name, "browser_download_url": srv.URL + "/" + name},
				{"name": "checksums.txt", "browser_download_url": srv.URL + "/checksums.txt"},
			}})
		case "/" + name:
			w.Write(archive)
		case "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n%s  other.deb\n", hex.EncodeToString(sum[:]), name, strings.Repeat("0", 64))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TIDEFILES_UPDATE_URL", srv.URL+"/latest")
}

// oldBinary writes a stand-in tidefiles v0.7.0 and makes it the running
// version.
func oldBinary(t *testing.T) string {
	t.Helper()
	old := version
	version = "v0.7.0"
	t.Cleanup(func() { version = old })
	exe := filepath.Join(t.TempDir(), "tidefiles")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho 'tidefiles v0.7.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestInstallRelease(t *testing.T) {
	fakeRelease(t, "v9.9.9", false)
	exe := oldBinary(t)
	rel, err := fetchRelease()
	if err != nil {
		t.Fatal(err)
	}
	if err := installRelease(rel, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "v9.9.9") {
		t.Fatalf("binary not replaced: %s", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".tidefiles-update-*")); len(left) > 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestInstallReleaseRefusesABadDownload(t *testing.T) {
	fakeRelease(t, "v9.9.9", true)
	exe := oldBinary(t)
	rel, err := fetchRelease()
	if err != nil {
		t.Fatal(err)
	}
	if err := installRelease(rel, exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want a checksum error, got %v", err)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "v0.7.0") {
		t.Fatal("the old binary must be left alone")
	}
}

func TestAutomaticUpdateAsksToRestart(t *testing.T) {
	fakeRelease(t, "v99.0.0", false)
	m := testModel(t, t.TempDir())
	m.upd = updateState{kind: installSelf, exe: oldBinary(t)}

	msg := checkUpdate(false)()
	m2, cmd := m.Update(msg)
	m = m2.(model)
	if cmd == nil || !m.upd.busy {
		t.Fatal("automatic mode should start installing")
	}
	if time.Since(time.Unix(m.cfg.UpdateChecked, 0)) > time.Minute {
		t.Fatal("the check time should be recorded")
	}
	m2, _ = m.Update(cmd())
	m = m2.(model)
	if m.modal == nil || m.modal.purpose != "restart" || m.upd.ready != "v99.0.0" {
		t.Fatalf("want the restart question, got modal %+v, upd %+v", m.modal, m.upd)
	}
	if b, _ := os.ReadFile(m.upd.exe); !strings.Contains(string(b), "v99.0.0") {
		t.Fatal("binary not replaced")
	}

	no := send(m, "n")
	if no.restart || no.modal != nil {
		t.Fatal("n should keep working")
	}
	if again := send(no, "U"); again.modal == nil || again.modal.purpose != "restart" {
		t.Fatal("U should offer the restart again")
	}
	if yes := send(m, "y"); !yes.restart {
		t.Fatal("y should restart")
	}
}

func TestNotifyAndPackagedInstallsDontReplaceThemselves(t *testing.T) {
	fakeRelease(t, "v99.0.0", false)
	m := testModel(t, t.TempDir())
	m.cfg.Updates = updatesNotify
	m.upd = updateState{kind: installSelf, exe: oldBinary(t)}
	m2, cmd := m.Update(checkUpdate(false)())
	m = m2.(model)
	if cmd != nil || !strings.Contains(m.msg, "U installs it") {
		t.Fatalf("notify mode: cmd %v, msg %q", cmd != nil, m.msg)
	}

	p := testModel(t, t.TempDir())
	p.upd = updateState{kind: installPackage, hint: "install the new package"}
	p2, cmd := p.Update(checkUpdate(false)())
	p = p2.(model)
	if cmd != nil || !strings.Contains(p.msg, "install the new package") {
		t.Fatalf("packaged: cmd %v, msg %q", cmd != nil, p.msg)
	}
}

func TestUpdateDue(t *testing.T) {
	m := testModel(t, t.TempDir())
	m.upd.kind = installSelf
	now := time.Now()
	m.cfg.UpdateChecked = now.Add(-25 * time.Hour).Unix()
	if !m.updateDue(now) {
		t.Fatal("a day old: due")
	}
	m.cfg.UpdateChecked = now.Add(-time.Hour).Unix()
	if m.updateDue(now) {
		t.Fatal("an hour old: not due")
	}
	m.cfg.UpdateChecked = 0
	m.cfg.Updates = updatesOff
	if m.updateDue(now) {
		t.Fatal("off: never due")
	}
	m.cfg.Updates = updatesAuto
	m.upd.kind = installDev
	if m.updateDue(now) {
		t.Fatal("development builds never check")
	}
}

// send delivers a key through Update, as the program does (modals included).
func send(m model, key string) model {
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return m2.(model)
}
