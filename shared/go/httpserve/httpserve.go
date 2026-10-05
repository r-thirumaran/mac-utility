// Package httpserve serves a web UI from an fs.FS and bridges a WebSocket at
// /ws to a bridge.Dispatcher, speaking the frames of shared/ui/native.js:
// text frames carry {"id","method","params"} requests and {"id","result"} /
// {"id","error"} responses; {"event","data"} frames are pushed from the
// Dispatcher's Emit; binary frames go to the client through Conn.SendBinary.
//
// An optional *http.ServeMux is mounted at /api/ (Reach's REST endpoints) and
// an optional Auth callback gates every request.
package httpserve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

// MaxMessage is the largest WebSocket message accepted from a client.
const MaxMessage = 8 << 20

// EventBuffer is how many pending event frames a connection buffers before
// Emit starts dropping them for that connection.
const EventBuffer = 256

// Options configures New.
type Options struct {
	// Static is served for every path not claimed below; "/" serves index.html.
	// nil answers 404 for static paths.
	Static fs.FS
	// Dispatcher handles /ws requests and is the source of pushed events.
	// nil means an empty dispatcher (every method is not_found).
	Dispatcher *bridge.Dispatcher
	// Auth decides whether a request may proceed (401 otherwise). It sees
	// every request including /ws, /api/ and static files; nil allows all.
	// CORS preflights (OPTIONS) under /api/ bypass it when AllowCORS is set.
	Auth func(r *http.Request) bool
	// API is mounted at /api/ (the mux sees the full path, e.g. "/api/info").
	API *http.ServeMux
	// AllowCORS adds Access-Control-Allow-Origin: * to /api/ responses,
	// answers preflights, and lets any origin open /ws.
	AllowCORS bool
	// OnWS runs synchronously after a WebSocket is accepted and registered as
	// an event sink, before the first request is read. Use it to send an
	// initial event or to remember the connection; return quickly.
	OnWS func(c *Conn)
}

// Conn is one accepted WebSocket. All methods are safe for concurrent use.
type Conn struct {
	ws     *websocket.Conn
	req    *http.Request
	ctx    context.Context
	cancel context.CancelFunc

	writeMu sync.Mutex
	sink    *bridge.ChanSink
	unsink  func()
	once    sync.Once
}

// Context is cancelled when the connection is closed.
func (c *Conn) Context() context.Context { return c.ctx }

// Request returns the upgrade request (query string, cookies, remote address).
func (c *Conn) Request() *http.Request { return c.req }

// SendBinary writes one binary message.
func (c *Conn) SendBinary(b []byte) error { return c.write(websocket.MessageBinary, b) }

// SendJSON writes v as one text message. Note that events should normally go
// through bridge.Dispatcher.Emit so every connection receives them.
func (c *Conn) SendJSON(v any) error {
	b, err := marshal(v)
	if err != nil {
		return err
	}
	return c.write(websocket.MessageText, b)
}

// Dropped returns how many event frames were dropped because this
// connection's buffer was full.
func (c *Conn) Dropped() uint64 { return c.sink.Dropped() }

func (c *Conn) write(typ websocket.MessageType, b []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.ctx.Err() != nil {
		return fmt.Errorf("httpserve: connection closed")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	return c.ws.Write(ctx, typ, b)
}

// Close unregisters the event sink, sends a normal close frame and cancels
// Context. It is idempotent and never blocks on the peer.
func (c *Conn) Close() {
	c.once.Do(func() {
		c.unsink()
		done := make(chan struct{})
		go func() {
			_ = c.ws.Close(websocket.StatusNormalClosure, "")
			close(done)
		}()
		go func() {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = c.ws.CloseNow()
			}
			c.cancel()
		}()
	})
}

type server struct {
	o      Options
	static http.Handler
}

// New builds the handler: /ws (WebSocket), /api/ (o.API) and everything else
// from o.Static.
func New(o Options) http.Handler {
	if o.Dispatcher == nil {
		o.Dispatcher = bridge.New()
	}
	return &server{o: o}
}

// ServeHTTP routes /ws, /api/ and static paths, applying CORS and Auth first.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isAPI := strings.HasPrefix(r.URL.Path, "/api/")
	if isAPI && s.o.AllowCORS {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if s.o.Auth != nil && !s.o.Auth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/ws":
		s.serveWS(w, r)
	case isAPI:
		if s.o.API == nil {
			http.NotFound(w, r)
			return
		}
		s.o.API.ServeHTTP(w, r)
	default:
		s.serveStatic(w, r)
	}
}

func (s *server) serveWS(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled}
	if s.o.AllowCORS {
		opts.OriginPatterns = []string{"*"}
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	ws.SetReadLimit(MaxMessage)
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{ws: ws, req: r, ctx: ctx, cancel: cancel, sink: bridge.NewChanSink(EventBuffer)}
	c.unsink = s.o.Dispatcher.AddSink(c.sink)
	defer c.Close()

	// Writer goroutine: drains pushed events.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-c.sink.C():
				if err := c.write(websocket.MessageText, ev); err != nil {
					c.Close()
					return
				}
			}
		}
	}()

	if s.o.OnWS != nil {
		s.o.OnWS(c)
	}
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue // clients do not send binary frames in v1
		}
		go func(req []byte) {
			resp := s.o.Dispatcher.Handle(ctx, req)
			if err := c.write(websocket.MessageText, resp); err != nil {
				c.Close()
			}
		}(data)
	}
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.o.Static == nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	f, st, name, err := openFile(s.o.Static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	if ctype == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, f)
}

// openFile opens name, falling back to name/index.html for directories, and
// returns the name of the file actually opened.
func openFile(fsys fs.FS, name string) (fs.File, fs.FileInfo, string, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, nil, "", err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, "", err
	}
	if !st.IsDir() {
		return f, st, name, nil
	}
	f.Close()
	name = path.Join(name, "index.html")
	f, err = fsys.Open(name)
	if err != nil {
		return nil, nil, "", err
	}
	st, err = f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, nil, "", errors.New("not a file")
	}
	return f, st, name, nil
}

// ListenLoopback serves h on 127.0.0.1 with a random port and returns the
// bound address ("127.0.0.1:12345") and the server, which the caller shuts
// down with srv.Shutdown or srv.Close.
func ListenLoopback(h http.Handler) (addr string, srv *http.Server, err error) {
	return Listen("127.0.0.1:0", h)
}

// Listen serves h on addr (for example "0.0.0.0:7788" for Reach) and returns
// the bound address and the server. Serving runs in a goroutine; errors after
// a successful bind are dropped (srv.Shutdown ends it cleanly).
func Listen(addr string, h http.Handler) (string, *http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("httpserve: listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), srv, nil
}
