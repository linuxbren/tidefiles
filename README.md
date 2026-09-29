# tidefiles

A keyboard-first terminal file manager in the [TideMail](https://github.com/allisonhere/tidemail)
family, built on [tideui](https://github.com/allisonhere/tideui) and Bubble Tea.

- **Three panes** — parent, current folder, and a preview (yazi-style). `shift+←/→` resizes the preview, `p` hides it.
- **A preview that understands your files** — real inline images (sixel in foot/wezterm/mlterm, kitty graphics in
  kitty/ghostty, half-block fallback anywhere else), PDF first pages, video thumbnails, syntax-highlighted code
  in your Omarchy colors, pretty-printed JSON, archive contents, audio tags, folder contents.
- **Word-wrapped preview** — long lines wrap at word boundaries; `w` toggles truncation.
- **Shortcut strip + help** — common keys are always in the status bar; `?` opens the full list.
- **Follows your Omarchy theme** — live, no restart. `T` opens a theme picker.
- **A modern file manager's toolbox** — select, copy / cut / paste (the system clipboard gets file lists
  for file managers and paths for terminals; `C` copies a picture itself for browsers and chat apps), trash with **undo**, rename, new file/folder, properties, open with…, places and
  bookmarks, back/forward history, filter, sort, terminal here, mouse support.
- **Safe by default** — paste never overwrites (conflicts become `name (copy)`), delete goes to the
  freedesktop trash, and permanent delete asks first.

Image previews use `imagemagick` (jpeg/svg/heic/…), `poppler` (PDF), `ffmpegthumbnailer` (video),
`libarchive` (archives) and `ffmpeg` (audio tags) when installed. Set `TIDEFILES_IMAGES=blocks|sixel|kitty`
to override terminal detection.

## Run

```sh
go run . [dir]
go run . --cwd-file /tmp/tidefiles.cwd   # write final dir on quit, for a cd-on-exit shell wrapper
```

Settings (theme, hidden files, wrap, sort, bookmarks) are remembered in
`~/.config/tidefiles/config.json`.

## Keys

Arrow keys first; vim keys work too. Press `?` in the app for the full list.
`space` select · `c`/`x`/`v` copy/cut/paste · `d` trash · `ctrl+z` undo · `F2` rename ·
`n`/`N` new file/folder · `/` filter · `s` sort · `b` places · `i` properties · `o` open with · `T` theme.
