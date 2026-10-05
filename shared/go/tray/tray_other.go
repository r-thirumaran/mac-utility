//go:build !windows

package tray

import (
	"log"
	"sync"
)

// Tray is the no-op tray used on non-Windows platforms. It records the
// tooltip and items so tests can inspect them. Methods are safe for
// concurrent use.
type Tray struct {
	mu      sync.Mutex
	tooltip string
	items   []Item
	quit    chan struct{}
	once    sync.Once
}

// Run calls onReady immediately and blocks until Quit.
func Run(cfg Config, onReady func(*Tray)) {
	t := &Tray{quit: make(chan struct{}), tooltip: cfg.Tooltip, items: append([]Item(nil), cfg.Items...)}
	if onReady != nil {
		onReady(t)
	}
	<-t.quit
}

// SetTooltip records the hover text.
func (t *Tray) SetTooltip(s string) {
	t.mu.Lock()
	t.tooltip = s
	t.mu.Unlock()
}

// Tooltip returns the recorded hover text.
func (t *Tray) Tooltip() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tooltip
}

// SetItems records the menu.
func (t *Tray) SetItems(items []Item) {
	t.mu.Lock()
	t.items = append([]Item(nil), items...)
	t.mu.Unlock()
}

// Items returns the menu as last set.
func (t *Tray) Items() []Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Item(nil), t.items...)
}

// Click invokes the OnClick of the first item with this title and reports
// whether one was found.
func (t *Tray) Click(title string) bool {
	t.mu.Lock()
	var f func()
	found := false
	for _, it := range t.items {
		if !it.Separator && it.Title == title {
			f, found = it.OnClick, true
			break
		}
	}
	t.mu.Unlock()
	if found && f != nil {
		go f()
	}
	return found
}

// Notify prints the notification to the log.
func (t *Tray) Notify(title, body string) {
	log.Printf("tray: notify: %s: %s", title, body)
}

// Quit makes Run return. It is idempotent.
func (t *Tray) Quit() {
	t.once.Do(func() { close(t.quit) })
}

// Done is closed once Quit was called.
func (t *Tray) Done() <-chan struct{} { return t.quit }
