package main

import (
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// overlayFS serves a base file system with other file systems mounted under
// named top-level directories, so the UI's ./shared/base.css resolves to the
// embedded shared/ui kit while ./app.js comes from the app's own static files.
// A mount shadows a same-named entry of the base.
type overlayFS struct {
	base   fs.FS
	mounts map[string]fs.FS
}

// newOverlayFS mounts each fs.FS in mounts at the directory named by its key.
func newOverlayFS(base fs.FS, mounts map[string]fs.FS) *overlayFS {
	m := make(map[string]fs.FS, len(mounts))
	for k, v := range mounts {
		m[strings.Trim(k, "/")] = v
	}
	return &overlayFS{base: base, mounts: m}
}

// Open implements fs.FS.
func (o *overlayFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return o.root()
	}
	head, rest, _ := strings.Cut(name, "/")
	if m, ok := o.mounts[head]; ok {
		if rest == "" {
			f, err := m.Open(".")
			if err != nil {
				return nil, &fs.PathError{Op: "open", Path: name, Err: err}
			}
			return &renamedDir{File: f, name: head}, nil
		}
		return m.Open(rest)
	}
	return o.base.Open(name)
}

// root lists the base root plus one directory entry per mount.
func (o *overlayFS) root() (fs.File, error) {
	entries, err := fs.ReadDir(o.base, ".")
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(entries)+len(o.mounts))
	for _, e := range entries {
		if _, shadowed := o.mounts[e.Name()]; !shadowed {
			out = append(out, e)
		}
	}
	for name := range o.mounts {
		out = append(out, mountEntry{name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return &virtualDir{name: ".", entries: out}, nil
}

// mountEntry is the directory entry of a mount point in the root listing.
type mountEntry struct{ name string }

func (m mountEntry) Name() string               { return m.name }
func (m mountEntry) IsDir() bool                { return true }
func (m mountEntry) Type() fs.FileMode          { return fs.ModeDir }
func (m mountEntry) Info() (fs.FileInfo, error) { return dirInfo{name: m.name}, nil }

// dirInfo is the FileInfo of a synthetic directory.
type dirInfo struct{ name string }

func (d dirInfo) Name() string       { return d.name }
func (d dirInfo) Size() int64        { return 0 }
func (d dirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (d dirInfo) ModTime() time.Time { return time.Time{} }
func (d dirInfo) IsDir() bool        { return true }
func (d dirInfo) Sys() any           { return nil }

// virtualDir is an in-memory directory (the overlay root).
type virtualDir struct {
	name    string
	entries []fs.DirEntry
	pos     int
}

func (d *virtualDir) Stat() (fs.FileInfo, error) { return dirInfo{name: d.name}, nil }
func (d *virtualDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid}
}
func (d *virtualDir) Close() error { return nil }

// ReadDir implements fs.ReadDirFile with the usual n semantics.
func (d *virtualDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.pos:]
	if n <= 0 {
		d.pos = len(d.entries)
		return append([]fs.DirEntry(nil), rest...), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.pos += n
	return append([]fs.DirEntry(nil), rest[:n]...), nil
}

// renamedDir is a mount's root directory presented under the mount name, so
// Stat().Name() reports "shared" rather than ".".
type renamedDir struct {
	fs.File
	name string
}

func (r *renamedDir) Stat() (fs.FileInfo, error) {
	info, err := r.File.Stat()
	if err != nil {
		return nil, err
	}
	return renamedInfo{FileInfo: info, name: r.name}, nil
}

// ReadDir delegates to the wrapped directory when it supports listing.
func (r *renamedDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d, ok := r.File.(fs.ReadDirFile); ok {
		return d.ReadDir(n)
	}
	return nil, &fs.PathError{Op: "readdir", Path: r.name, Err: fs.ErrInvalid}
}

type renamedInfo struct {
	fs.FileInfo
	name string
}

func (r renamedInfo) Name() string { return r.name }
