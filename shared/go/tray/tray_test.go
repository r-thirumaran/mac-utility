//go:build !windows

package tray

import (
	"testing"
	"time"
)

func TestRunQuit(t *testing.T) {
	clicked := make(chan string, 2)
	cfg := Config{Tooltip: "Vitals", Items: []Item{
		{Title: "Open", OnClick: func() { clicked <- "open" }},
		{Separator: true},
		{Title: "Quit", OnClick: func() { clicked <- "quit" }},
	}}
	done := make(chan struct{})
	var tr *Tray
	ready := make(chan struct{})
	go func() {
		Run(cfg, func(x *Tray) { tr = x; close(ready) })
		close(done)
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("onReady not called")
	}
	if tr.Tooltip() != "Vitals" || len(tr.Items()) != 3 {
		t.Fatalf("state %q %d", tr.Tooltip(), len(tr.Items()))
	}
	if !tr.Click("Open") {
		t.Fatal("Click(Open) not found")
	}
	if got := <-clicked; got != "open" {
		t.Fatalf("clicked %q", got)
	}
	if tr.Click("Nope") {
		t.Fatal("Click on a missing title returned true")
	}
	tr.SetTooltip("Vitals 42%")
	tr.SetItems([]Item{{Title: "Only"}})
	if tr.Tooltip() != "Vitals 42%" || len(tr.Items()) != 1 || tr.Items()[0].Title != "Only" {
		t.Fatalf("after set: %q %+v", tr.Tooltip(), tr.Items())
	}
	tr.Notify("hello", "world")
	select {
	case <-done:
		t.Fatal("Run returned before Quit")
	default:
	}
	tr.Quit()
	tr.Quit()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Quit")
	}
	<-tr.Done()
}
