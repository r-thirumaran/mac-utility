package httpserve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

var static = fstest.MapFS{
	"index.html":     {Data: []byte("<!doctype html><title>t</title>")},
	"app.js":         {Data: []byte("export const x = 1;")},
	"sub/index.html": {Data: []byte("sub index")},
}

func newDispatcher() *bridge.Dispatcher {
	d := bridge.New()
	d.Register("echo", func(ctx context.Context, p json.RawMessage) (any, error) {
		return map[string]any{"got": p}, nil
	})
	d.Register("slow", func(ctx context.Context, p json.RawMessage) (any, error) {
		time.Sleep(100 * time.Millisecond)
		return "slow", nil
	})
	return d
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestStatic(t *testing.T) {
	addr, srv, err := ListenLoopback(New(Options{Static: static}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("addr = %q", addr)
	}
	base := "http://" + addr
	cases := []struct {
		path, wantBody, wantType string
		wantCode                 int
	}{
		{"/", "<!doctype html><title>t</title>", "text/html", 200},
		{"/index.html", "<!doctype html><title>t</title>", "text/html", 200},
		{"/app.js", "export const x = 1;", "javascript", 200},
		{"/sub", "sub index", "text/html", 200},
		{"/sub/", "sub index", "text/html", 200},
		{"/missing.css", "", "", 404},
		{"/../index.html", "<!doctype html><title>t</title>", "text/html", 200},
		{"/api/info", "", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, body := get(t, base+tc.path)
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.wantCode)
			}
			if tc.wantCode != 200 {
				return
			}
			if body != tc.wantBody {
				t.Fatalf("body %q", body)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, tc.wantType) {
				t.Fatalf("content-type %q", ct)
			}
		})
	}
	resp, err := http.Post(base+"/app.js", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", resp.StatusCode)
	}
}

func TestAuth(t *testing.T) {
	auth := func(r *http.Request) bool { return r.URL.Query().Get("token") == "secret" }
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) })
	addr, srv, err := ListenLoopback(New(Options{Static: static, Auth: auth, API: mux, AllowCORS: true, Dispatcher: newDispatcher()}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	base := "http://" + addr

	if resp, _ := get(t, base+"/"); resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := get(t, base+"/?token=wrong"); resp.StatusCode != 401 {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if resp, _ := get(t, base+"/?token=secret"); resp.StatusCode != 200 {
		t.Fatalf("right token: %d", resp.StatusCode)
	}
	if resp, body := get(t, base+"/api/info?token=secret"); resp.StatusCode != 200 || body != `{"ok":true}` || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("api: %d %q %q", resp.StatusCode, body, resp.Header.Get("Access-Control-Allow-Origin"))
	}
	if resp, _ := get(t, base+"/api/info"); resp.StatusCode != 401 {
		t.Fatalf("api without token: %d", resp.StatusCode)
	}
	// Preflight bypasses auth.
	req, _ := http.NewRequest(http.MethodOptions, base+"/api/info", nil)
	req.Header.Set("Origin", "capacitor://localhost")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, wsResp, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err == nil {
		t.Fatal("ws without token must fail")
	}
	if wsResp == nil || wsResp.StatusCode != 401 {
		t.Fatalf("ws without token: %v %v", err, wsResp)
	}
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws?token=secret", nil)
	if err != nil {
		t.Fatalf("ws with token: %v", err)
	}
	c.Close(websocket.StatusNormalClosure, "")
}

func readText(t *testing.T, ctx context.Context, c *websocket.Conn) string {
	t.Helper()
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", typ)
	}
	return string(data)
}

func TestWebSocketRoundTrip(t *testing.T) {
	d := newDispatcher()
	conns := make(chan *Conn, 1)
	addr, srv, err := ListenLoopback(New(Options{Static: static, Dispatcher: d, OnWS: func(c *Conn) { conns <- c }}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var sc *Conn
	select {
	case sc = <-conns:
	case <-ctx.Done():
		t.Fatal("OnWS not called")
	}
	if sc.Request().URL.Path != "/ws" {
		t.Fatalf("Request path %q", sc.Request().URL.Path)
	}

	// Request / response.
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"id":7,"method":"echo","params":{"a":1}}`)); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, ctx, c); got != `{"id":7,"result":{"got":{"a":1}}}` {
		t.Fatalf("response %s", got)
	}
	// Requests are handled concurrently: the slow one answers after the fast one.
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"id":"s","method":"slow"}`))
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"id":"f","method":"echo"}`))
	first, second := readText(t, ctx, c), readText(t, ctx, c)
	if !strings.HasPrefix(first, `{"id":"f"`) || !strings.HasPrefix(second, `{"id":"s"`) {
		t.Fatalf("order: %s then %s", first, second)
	}
	// Unknown method.
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"id":8,"method":"nope"}`))
	if got := readText(t, ctx, c); !strings.Contains(got, `"code":"not_found"`) {
		t.Fatalf("unknown method: %s", got)
	}

	// Event push through the dispatcher.
	if d.SinkCount() != 1 {
		t.Fatalf("SinkCount = %d", d.SinkCount())
	}
	d.Emit("vitals.sample", map[string]int{"cpu": 3})
	if got := readText(t, ctx, c); got != `{"event":"vitals.sample","data":{"cpu":3}}` {
		t.Fatalf("event %s", got)
	}

	// Direct sends.
	if err := sc.SendJSON(map[string]string{"event": "x", "data": "<y>"}); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, ctx, c); got != `{"data":"<y>","event":"x"}` {
		t.Fatalf("SendJSON %s", got)
	}
	if err := sc.SendBinary([]byte{1, 0, 2, 0, 3}); err != nil {
		t.Fatal(err)
	}
	typ, data, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "\x01\x00\x02\x00\x03" {
		t.Fatalf("binary: %v %v %x", err, typ, data)
	}

	// Server-side close reaches the client with a normal status and
	// unregisters the sink.
	sc.Close()
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("close status: %v", err)
	}
	select {
	case <-sc.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Context not cancelled after Close")
	}
	deadline := time.Now().Add(2 * time.Second)
	for d.SinkCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if d.SinkCount() != 0 {
		t.Fatalf("sink still registered: %d", d.SinkCount())
	}
	if err := sc.SendJSON("late"); err == nil {
		t.Fatal("SendJSON after Close must fail")
	}
}

func TestClientCloseUnregisters(t *testing.T) {
	d := newDispatcher()
	addr, srv, err := ListenLoopback(New(Options{Dispatcher: d}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"echo"}`))
	readText(t, ctx, c)
	c.Close(websocket.StatusNormalClosure, "bye")
	deadline := time.Now().Add(2 * time.Second)
	for d.SinkCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if d.SinkCount() != 0 {
		t.Fatal("sink not removed after client close")
	}
}

func TestListenAddr(t *testing.T) {
	addr, srv, err := Listen("127.0.0.1:0", New(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if resp, _ := get(t, "http://"+addr+"/"); resp.StatusCode != 404 {
		t.Fatalf("no static: %d", resp.StatusCode)
	}
	if _, _, err := Listen("127.0.0.1:1", New(Options{})); err == nil {
		t.Skip("binding port 1 succeeded (running as root); skip the error path")
	}
}
