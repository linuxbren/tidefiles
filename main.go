// tidefiles is a keyboard-first terminal file manager in the TideMail family:
// three panes, a word-wrapped preview, an always-visible shortcut strip, and a
// theme that follows the active Omarchy desktop theme.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	themeMode := flag.String("theme", themeOmarchy, `theme: "omarchy" (follow the desktop) or a tideui palette name`)
	cwdFile := flag.String("cwd-file", "", "write the final directory to this file on exit (for shell cd-on-quit)")
	flag.Parse()

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		if st, serr := os.Stat(abs); serr != nil || !st.IsDir() {
			err = fmt.Errorf("%s is not a directory", dir)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tidefiles:", err)
		os.Exit(1)
	}

	if _, err := tea.NewProgram(newModel(abs, *themeMode, *cwdFile), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tidefiles:", err)
		os.Exit(1)
	}
}
