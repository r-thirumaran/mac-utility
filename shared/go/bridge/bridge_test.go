package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

type echoParams struct {
	Text string `json:"text"`
	N    int    `json:"n"`
}

func newTestDispatcher() *Dispatcher {
	d := New()
	d.Register("echo", func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := Params[echoParams](raw)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": p.Text, "n": p.N}, nil
	})
	d.Register("fail.bridge", func(ctx context.Context, raw json.RawMessage) (any, error) {
		return nil, &Error{Code: CodeNeedsPermission, Message: "grant it", Details: map[string]string{"permission": "accessibility"}}
	})
	d.Register("fail.plain", func(ctx context.Context, raw json.RawMessage) (any, error) {
		return nil, errors.New("boom")
	})
	d.Register("nilresult", func(ctx context.Context, raw json.RawMessage) (any, error) {
		return nil, nil
	})
	d.Register("panic", func(ctx context.Context, raw json.RawMessage) (any, error) {
		panic("oops")
	})
	return d
}

func decode(t *testing.T, b []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, b)
	}
	return m
}

func TestHandle(t *testing.T) {
	d := newTestDispatcher()
	ctx := context.Background()
	cases := []struct {
		name      string
		req       string
		wantID    string
		wantRes   string
		wantCode  string
		wantInMsg string
	}{
		{"numeric id round trip", `{"id":7,"method":"echo","params":{"text":"hi","n":3}}`, "7", `{"n":3,"text":"hi"}`, "", ""},
		{"string id round trip", `{"id":"abc","method":"echo","params":{"text":"x"}}`, `"abc"`, `{"n":0,"text":"x"}`, "", ""},
		{"missing params", `{"id":1,"method":"echo"}`, "1", `{"n":0,"text":""}`, "", ""},
		{"nil result becomes object", `{"id":2,"method":"nilresult"}`, "2", `{}`, "", ""},
		{"unknown method", `{"id":3,"method":"nope"}`, "3", "", CodeNotFound, "nope"},
		{"bad json", `{"id":`, "null", "", CodeInvalidParams, ""},
		{"missing id", `{"method":"echo"}`, "null", `{"n":0,"text":""}`, "", ""},
		{"missing method", `{"id":4}`, "4", "", CodeInvalidParams, "method"},
		{"bad params type", `{"id":5,"method":"echo","params":{"n":"notanumber"}}`, "5", "", CodeInvalidParams, ""},
		{"bridge error with details", `{"id":6,"method":"fail.bridge"}`, "6", "", CodeNeedsPermission, "grant it"},
		{"plain error is internal", `{"id":8,"method":"fail.plain"}`, "8", "", CodeInternal, "boom"},
		{"panic is internal", `{"id":9,"method":"panic"}`, "9", "", CodeInternal, "oops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := d.Handle(ctx, []byte(tc.req))
			m := decode(t, resp)
			if string(m["id"]) != tc.wantID {
				t.Fatalf("id = %s, want %s (%s)", m["id"], tc.wantID, resp)
			}
			if tc.wantCode == "" {
				if _, has := m["error"]; has {
					t.Fatalf("unexpected error: %s", resp)
				}
				if string(m["result"]) != tc.wantRes {
					t.Fatalf("result = %s, want %s", m["result"], tc.wantRes)
				}
				return
			}
			if _, has := m["result"]; has {
				t.Fatalf("unexpected result in error response: %s", resp)
			}
			var e Error
			if err := json.Unmarshal(m["error"], &e); err != nil {
				t.Fatalf("error member: %v (%s)", err, resp)
			}
			if e.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q (%s)", e.Code, tc.wantCode, resp)
			}
			if !strings.Contains(e.Message, tc.wantInMsg) {
				t.Fatalf("message %q does not contain %q", e.Message, tc.wantInMsg)
			}
		})
	}
}

func TestErrorDetails(t *testing.T) {
	d := newTestDispatcher()
	resp := d.Handle(context.Background(), []byte(`{"id":1,"method":"fail.bridge"}`))
	if !strings.Contains(string(resp), `"details":{"permission":"accessibility"}`) {
		t.Fatalf("details missing: %s", resp)
	}
	resp = d.Handle(context.Background(), []byte(`{"id":1,"method":"fail.plain"}`))
	if strings.Contains(string(resp), "details") {
		t.Fatalf("details should be omitted when nil: %s", resp)
	}
	e := Errorf(CodeNotSupported, "no %s here", "gpu")
	if e.Error() != "not_supported: no gpu here" {
		t.Fatalf("Error() = %q", e.Error())
	}
}

type recordSink struct {
	mu     sync.Mutex
	frames []string
}

func (r *recordSink) Push(b []byte) {
	r.mu.Lock()
	r.frames = append(r.frames, string(b))
	r.mu.Unlock()
}

func TestEmit(t *testing.T) {
	d := New()
	a, b := &recordSink{}, &recordSink{}
	removeA := d.AddSink(a)
	d.AddSink(b)
	d.Emit("vitals.sample", map[string]int{"cpu": 42})
	d.Emit("nil.data", nil)
	removeA()
	removeA() // idempotent
	d.Emit("after.remove", "x")
	want := []string{`{"event":"vitals.sample","data":{"cpu":42}}`, `{"event":"nil.data","data":{}}`}
	if len(a.frames) != 2 || a.frames[0] != want[0] || a.frames[1] != want[1] {
		t.Fatalf("sink a got %q, want %q", a.frames, want)
	}
	if len(b.frames) != 3 || b.frames[2] != `{"event":"after.remove","data":"x"}` {
		t.Fatalf("sink b got %q", b.frames)
	}
	if d.SinkCount() != 1 {
		t.Fatalf("SinkCount = %d, want 1", d.SinkCount())
	}
}

func TestChanSinkDrops(t *testing.T) {
	s := NewChanSink(2)
	d := New()
	d.AddSink(s)
	for i := 0; i < 5; i++ {
		d.Emit("e", i)
	}
	if s.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 3", s.Dropped())
	}
	got := <-s.C()
	if string(got) != `{"event":"e","data":0}` {
		t.Fatalf("first frame = %s", got)
	}
	<-s.C()
	select {
	case f := <-s.C():
		t.Fatalf("unexpected extra frame %s", f)
	default:
	}
}

func TestConcurrentHandle(t *testing.T) {
	d := newTestDispatcher()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := d.Handle(context.Background(), []byte(`{"id":1,"method":"echo","params":{"n":1}}`))
			if !strings.Contains(string(resp), `"n":1`) {
				t.Errorf("bad response %s", resp)
			}
			if i%10 == 0 {
				d.Register("extra", func(context.Context, json.RawMessage) (any, error) { return 1, nil })
			}
		}(i)
	}
	wg.Wait()
}

func TestParamsHelper(t *testing.T) {
	v, err := Params[echoParams](nil)
	if err != nil || v.Text != "" {
		t.Fatalf("nil params: %v %+v", err, v)
	}
	v, err = Params[echoParams](json.RawMessage(`null`))
	if err != nil || v.N != 0 {
		t.Fatalf("null params: %v %+v", err, v)
	}
	_, err = Params[echoParams](json.RawMessage(`[1,2]`))
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidParams {
		t.Fatalf("got %v, want invalid_params", err)
	}
}
