-- Omarchy / Hyprland: make SUPER+C copy files in tidefiles.
--
-- Omarchy's "Universal copy" sends Ctrl+Insert to terminal windows, and foot
-- keeps that key for its own clipboard-copy, so tidefiles never sees SUPER+C.
-- Append this file to ~/.config/hypr/bindings.lua (Hyprland reloads it on save;
-- check with `hyprctl configerrors`). It only changes terminals titled
-- "tidefiles — …", which tidefiles sets itself.
--
-- SUPER+V and SUPER+X need nothing: they already reach tidefiles.

-- tidefiles: make Super+C copy files when tidefiles is focused.
-- Omarchy's "Universal copy" sends Ctrl+Insert to terminal windows, which foot
-- keeps for its own clipboard-copy, so tidefiles never sees it. A terminal whose
-- title starts with "tidefiles — " (set by tidefiles itself) gets ctrl+y, its
-- copy key, instead. Every other window behaves exactly like Omarchy's default
-- (/usr/share/omarchy/default/hypr/bindings/clipboard.lua).
local function tidefiles_send_once(mods, key)
  hl.dispatch(hl.dsp.send_key_state({ mods = mods, key = key, state = "down" }))
  hl.timer(function()
    hl.dispatch(hl.dsp.send_key_state({ mods = mods, key = key, state = "up" }))
  end, { timeout = 50, type = "oneshot" })
end

local function tidefiles_is_terminal(window)
  for _, tag in ipairs(window.tags or {}) do
    if tag:gsub("%*$", "") == "terminal" then
      return true
    end
  end
  return false
end

-- Global so it can be exercised with:
--   hyprctl dispatch '(tidefiles_universal_copy() or hl.dsp.exec_cmd("true"))'
function tidefiles_universal_copy()
  local window = hl.get_active_window()
  if window and tidefiles_is_terminal(window) then
    if (window.title or ""):find("tidefiles — ", 1, true) == 1 then
      tidefiles_send_once("CTRL", "Y")
    else
      tidefiles_send_once("CTRL", "Insert")
    end
  else
    tidefiles_send_once("CTRL", "C")
  end
end

-- Was: Omarchy "Universal copy" (Ctrl+Insert in terminals, Ctrl+C elsewhere).
hl.unbind("SUPER + C")
o.bind("SUPER + C", "Universal copy", tidefiles_universal_copy)
