package main

import (
	"strings"

	"github.com/r-thirumaran/mac-utility/shared/keymap"
)

// keyStroke is how a key name is injected on Windows: a Set-1 scan code
// (with the E0 prefix when Ext) through KEYEVENTF_SCANCODE, or, for the
// media and audio keys, a virtual-key code through wVk.
type keyStroke struct {
	Name string // canonical DOM code name
	Scan int
	Ext  bool
	VK   int
}

// Virtual-key codes of the media keys (winuser.h).
const (
	vkVolumeMute     = 0xAD
	vkVolumeDown     = 0xAE
	vkVolumeUp       = 0xAF
	vkMediaNextTrack = 0xB0
	vkMediaPrevTrack = 0xB1
	vkMediaStop      = 0xB2
	vkMediaPlayPause = 0xB3
	vkMenu           = 0x12 // ALT, for the SetForegroundWindow trick
)

// mediaVK maps the media/audio DOM names to virtual keys.
var mediaVK = map[string]int{
	"MediaPlayPause":     vkMediaPlayPause,
	"MediaTrackNext":     vkMediaNextTrack,
	"MediaTrackPrevious": vkMediaPrevTrack,
	"MediaStop":          vkMediaStop,
	"AudioVolumeUp":      vkVolumeUp,
	"AudioVolumeDown":    vkVolumeDown,
	"AudioVolumeMute":    vkVolumeMute,
}

// mediaKeyOf maps a `media` action to its key name.
var mediaKeyOf = map[string]string{
	"playPause":  "MediaPlayPause",
	"next":       "MediaTrackNext",
	"previous":   "MediaTrackPrevious",
	"volumeUp":   "AudioVolumeUp",
	"volumeDown": "AudioVolumeDown",
	"mute":       "AudioVolumeMute",
}

// keyAliases accepts the common short names next to the DOM codes.
var keyAliases = map[string]string{
	"ctrl": "ControlLeft", "control": "ControlLeft", "shift": "ShiftLeft", "alt": "AltLeft", "option": "AltLeft",
	"meta": "MetaLeft", "cmd": "MetaLeft", "command": "MetaLeft", "win": "MetaLeft", "super": "MetaLeft",
	"esc": "Escape", "return": "Enter", "del": "Delete", "ins": "Insert", "pgup": "PageUp", "pgdn": "PageDown", "pgdown": "PageDown",
	"up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight", " ": "Space", "spacebar": "Space",
	"back": "Backspace", "bksp": "Backspace", "menu": "ContextMenu", "capslock": "CapsLock",
}

// modKeys maps a `mods` entry to the key that holds it ("cmdOrCtrl" is Ctrl
// on Windows, docs/reach-api.md §2).
var modKeys = map[string]string{
	"ctrl": "ControlLeft", "shift": "ShiftLeft", "alt": "AltLeft", "meta": "MetaLeft", "cmdOrCtrl": "ControlLeft",
}

// resolveKey maps a key name to its stroke: DOM codes ("KeyA", "Enter",
// "ArrowLeft", "F5", "MetaLeft"), the media names, single printable
// characters ("a", "1", "/") and the aliases above.
func resolveKey(name string) (keyStroke, bool) {
	if name == "" {
		return keyStroke{}, false
	}
	if vk, ok := mediaVK[name]; ok {
		return keyStroke{Name: name, VK: vk}, true
	}
	km := keymap.Default()
	if k, ok := km.ByDom(name); ok && k.WinScan > 0 {
		return keyStroke{Name: name, Scan: k.WinScan, Ext: k.WinExt}, true
	}
	if alias, ok := keyAliases[strings.ToLower(name)]; ok {
		return resolveKey(alias)
	}
	if len(name) == 1 {
		if dom := domOfChar(name[0]); dom != "" {
			return resolveKey(dom)
		}
	}
	// Case-insensitive match against the DOM table ("enter", "arrowleft").
	lower := strings.ToLower(name)
	for _, k := range km.Keys() {
		if k.Dom != "" && strings.ToLower(k.Dom) == lower && k.WinScan > 0 {
			return keyStroke{Name: k.Dom, Scan: k.WinScan, Ext: k.WinExt}, true
		}
	}
	return keyStroke{}, false
}

// domOfChar returns the DOM code of the US-layout key that types c.
func domOfChar(c byte) string {
	switch {
	case c >= 'a' && c <= 'z':
		return "Key" + string(c-'a'+'A')
	case c >= 'A' && c <= 'Z':
		return "Key" + string(c)
	case c >= '0' && c <= '9':
		return "Digit" + string(c)
	}
	switch c {
	case ' ':
		return "Space"
	case '-', '_':
		return "Minus"
	case '=', '+':
		return "Equal"
	case '[', '{':
		return "BracketLeft"
	case ']', '}':
		return "BracketRight"
	case '\\', '|':
		return "Backslash"
	case ';', ':':
		return "Semicolon"
	case '\'', '"':
		return "Quote"
	case '`', '~':
		return "Backquote"
	case ',', '<':
		return "Comma"
	case '.', '>':
		return "Period"
	case '/', '?':
		return "Slash"
	case '\n':
		return "Enter"
	case '\t':
		return "Tab"
	}
	return ""
}

// resolveMods returns the strokes holding mods (unknown names are an error).
func resolveMods(mods []string) ([]keyStroke, bool) {
	out := make([]keyStroke, 0, len(mods))
	for _, m := range mods {
		dom, ok := modKeys[m]
		if !ok {
			return nil, false
		}
		k, ok := resolveKey(dom)
		if !ok {
			return nil, false
		}
		out = append(out, k)
	}
	return out, true
}
