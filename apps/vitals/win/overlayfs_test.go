package main

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/r-thirumaran/mac-utility/apps/vitals/ui"
	sharedui "github.com/r-thirumaran/mac-utility/shared/ui"
)

func TestOverlayFS(t *testing.T) {
	base := fstest.MapFS{
		"index.html":   {Data: []byte("<!doctype html>")},
		"app.js":       {Data: []byte("export {};")},
		"shared":       {Data: []byte("shadowed by the mount")},
		"img/icon.png": {Data: []byte{1, 2, 3}},
	}
	kit := fstest.MapFS{
		"base.css":  {Data: []byte("body{}")},
		"native.js": {Data: []byte("export const native = {};")},
	}
	o := newOverlayFS(base, map[string]fs.FS{"shared/": kit})

	if err := fstest.TestFS(o, "index.html", "app.js", "img/icon.png", "shared/base.css", "shared/native.js"); err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(o, "shared/base.css")
	if err != nil || string(got) != "body{}" {
		t.Fatalf("shared/base.css = %q, %v", got, err)
	}
	if _, err := o.Open("shared/missing.css"); err == nil {
		t.Fatal("expected an error for a missing kit file")
	}
	if _, err := o.Open("../etc/passwd"); err == nil {
		t.Fatal("expected an error for an invalid path")
	}
	// The mount shadows the base entry of the same name.
	st, err := fs.Stat(o, "shared")
	if err != nil || !st.IsDir() || st.Name() != "shared" {
		t.Fatalf("stat shared = %v, %v", st, err)
	}
	entries, err := fs.ReadDir(o, ".")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		if names[e.Name()] {
			t.Fatalf("duplicate root entry %q", e.Name())
		}
		names[e.Name()] = true
	}
	for _, want := range []string{"index.html", "app.js", "img", "shared"} {
		if !names[want] {
			t.Fatalf("root listing lacks %q: %v", want, names)
		}
	}
}

// TestStaticFS checks the real embedded assets compose the way index.html
// expects them (./shared/… next to ./app.js).
func TestStaticFS(t *testing.T) {
	o := staticFS()
	for _, name := range []string{"index.html", "app.js", "app.css", "shared/base.css", "shared/charts.js", "shared/ui.js", "shared/native.js"} {
		b, err := fs.ReadFile(o, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	if _, err := fs.ReadFile(o, "shared/demo.html"); err == nil {
		t.Fatal("demo.html should not be embedded")
	}
	// The embed roots are what staticFS composes.
	if _, err := fs.Stat(ui.Static, "index.html"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(sharedui.FS, "base.css"); err != nil {
		t.Fatal(err)
	}
}
