//go:build windows

package main

import (
	"sync"
	"testing"
)

// TestClipboardRoundTrip sets and reads the real clipboard, also from
// several goroutines at once: each operation must open and close the
// clipboard on one OS thread, or it stays open for every process.
func TestClipboardRoundTrip(t *testing.T) {
	c := newClipboard()
	const want = "Edgehop clipboard ✓ 测试 \U0001F600"
	c.SetText(want)
	got, ok := c.Text()
	if !ok || got != want {
		t.Fatalf("clipboard = %q ok=%v, want %q", got, ok, want)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c.SetText(want)
				if s, ok := c.Text(); ok && s != want {
					t.Errorf("clipboard = %q", s)
				}
			}
		}()
	}
	wg.Wait()
	if got, ok := c.Text(); !ok || got != want {
		t.Fatalf("after the burst: %q ok=%v", got, ok)
	}
}
