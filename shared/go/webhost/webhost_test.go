package webhost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
)

var static = fstest.MapFS{
	"index.html": {Data: []byte("<!doctype html><title>Vitals</title>")},
	"app.js":     {Data: []byte("export {};")},
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func start(t *testing.T, c Config) (url string, done <-chan error, out *lockedBuf, cancel func()) {
	t.Helper()
	out = &lockedBuf{}
	urls := make(chan string, 1)
	c.Stdout = out
	c.OnListen = func(u string) { urls <- u }
	t.Setenv("PATH", t.TempDir()) // no xdg-open on Linux runs with NoUI=false
	ctx, cancelCtx := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- Run(ctx, c) }()
	select {
	case url = <-urls:
	case err := <-errc:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("OnListen not called")
	}
	return url, errc, out, cancelCtx
}

var lineRE = regexp.MustCompile(`^listening on (http://127\.0\.0\.1:(\d+)/\?token=([A-Za-z0-9_-]+))\n$`)

func TestRunNoUI(t *testing.T) {
	d := bridge.New()
	d.Register("app.info", func(ctx context.Context, p json.RawMessage) (any, error) {
		return map[string]string{"name": "Vitals", "platform": "linux"}, nil
	})
	url, done, out, cancel := start(t, Config{Title: "Vitals", Static: static, Dispatcher: d, NoUI: true})
	defer cancel()

	m := lineRE.FindStringSubmatch(out.String())
	if m == nil || m[1] != url {
		t.Fatalf("stdout %q does not match (url %s)", out.String(), url)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("expected exactly one line, got %q", out.String())
	}
	token := m[3]
	if len(token) != 43 {
		t.Fatalf("token %q is not 32 bytes base64url", token)
	}
	base := "http://127.0.0.1:" + m[2]

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	get := func(c *http.Client, u string) *http.Response {
		t.Helper()
		resp, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	// No token, wrong token: 401. Static assets need the token or cookie too.
	if resp := get(http.DefaultClient, base+"/"); resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp := get(http.DefaultClient, base+"/?token=wrong"); resp.StatusCode != 401 {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if resp := get(http.DefaultClient, base+"/app.js"); resp.StatusCode != 401 {
		t.Fatalf("asset without token: %d", resp.StatusCode)
	}
	// First GET / with the token sets the cookie …
	resp := get(client, url)
	if resp.StatusCode != 200 {
		t.Fatalf("with token: %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == CookieName {
			cookie = ck
		}
	}
	if cookie == nil || cookie.Value != token || !cookie.HttpOnly || cookie.Path != "/" {
		t.Fatalf("cookie not set correctly: %+v", cookie)
	}
	// … so later navigations and assets pass without the query string.
	if resp := get(client, base+"/app.js"); resp.StatusCode != 200 {
		t.Fatalf("asset with cookie: %d", resp.StatusCode)
	}
	if resp := get(client, base+"/"); resp.StatusCode != 200 || len(resp.Cookies()) != 0 {
		t.Fatalf("second GET /: %d cookies=%v", resp.StatusCode, resp.Cookies())
	}
	// A wrong cookie does not pass.
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "nope"})
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != 401 {
		t.Fatalf("wrong cookie: %d", r2.StatusCode)
	}

	ctx, cancelWS := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWS()
	if _, wsResp, err := websocket.Dial(ctx, "ws://127.0.0.1:"+m[2]+"/ws", nil); err == nil || wsResp == nil || wsResp.StatusCode != 401 {
		t.Fatalf("ws without token must be rejected: %v", err)
	}
	ws, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+m[2]+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"app.info","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := ws.Read(ctx)
	if err != nil || string(data) != `{"id":1,"result":{"name":"Vitals","platform":"linux"}}` {
		t.Fatalf("ws response %s %v", data, err)
	}
	// Cookie auth works for the socket as well.
	ws2, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+m[2]+"/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {CookieName + "=" + token}}})
	if err != nil {
		t.Fatalf("ws with cookie: %v", err)
	}
	ws2.CloseNow()
	// /api/ is not gated by the token (nil mux → 404, not 401).
	if resp := get(http.DefaultClient, base+"/api/info"); resp.StatusCode != 404 {
		t.Fatalf("/api/ without token: %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if _, err := http.DefaultClient.Get(base + "/"); err == nil {
		t.Fatal("server still answering after Run returned")
	}
}

func TestFixedTokenAndExtraAuth(t *testing.T) {
	url, done, _, cancel := start(t, Config{
		Title: "Reach", Static: static, NoUI: true, Token: "fixed-token", Listen: "127.0.0.1:0",
		Auth: func(r *http.Request) bool { return r.URL.Query().Get("token") == "phone" },
	})
	defer cancel()
	if !strings.HasSuffix(url, "/?token=fixed-token") {
		t.Fatalf("url %q", url)
	}
	base := strings.TrimSuffix(url, "/?token=fixed-token")
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/?token=fixed-token", 200},
		{"/?token=phone", 200},
		{"/?token=other", 401},
	} {
		resp, err := http.Get(base + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("%s: %d want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTrayAndQuit(t *testing.T) {
	trays := make(chan *tray.Tray, 1)
	custom := tray.Item{Title: "Pause", OnClick: func() {}}
	_, done, _, cancel := start(t, Config{
		Title: "Edgehop", Static: static, ShowOnStart: true,
		Tray:   tray.Config{Items: []tray.Item{custom}},
		OnTray: func(tr *tray.Tray) { trays <- tr },
	})
	defer cancel()
	var tr *tray.Tray
	select {
	case tr = <-trays:
	case <-time.After(5 * time.Second):
		t.Fatal("OnTray not called")
	}
	items := tr.Items()
	titles := make([]string, len(items))
	for i, it := range items {
		if it.Separator {
			titles[i] = "---"
		} else {
			titles[i] = it.Title
		}
	}
	if got := strings.Join(titles, ","); got != "Open Edgehop,---,Pause,---,Quit" {
		t.Fatalf("menu = %s", got)
	}
	if tr.Tooltip() != "Edgehop" {
		t.Fatalf("tooltip %q", tr.Tooltip())
	}
	tr.Click("Open Edgehop") // xdg-open is absent from PATH; failure is ignored
	if !tr.Click("Quit") {
		t.Fatal("Quit item missing")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Quit")
	}
}

func TestListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Run(ctx, Config{Listen: ln.Addr().String(), NoUI: true, Stdout: io.Discard}); err == nil {
		t.Fatal("Run must fail when the port is taken")
	}
}

func TestNewToken(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if len(a) != 43 || a == b || strings.ContainsAny(a, "+/=") {
		t.Fatalf("tokens %q %q", a, b)
	}
}
