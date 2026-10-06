package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/r-thirumaran/mac-utility/shared/go/httpserve"
)

// newTestAgent builds an agent on the fake backend with a temp config.
func newTestAgent(t *testing.T) (*agent, *fakeBackend) {
	t.Helper()
	cfg, err := loadStore(filepath.Join(t.TempDir(), "config.json"), "testbox")
	if err != nil {
		t.Fatal(err)
	}
	fb := newFakeBackend()
	a := newAgent(cfg, fb, "local-token", "test", log.New(io.Discard, "", 0), true, false)
	return a, fb
}

// newTestServer serves the agent exactly like main.go does.
func newTestServer(t *testing.T, a *agent) *httptest.Server {
	t.Helper()
	h := httpserve.New(httpserve.Options{Dispatcher: a.dLAN, Auth: a.authorize, API: a.apiMux(), AllowCORS: true, OnWS: a.onWS})
	srv := httptest.NewServer(a.cookieSetter(h))
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out
}

func TestInfoAndPeers(t *testing.T) {
	a, _ := newTestAgent(t)
	srv := newTestServer(t, a)
	resp, err := http.Get(srv.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header")
	}
	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info["name"] != "testbox" || info["platform"] != "linux" || info["pairingRequired"] != true || info["id"] != a.id {
		t.Fatalf("info = %v", info)
	}
	if caps, _ := info["caps"].([]any); len(caps) != 5 {
		t.Fatalf("caps = %v", info["caps"])
	}
	resp2, err := http.Get(srv.URL + "/api/peers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("peers = %s", body)
	}
}

// TestPairingFlow: request → code shown locally → wrong ×5 → locked, then a
// fresh pairer accepts the right code and the token opens /ws.
func TestPairingFlow(t *testing.T) {
	a, _ := newTestAgent(t)
	srv := newTestServer(t, a)
	now := time.Now()
	a.pair.now = func() time.Time { return now }

	// The code reaches the settings page as a reach.pairCode event.
	codes := make(chan string, 4)
	a.dLocal.AddSink(sinkFunc(func(ev []byte) {
		var e struct {
			Event string `json:"event"`
			Data  struct {
				Code   string `json:"code"`
				Device string `json:"device"`
			} `json:"data"`
		}
		_ = json.Unmarshal(ev, &e)
		if e.Event == "reach.pairCode" {
			codes <- e.Data.Code + "|" + e.Data.Device
		}
	}))

	status, _ := postJSON(t, srv.URL+"/api/pair/request", map[string]string{"device": "Test phone"})
	if status != http.StatusNoContent {
		t.Fatalf("pair/request status %d", status)
	}
	var code string
	select {
	case got := <-codes:
		parts := strings.SplitN(got, "|", 2)
		code = parts[0]
		if len(code) != 6 || parts[1] != "Test phone" {
			t.Fatalf("pairCode event = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reach.pairCode event")
	}
	// Asking again while the code is valid shows the same code.
	again, _, ok, err := a.pair.request("Other phone")
	if err != nil || !ok || again != code {
		t.Fatalf("second request = %q ok=%v err=%v, want %q", again, ok, err, code)
	}

	wrong := "000000"
	if wrong == code {
		wrong = "000001"
	}
	for i := 1; i <= 4; i++ {
		status, body := postJSON(t, srv.URL+"/api/pair", map[string]string{"device": "Test phone", "code": wrong})
		if status != http.StatusForbidden || body["error"] != "bad_code" {
			t.Fatalf("wrong code #%d: %d %v", i, status, body)
		}
	}
	status, body := postJSON(t, srv.URL+"/api/pair", map[string]string{"device": "Test phone", "code": wrong})
	if status != http.StatusForbidden || body["error"] != "locked" {
		t.Fatalf("5th wrong code: %d %v (want locked)", status, body)
	}
	// Even the right code is refused while locked, and so is a new request.
	status, body = postJSON(t, srv.URL+"/api/pair", map[string]string{"device": "Test phone", "code": code})
	if status != http.StatusForbidden || body["error"] != "locked" {
		t.Fatalf("right code while locked: %d %v", status, body)
	}
	status, body = postJSON(t, srv.URL+"/api/pair/request", map[string]string{"device": "Test phone"})
	if status != http.StatusForbidden || body["error"] != "locked" {
		t.Fatalf("request while locked: %d %v", status, body)
	}
	if len(a.cfg.Snapshot().Pairings) != 0 {
		t.Fatal("a pairing was stored during the lockout")
	}

	// 10 minutes later pairing works again.
	now = now.Add(lockoutPeriod + time.Second)
	status, _ = postJSON(t, srv.URL+"/api/pair/request", map[string]string{"device": "Test phone"})
	if status != http.StatusNoContent {
		t.Fatalf("request after lockout: %d", status)
	}
	code = strings.SplitN(<-codes, "|", 2)[0]
	status, body = postJSON(t, srv.URL+"/api/pair", map[string]string{"device": "Test phone", "code": code})
	if status != http.StatusOK || body["token"] == "" {
		t.Fatalf("pair: %d %v", status, body)
	}
	token := body["token"].(string)
	if len(token) != 43 {
		t.Fatalf("token %q is not 32 bytes base64url", token)
	}
	// The code is consumed.
	status, body = postJSON(t, srv.URL+"/api/pair", map[string]string{"device": "Test phone", "code": code})
	if status != http.StatusForbidden || body["error"] != "bad_code" {
		t.Fatalf("reused code: %d %v", status, body)
	}
	pairings := a.cfg.Snapshot().Pairings
	if len(pairings) != 1 || pairings[0].Device != "Test phone" || pairings[0].Token != token {
		t.Fatalf("pairings = %+v", pairings)
	}

	// Expired codes are refused.
	now = now.Add(time.Minute)
	c2, _, _, _ := a.pair.request("x")
	now = now.Add(codeTTL + time.Second)
	if r := a.pair.redeem(c2); r != pairBadCode {
		t.Fatalf("expired code: %v", r)
	}
}

type sinkFunc func([]byte)

func (f sinkFunc) Push(b []byte) { f(b) }

func dialWS(t *testing.T, srvURL, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srvURL, "http") + "/ws" + query
	return websocket.Dial(ctx, url, nil)
}

func wsCall(t *testing.T, c *websocket.Conn, id int, method string, params any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := c.Write(ctx, websocket.MessageText, req); err != nil {
		t.Fatal(err)
	}
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		if m["event"] != nil {
			continue
		}
		if v, ok := m["id"].(float64); ok && int(v) == id {
			return m
		}
	}
}

func TestWSTokenAuth(t *testing.T) {
	a, fb := newTestAgent(t)
	srv := newTestServer(t, a)
	for _, q := range []string{"", "?token=nope", "?token=", "?token=local-token-x"} {
		c, resp, err := dialWS(t, srv.URL, q)
		if err == nil {
			c.Close(websocket.StatusNormalClosure, "")
			t.Fatalf("dial %q succeeded", q)
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("dial %q: %v (resp %v)", q, err, resp)
		}
	}
	token, err := a.cfg.AddPairing("Phone")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := dialWS(t, srv.URL, "?token="+token)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	// First frame is the info event.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, first, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ev map[string]any
	_ = json.Unmarshal(first, &ev)
	if ev["event"] != "info" {
		t.Fatalf("first frame = %s", first)
	}
	if data := ev["data"].(map[string]any); data["hostname"] == nil || data["uptimeSec"] == nil {
		t.Fatalf("info data = %v", data)
	}
	// A phone may drive the laptop but not the settings methods.
	if r := wsCall(t, c, 1, "mouse.move", map[string]any{"dx": 3, "dy": -2}); r["error"] != nil {
		t.Fatalf("mouse.move: %v", r)
	}
	if r := wsCall(t, c, 2, "reach.state", nil); r["error"] == nil || r["error"].(map[string]any)["code"] != "not_authorized" {
		t.Fatalf("reach.state from a phone: %v", r)
	}
	acts := fb.Actions()
	if len(acts) != 1 || acts[0]["kind"] != "mouse.move" || acts[0]["dx"] != 3.0 {
		t.Fatalf("actions = %v", acts)
	}

	// The local token (query or cookie) opens the settings methods.
	lc, _, err := dialWS(t, srv.URL, "?token=local-token")
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close(websocket.StatusNormalClosure, "")
	if r := wsCall(t, lc, 3, "reach.state", nil); r["error"] != nil || r["result"].(map[string]any)["name"] != "testbox" {
		t.Fatalf("reach.state locally: %v", r)
	}
	// Revoking the phone closes its socket.
	if r := wsCall(t, lc, 4, "reach.pairings.revoke", map[string]string{"device": "Phone"}); r["error"] != nil {
		t.Fatalf("revoke: %v", r)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	if _, _, err := c.Read(rctx); err == nil {
		t.Fatal("phone socket still open after revoke")
	}
	if _, _, err := dialWS(t, srv.URL, "?token="+token); err == nil {
		t.Fatal("revoked token still accepted")
	}

	// GET / with the local token sets the cookie that /ws accepts.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/?token=local-token")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie string
	for _, ck := range resp.Cookies() {
		if ck.Name == "mu_token" {
			cookie = ck.Value
		}
	}
	if cookie != "local-token" {
		t.Fatalf("cookie = %q", cookie)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	cc, _, err := websocket.Dial(ctx2, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {"mu_token=" + cookie}}})
	if err != nil {
		t.Fatalf("cookie dial: %v", err)
	}
	cc.Close(websocket.StatusNormalClosure, "")
}

func TestStreamOverWS(t *testing.T) {
	a, _ := newTestAgent(t)
	srv := newTestServer(t, a)
	token, _ := a.cfg.AddPairing("Phone")
	c, _, err := dialWS(t, srv.URL, "?token="+token)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(8 << 20)
	if r := wsCall(t, c, 1, "screen.stream.start", map[string]any{"display": 0, "maxWidth": 320, "quality": 0.5, "fps": 10}); r["error"] != nil {
		t.Fatalf("start: %v", r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ != websocket.MessageBinary {
			continue
		}
		kind, display, w, h, body, ok := decodeFrameHeader(data)
		if !ok || kind != frameKindJPEG || display != 0 || w != 320 || h != 180 || !bytes.HasPrefix(body, []byte{0xff, 0xd8}) {
			t.Fatalf("frame kind=%d display=%d %dx%d ok=%v", kind, display, w, h, ok)
		}
		break
	}
	if r := wsCall(t, c, 2, "screen.stream.stop", nil); r["error"] != nil {
		t.Fatalf("stop: %v", r)
	}
	if r := wsCall(t, c, 3, "screen.frame", map[string]any{"maxWidth": 160}); r["error"] != nil || r["result"].(map[string]any)["w"] != 160.0 {
		t.Fatalf("screen.frame: %v", r)
	}
}

func TestMethodValidation(t *testing.T) {
	a, fb := newTestAgent(t)
	ctx := context.Background()
	call := func(method, params string) map[string]any {
		var m map[string]any
		_ = json.Unmarshal(a.dLAN.Handle(ctx, []byte(`{"id":1,"method":"`+method+`","params":`+params+`}`)), &m)
		return m
	}
	errCode := func(m map[string]any) string {
		if e, ok := m["error"].(map[string]any); ok {
			return e["code"].(string)
		}
		return ""
	}
	if c := errCode(call("mouse.click", `{"button":"back"}`)); c != "invalid_params" {
		t.Fatalf("bad button → %q", c)
	}
	if c := errCode(call("media", `{"action":"louder"}`)); c != "invalid_params" {
		t.Fatalf("bad media → %q", c)
	}
	if c := errCode(call("power", `{"action":"explode"}`)); c != "invalid_params" {
		t.Fatalf("bad power → %q", c)
	}
	if c := errCode(call("open.url", `{"url":"file:///etc/passwd"}`)); c != "invalid_params" {
		t.Fatalf("file url → %q", c)
	}
	if c := errCode(call("key.press", `{"key":"Enter","mods":["hyper"]}`)); c != "invalid_params" {
		t.Fatalf("bad mod → %q", c)
	}
	if c := errCode(call("volume.set", `{"level":101}`)); c != "invalid_params" {
		t.Fatalf("bad level → %q", c)
	}
	if c := errCode(call("nope", `{}`)); c != "not_found" {
		t.Fatalf("unknown method → %q", c)
	}
	fb.ClearActions()
	if m := call("mouse.click", `{"button":"left","count":2,"display":0,"x":0.25,"y":0.75}`); m["error"] != nil {
		t.Fatalf("click with position: %v", m)
	}
	if m := call("key.press", `{"key":"KeyC","mods":["cmdOrCtrl"]}`); m["error"] != nil {
		t.Fatalf("key.press: %v", m)
	}
	acts := fb.Actions()
	if len(acts) != 3 || acts[0]["kind"] != "mouse.moveTo" || acts[1]["kind"] != "mouse.click" || acts[1]["count"] != 2 || acts[2]["scan"] != 0x2e {
		t.Fatalf("actions = %v", acts)
	}
	if m := call("reach.debug.actions", `{"clear":true}`); m["error"] != nil || len(m["result"].(map[string]any)["actions"].([]any)) != 3 {
		t.Fatalf("debug actions: %v", m)
	}
	if len(fb.Actions()) != 0 {
		t.Fatal("clear did not empty the buffer")
	}
}
