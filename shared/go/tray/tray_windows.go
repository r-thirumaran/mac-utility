//go:build windows

package tray

import (
	"html"
	"strings"
	"sync"

	"github.com/getlantern/systray"

	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
)

// Tray is the running tray icon. Methods are safe for concurrent use.
type Tray struct {
	mu      sync.Mutex
	pool    []*poolEntry
	items   []Item
	tooltip string
	quit    chan struct{}
	once    sync.Once
}

type poolEntry struct {
	mi      *systray.MenuItem // nil for a separator
	onClick func()
}

// Run creates the icon and menu, calls onReady with the Tray and blocks until
// Quit. It must run on the main goroutine.
func Run(cfg Config, onReady func(*Tray)) {
	t := &Tray{quit: make(chan struct{}), tooltip: cfg.Tooltip}
	systray.Run(func() {
		if len(cfg.IconICO) > 0 {
			systray.SetIcon(cfg.IconICO)
		}
		if cfg.Tooltip != "" {
			systray.SetTooltip(cfg.Tooltip)
		}
		t.SetItems(cfg.Items)
		if onReady != nil {
			onReady(t)
		}
	}, func() {
		t.once.Do(func() { close(t.quit) })
	})
}

// SetTooltip changes the hover text.
func (t *Tray) SetTooltip(s string) {
	t.mu.Lock()
	t.tooltip = s
	t.mu.Unlock()
	systray.SetTooltip(s)
}

// Tooltip returns the current hover text.
func (t *Tray) Tooltip() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tooltip
}

// SetItems replaces the menu (see the package comment for the structural
// limits on Windows).
func (t *Tray) SetItems(items []Item) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items = append([]Item(nil), items...)
	pi := 0
	for _, it := range items {
		if it.Separator {
			if pi < len(t.pool) && t.pool[pi].mi == nil {
				pi++ // existing separator at this position
			} else if pi >= len(t.pool) {
				systray.AddSeparator()
				t.pool = append(t.pool, &poolEntry{})
				pi++
			}
			// else: a menu item sits here; the separator cannot be inserted.
			continue
		}
		for pi < len(t.pool) && t.pool[pi].mi == nil {
			pi++ // skip a fixed separator the new list does not have here
		}
		if pi >= len(t.pool) {
			e := &poolEntry{mi: systray.AddMenuItem(it.Title, it.Title)}
			t.pool = append(t.pool, e)
			go t.clickLoop(e)
		}
		e := t.pool[pi]
		e.onClick = it.OnClick
		e.mi.SetTitle(it.Title)
		if it.Disabled {
			e.mi.Disable()
		} else {
			e.mi.Enable()
		}
		if it.Checked {
			e.mi.Check()
		} else {
			e.mi.Uncheck()
		}
		e.mi.Show()
		pi++
	}
	for ; pi < len(t.pool); pi++ {
		if e := t.pool[pi]; e.mi != nil {
			e.onClick = nil
			e.mi.Hide()
		}
	}
}

func (t *Tray) clickLoop(e *poolEntry) {
	for range e.mi.ClickedCh {
		t.mu.Lock()
		f := e.onClick
		t.mu.Unlock()
		if f != nil {
			go f()
		}
	}
}

// Items returns the menu as last set.
func (t *Tray) Items() []Item {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Item(nil), t.items...)
}

// Click invokes the OnClick of the first item with this title and reports
// whether one was found (used by tests and for keyboard shortcuts).
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

// Notify shows a toast notification. It is best effort: it runs a hidden
// PowerShell that raises a WinRT toast (attributed to PowerShell, since an
// unpackaged exe has no registered AppUserModelID) and ignores failures.
func (t *Tray) Notify(title, body string) {
	script := strings.NewReplacer("TITLE", psQuote(title), "BODY", psQuote(body)).Replace(toastScript)
	go func() {
		_, _ = winutil.RunHidden("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	}()
}

func psQuote(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "'", "''")
}

const toastScript = `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null;` +
	`[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null;` +
	`$x = New-Object Windows.Data.Xml.Dom.XmlDocument;` +
	`$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text>TITLE</text><text>BODY</text></binding></visual></toast>');` +
	`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show([Windows.UI.Notifications.ToastNotification]::new($x))`

// Quit removes the icon and makes Run return.
func (t *Tray) Quit() {
	systray.Quit()
}

// Done is closed once Run has returned.
func (t *Tray) Done() <-chan struct{} { return t.quit }
