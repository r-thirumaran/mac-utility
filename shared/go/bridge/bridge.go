// Package bridge is the JSON-RPC-like dispatcher behind every web UI
// (docs/ARCHITECTURE.md §4). A request {"id", "method", "params"} is routed to
// the Handler registered for its method and answered with {"id", "result"} or
// {"id", "error": {"code", "message", "details"?}}. Events are pushed to the
// UI as {"event", "data"} through every registered Sink.
//
// The wire format is exactly what shared/ui/native.js expects; the same
// Dispatcher serves a WebSocket (httpserve) and any other transport.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// Error codes shared by every backend (docs/ARCHITECTURE.md §4 and
// docs/reach-api.md §2).
const (
	CodeNotFound        = "not_found"
	CodeInvalidParams   = "invalid_params"
	CodeInternal        = "internal"
	CodeNotAuthorized   = "not_authorized"
	CodeNeedsPermission = "needs_permission"
	CodeNotSupported    = "not_supported"
)

// Handler serves one method. params is the raw "params" member of the request
// (nil when absent). The returned value is JSON-encoded as "result"; a nil
// value encodes as {} so the UI always receives an object. Return an *Error to
// choose the error code; any other error is reported with code "internal".
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Error is a bridge error with a short snake_case code, a human message and
// optional structured details (for example {"permission": "accessibility"}).
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// Errorf builds an *Error with the given code and a formatted message.
func Errorf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Sink receives serialized event frames ({"event":…,"data":…}). Push must not
// block: a sink that cannot keep up should drop the frame (see ChanSink).
type Sink interface {
	Push(eventJSON []byte)
}

// Dispatcher routes requests to handlers and fans events out to sinks. The
// zero value is not usable; call New. All methods are safe for concurrent use.
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	sinks    map[uint64]Sink
	nextSink uint64
}

// New returns an empty Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{handlers: map[string]Handler{}, sinks: map[uint64]Sink{}}
}

// Register installs h for method, replacing any previous handler.
func (d *Dispatcher) Register(method string, h Handler) {
	d.mu.Lock()
	d.handlers[method] = h
	d.mu.Unlock()
}

// Methods returns the registered method names (unordered).
func (d *Dispatcher) Methods() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.handlers))
	for m := range d.handlers {
		out = append(out, m)
	}
	return out
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Handle decodes one request frame, runs its handler and returns the encoded
// response frame. The id is echoed back unchanged (number or string); when the
// request is not valid JSON the response carries "id": null and the error code
// "invalid_params". An unknown method yields "not_found". A handler panic is
// recovered and reported as "internal".
func (d *Dispatcher) Handle(ctx context.Context, req []byte) []byte {
	var r request
	if err := json.Unmarshal(req, &r); err != nil {
		return encode(response{ID: nullID, Error: Errorf(CodeInvalidParams, "bad request: %v", err)})
	}
	id := r.ID
	if len(id) == 0 {
		id = nullID
	}
	if r.Method == "" {
		return encode(response{ID: id, Error: Errorf(CodeInvalidParams, "missing method")})
	}
	d.mu.RLock()
	h, ok := d.handlers[r.Method]
	d.mu.RUnlock()
	if !ok {
		return encode(response{ID: id, Error: Errorf(CodeNotFound, "unknown method %q", r.Method)})
	}
	result, err := d.call(ctx, h, r.Params)
	if err != nil {
		return encode(response{ID: id, Error: toError(err)})
	}
	if result == nil {
		result = map[string]any{}
	}
	out, mErr := json.Marshal(response{ID: id, Result: result})
	if mErr != nil {
		return encode(response{ID: id, Error: Errorf(CodeInternal, "encode result: %v", mErr)})
	}
	return out
}

func (d *Dispatcher) call(ctx context.Context, h Handler, params json.RawMessage) (result any, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = Errorf(CodeInternal, "handler panic: %v", rec)
			_ = debug.Stack()
		}
	}()
	if len(params) == 0 {
		params = nil
	}
	return h(ctx, params)
}

var nullID = json.RawMessage("null")

func toError(err error) *Error {
	if e, ok := err.(*Error); ok {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

func encode(r response) []byte {
	out, err := json.Marshal(r)
	if err != nil {
		// Only reachable with unencodable Details; degrade to a plain error.
		r.Error = &Error{Code: r.Error.Code, Message: r.Error.Message}
		out, _ = json.Marshal(r)
	}
	return out
}

// AddSink registers s to receive every event and returns a function that
// removes it again. Removing twice is harmless.
func (d *Dispatcher) AddSink(s Sink) (remove func()) {
	d.mu.Lock()
	id := d.nextSink
	d.nextSink++
	d.sinks[id] = s
	d.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			delete(d.sinks, id)
			d.mu.Unlock()
		})
	}
}

// SinkCount returns the number of registered sinks.
func (d *Dispatcher) SinkCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sinks)
}

// Emit encodes {"event": event, "data": data} once and pushes it to every
// sink. It never blocks on a sink: delivery is the sink's responsibility.
// Emitting with no sinks is a no-op. Unencodable data is dropped silently.
func (d *Dispatcher) Emit(event string, data any) {
	d.mu.RLock()
	if len(d.sinks) == 0 {
		d.mu.RUnlock()
		return
	}
	sinks := make([]Sink, 0, len(d.sinks))
	for _, s := range d.sinks {
		sinks = append(sinks, s)
	}
	d.mu.RUnlock()
	frame, err := EncodeEvent(event, data)
	if err != nil {
		return
	}
	for _, s := range sinks {
		s.Push(frame)
	}
}

// EncodeEvent returns the event frame {"event": event, "data": data}.
func EncodeEvent(event string, data any) ([]byte, error) {
	if data == nil {
		data = map[string]any{}
	}
	return json.Marshal(struct {
		Event string `json:"event"`
		Data  any    `json:"data"`
	}{event, data})
}

// Params decodes raw into T and converts any decoding failure into an
// invalid_params *Error. A missing/empty params member decodes to the zero T.
func Params[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, Errorf(CodeInvalidParams, "invalid params: %v", err)
	}
	return v, nil
}

// ChanSink is a Sink backed by a bounded channel. Push never blocks: when the
// channel is full the frame is dropped and Dropped() is incremented. A
// transport drains C() from its own writer goroutine.
type ChanSink struct {
	ch      chan []byte
	dropped atomic.Uint64
}

// NewChanSink returns a ChanSink buffering up to size frames (minimum 1).
func NewChanSink(size int) *ChanSink {
	if size < 1 {
		size = 1
	}
	return &ChanSink{ch: make(chan []byte, size)}
}

// Push queues the frame or drops it when the buffer is full.
func (s *ChanSink) Push(eventJSON []byte) {
	select {
	case s.ch <- eventJSON:
	default:
		s.dropped.Add(1)
	}
}

// C returns the channel the queued frames are read from.
func (s *ChanSink) C() <-chan []byte { return s.ch }

// Dropped returns how many frames were dropped because the buffer was full.
func (s *ChanSink) Dropped() uint64 { return s.dropped.Load() }
