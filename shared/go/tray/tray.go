// Package tray shows a notification-area icon with a menu. On Windows it wraps
// github.com/getlantern/systray; on every other platform (Linux is a test
// target, never a product) it is a no-op that records what it was asked to do
// so app code paths run unchanged.
//
// Run must be called from the main goroutine on Windows (systray locks the OS
// thread at init) and blocks until Quit is called; everything the app needs
// to do after the icon exists goes in onReady.
//
// Menu structure (Windows): systray cannot remove or reorder items, so
// SetItems reuses a pool of menu items in order. Titles, checked and disabled
// states can change freely; items beyond the current list are hidden; a longer
// list appends new items at the end; separators are fixed where the first
// SetItems placed them. Design the full menu once in Config.Items and use
// SetItems for state changes.
package tray

// Item is one menu entry.
type Item struct {
	// Title is the label. Ignored for separators.
	Title string
	// OnClick runs (on its own goroutine) when the item is clicked.
	OnClick func()
	// Checked shows a check mark.
	Checked bool
	// Disabled greys the item out.
	Disabled bool
	// Separator makes this entry a separator line.
	Separator bool
}

// Config describes the icon and the initial menu.
type Config struct {
	// Tooltip is shown when hovering the icon.
	Tooltip string
	// IconICO is the icon in .ico format (Windows). nil keeps the default icon.
	IconICO []byte
	// Items is the initial menu.
	Items []Item
}
