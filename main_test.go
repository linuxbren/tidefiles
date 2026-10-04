package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the config and the trash (XDG_DATA_HOME) at throwaway
// places, so no test can read or change the user's real ones.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tidefiles-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("TIDEFILES_CONFIG", filepath.Join(dir, "config.json"))
	os.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
