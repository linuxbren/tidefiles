package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the config, the trash (XDG_DATA_HOME) and the update
// check at throwaway places, so no test can read or change the user's real
// ones or reach GitHub.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tidefiles-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("TIDEFILES_CONFIG", filepath.Join(dir, "config.json"))
	os.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	os.Setenv("TIDEFILES_UPDATE_URL", "http://127.0.0.1:1/") // never ask GitHub
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
