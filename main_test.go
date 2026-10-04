package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the config at a throwaway file, so no test can change the
// user's real ~/.config/tidefiles/config.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tidefiles-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("TIDEFILES_CONFIG", filepath.Join(dir, "config.json"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
