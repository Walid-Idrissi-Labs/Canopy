package main

import tea "charm.land/bubbletea/v2"

// keyText is a key press that types s, as Bubble Tea v2 reports one.
func keyText(s string) tea.KeyPressMsg {
	r := []rune(s)
	if len(r) == 0 {
		return tea.KeyPressMsg{}
	}
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

// keyCode is a press of a named key, with any modifiers.
func keyCode(code rune, mods ...tea.KeyMod) tea.KeyPressMsg {
	var mod tea.KeyMod
	for _, m := range mods {
		mod |= m
	}
	return tea.KeyPressMsg{Code: code, Mod: mod}
}
