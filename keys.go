package main

type action int

const (
	actNone action = iota
	actUp
	actDown
	actParent
	actOpen
	actTop
	actBottom
	actPageUp
	actPageDown
	actHalfUp
	actHalfDown
	actBack
	actForward
	actHome
	actGoto
	actPlaces
	actBookmark
	actSelect
	actSelectAll
	actInvert
	actEscape
	actCopy
	actCut
	actPaste
	actTrash
	actDelete
	actRename
	actNewFile
	actNewFolder
	actUndo
	actRestore
	actEmptyTrash
	actProps
	actOpenWith
	actTerminal
	actEdit
	actCopyPath
	actHidden
	actWrap
	actSort
	actSortRev
	actFilter
	actRefresh
	actTheme
	actScrollDown
	actScrollUp
	actHelp
	actQuit
)

// binding ties keys to an action and drives both dispatch and the help
// overlay, so what is documented is what is bound. Arrow keys come first, as
// in TideMail; vim-style letters remain as alternates.
type binding struct {
	act     action
	keys    []string
	display string // shown in help
	desc    string
}

type bindingGroup struct {
	title    string
	bindings []binding
}

var bindingGroups = []bindingGroup{
	{"Move", []binding{
		{actUp, []string{"up", "k"}, "↑ / ↓", "move  (also k / j)"},
		{actDown, []string{"down", "j"}, "", ""},
		{actParent, []string{"left", "backspace", "h", "alt+up"}, "←  backspace", "parent directory  (also h)"},
		{actOpen, []string{"right", "enter", "l"}, "→  enter", "open folder or file  (also l)"},
		{actBack, []string{"alt+left"}, "alt+← / alt+→", "history back / forward"},
		{actForward, []string{"alt+right"}, "", ""},
		{actTop, []string{"home", "g"}, "home / end", "first / last  (also g / G)"},
		{actBottom, []string{"end", "G"}, "", ""},
		{actPageUp, []string{"pgup", "ctrl+b"}, "pgup / pgdn", "page up / down"},
		{actPageDown, []string{"pgdown", "ctrl+f"}, "", ""},
		{actHalfUp, []string{"ctrl+u"}, "ctrl+u / ctrl+d", "half page up / down"},
		{actHalfDown, []string{"ctrl+d"}, "", ""},
	}},
	{"Go to", []binding{
		{actGoto, []string{"ctrl+l"}, "ctrl+l", "type a path"},
		{actPlaces, []string{"b"}, "b", "places and bookmarks"},
		{actBookmark, []string{"B"}, "B", "bookmark this folder"},
		{actHome, []string{"~"}, "~", "home folder"},
	}},
	{"Select", []binding{
		{actSelect, []string{" "}, "space", "select / unselect, move down"},
		{actSelectAll, []string{"A", "ctrl+a"}, "A", "select all"},
		{actInvert, []string{"*"}, "*", "invert selection"},
		{actEscape, []string{"esc"}, "esc", "clear selection / filter"},
	}},
	{"Files", []binding{
		{actCopy, []string{"c", "ctrl+y"}, "c", "copy"},
		{actCut, []string{"x", "ctrl+x"}, "x", "cut"},
		{actPaste, []string{"v", "ctrl+v"}, "v", "paste  (works with other apps)"},
		{actTrash, []string{"d", "delete"}, "d  delete", "move to trash"},
		{actDelete, []string{"D", "shift+delete"}, "D", "delete permanently"},
		{actRename, []string{"f2", "r"}, "F2  r", "rename"},
		{actNewFile, []string{"n"}, "n", "new file"},
		{actNewFolder, []string{"N"}, "N", "new folder"},
		{actUndo, []string{"ctrl+z", "u"}, "ctrl+z  u", "undo last change"},
		{actRestore, []string{"R"}, "R", "restore from trash"},
		{actEmptyTrash, []string{"E"}, "E", "empty trash"},
		{actProps, []string{"i"}, "i", "properties"},
		{actCopyPath, []string{"y"}, "y", "copy path as text"},
	}},
	{"Open", []binding{
		{actEdit, []string{"e"}, "e", "edit in $EDITOR"},
		{actOpenWith, []string{"o"}, "o", "open with…"},
		{actTerminal, []string{"t"}, "t", "terminal here"},
	}},
	{"View", []binding{
		{actHidden, []string{"."}, ".", "show / hide hidden files"},
		{actWrap, []string{"w"}, "w", "preview word wrap"},
		{actSort, []string{"s"}, "s", "cycle sort: name, size, modified, type"},
		{actSortRev, []string{"S"}, "S", "reverse sort"},
		{actFilter, []string{"/"}, "/", "filter this folder"},
		{actScrollDown, []string{"J"}, "J / K", "scroll preview"},
		{actScrollUp, []string{"K"}, "", ""},
		{actRefresh, []string{"ctrl+r"}, "ctrl+r", "refresh"},
		{actTheme, []string{"T"}, "T", "theme picker"},
	}},
	{"App", []binding{
		{actHelp, []string{"?"}, "?", "this help"},
		{actQuit, []string{"q", "ctrl+c"}, "q", "quit"},
	}},
}

var keyIndex = func() map[string]action {
	m := map[string]action{}
	for _, g := range bindingGroups {
		for _, b := range g.bindings {
			for _, k := range b.keys {
				m[k] = b.act
			}
		}
	}
	return m
}()

// statusHints is the always-visible shortcut strip. "? all keys" leads so it
// survives truncation on narrow terminals.
const statusHints = "? all keys  ↑↓←→ navigate  space select  c x v copy cut paste  d trash  F2 rename  / filter  q quit"
