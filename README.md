# tidefiles

A keyboard-first terminal file manager in the [TideMail](https://github.com/allisonhere/tidemail)
family, built on [tideui](https://github.com/allisonhere/tideui) and Bubble Tea.

- **Three panes** — parent, current directory, and a preview (yazi-style).
- **Word-wrapped preview** — long lines wrap at word boundaries; `w` toggles to truncation.
- **Shortcut strip + help** — common keys are always shown in the status bar; `?` opens the full list.
- **Follows your Omarchy theme** — live, no restart when you switch themes. Use `--theme <name>` for a fixed tideui palette.

## Run

```sh
go run . [dir]
go run . --cwd-file /tmp/tidefiles.cwd   # write final dir on quit, for a cd-on-exit shell wrapper
```

## Keys

Press `?` in the app. Highlights: `h j k l` move, `enter` open, `e` edit, `.` hidden files,
`w` wrap, `J`/`K` scroll preview, `q` quit.
