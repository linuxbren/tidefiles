# tidefiles

A keyboard-first terminal file manager in the [TideMail](https://github.com/allisonhere/tidemail)
family, built on [tideui](https://github.com/allisonhere/tideui) and Bubble Tea.

- **Three panes** — parent, current folder, and a preview (yazi-style). `shift+←/→` resizes the preview, `p` hides it.
- **A preview that understands your files** — real inline images (sixel in foot/wezterm/mlterm, kitty graphics in
  kitty/ghostty, half-block fallback anywhere else), PDF first pages, video thumbnails, syntax-highlighted code
  in your Omarchy colors, rendered markdown (`m` shows the source), pretty-printed JSON, archive contents, audio tags, folder contents.
- **Word-wrapped preview** — long lines wrap at word boundaries; `w` toggles truncation.
- **Shortcut strip + help** — common keys are always in the status bar; `?` opens the full list.
- **Follows your Omarchy theme** — live, no restart. `T` opens a theme picker.
- **A modern file manager's toolbox** — select, copy / cut / paste (the system clipboard gets file lists
  for file managers and paths for terminals and editors), trash with **undo**, rename, new file/folder, properties, permissions (chmod), open with…, places and
  bookmarks, back/forward history, filter, fuzzy find across subfolders, search inside files (ripgrep when installed; `alt+r` for regex), sort, terminal here, mouse support.
- **Archives** — `enter` on a zip/tar/7z/rar browses it like a read-only folder (`←` leaves);
  `X` extracts archives here (into a folder named after the archive, unless it has a single
  top-level folder); `Z` compresses the selection into a `.zip` or `.tar.gz`. Reading uses `bsdtar`
  (libarchive), with `7z` as a fallback.
- **Progress you can stop** — copies, moves, extracts and compression run in the background with a
  progress bar, sizes and an ETA in the status line; `esc` stops one, removing whatever it half-made.
- **Safe by default** — paste never overwrites (conflicts become `name (copy)`), delete goes to the
  freedesktop trash, and permanent delete asks first.

Image previews use `imagemagick` (jpeg/svg/heic/…), `poppler` (PDF), `ffmpegthumbnailer` (video),
`libarchive` (archives) and `ffmpeg` (audio tags) when installed. Set `TIDEFILES_IMAGES=blocks|sixel|kitty`
to override terminal detection.

## Install

tidefiles runs on **Linux only** (it's built and tested on Omarchy: Hyprland + foot). It needs
Go 1.27 or newer to build:

```sh
go install github.com/linuxbren/tidefiles@latest
tidefiles --version
```

The binary lands in `$(go env GOPATH)/bin` (usually `~/go/bin`); make sure that's on your `PATH`.
The system clipboard needs a Wayland session and `wl-clipboard`.

**Optional — `SUPER+C` on Omarchy.** `SUPER+V` and `SUPER+X` work out of the box, but foot keeps
the key Omarchy's `SUPER+C` sends to terminals for itself. To make `SUPER+C` copy in tidefiles,
append [`contrib/omarchy-super-c.lua`](contrib/omarchy-super-c.lua) to `~/.config/hypr/bindings.lua`
(Hyprland reloads on save; check with `hyprctl configerrors`). It only affects windows titled
`tidefiles — …`; every other window keeps Omarchy's default. Without it, plain `c` copies.

## Run

```sh
tidefiles [dir]
tidefiles --cwd-file /tmp/tidefiles.cwd   # write final dir on quit, for a cd-on-exit shell wrapper
```

From a checkout, `go run . [dir]` works the same way.

Settings (theme, hidden files, wrap, sort, bookmarks) are remembered in
`~/.config/tidefiles/config.json`.

## Keys

Arrow keys first; vim keys work too. Press `?` in the app for the full list.
`space` select · `SUPER+C`/`X`/`V` copy/cut/paste (plain `c`/`x`/`v` work too) · `d` trash · `ctrl+z` undo · `F2` rename ·
`n`/`N` new file/folder · `/` filter · `f` find · `F` search inside files · `X` extract · `Z` compress · `s` sort · `b` places · `i` properties · `P` permissions · `o` open with · `T` theme.

`SUPER+C` needs a one-time Hyprland snippet on Omarchy — see [Install](#install).
