package core

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
)

// Encrypted message type bytes (docs/edgehop-protocol.md §5).
const (
	TypeControl     = securechan.TypeControl
	TypeMouseMove   = securechan.TypeMouseMove
	TypeMouseButton = securechan.TypeMouseButton
	TypeScroll      = securechan.TypeScroll
	TypeKey         = securechan.TypeKey
	TypeKeyText     = securechan.TypeKeyText
)

// Key states (§5 key).
const (
	KeyUp     uint8 = 0
	KeyDown   uint8 = 1
	KeyRepeat uint8 = 2
)

// Scroll flags (§5 scroll).
const (
	ScrollFlagDiscrete uint8 = 1 << 0
	ScrollFlagMomentum uint8 = 1 << 1
)

// Control message names (§6 `t`).
const (
	TAuth        = "auth"
	TAuthOK      = "auth_ok"
	TInfo        = "info"
	TDisplays    = "displays"
	TLayout      = "layout"
	TActive      = "active"
	TClaim       = "claim"
	TClipboard   = "clipboard"
	TPing        = "ping"
	TPong        = "pong"
	TSecureInput = "secure_input"
	TError       = "error"
)

// ErrMalformed is returned for a frame whose body does not match its type.
var ErrMalformed = errors.New("edgehop: malformed frame")

// ErrUnknownType is returned for a type byte outside 0x01–0x06.
var ErrUnknownType = errors.New("edgehop: unknown frame type")

// ---------------------------------------------------------------- control

// InfoMsg is `info` (right after auth) and `displays` (on change). Port is
// an optional extension: the TCP port the sender listens on, so a machine
// that accepted a manually addressed pairing (no Bonjour) learns where to
// reconnect. Receivers that do not know the field ignore it.
type InfoMsg struct {
	T        string    `json:"t"`
	Name     string    `json:"name,omitempty"`
	Plat     string    `json:"plat,omitempty"`
	Version  string    `json:"version,omitempty"`
	Port     int       `json:"port,omitempty"`
	Displays []Display `json:"displays"`
}

// LayoutMsg is `layout`.
type LayoutMsg struct {
	T         string `json:"t"`
	UpdatedAt int64  `json:"updatedAt"`
	Tiles     []Tile `json:"tiles"`
}

// Layout converts the message to a Layout.
func (m LayoutMsg) Layout() Layout { return Layout{UpdatedAt: m.UpdatedAt, Tiles: m.Tiles} }

// Enter says where the cursor appears on the newly active machine.
type Enter struct {
	Edge Edge    `json:"edge"`
	Frac float64 `json:"frac"`
}

// ActiveMsg is `active`: control moves to ID. Enter is nil for a hotkey or
// menu switch, in which case the new machine shows its cursor at the centre
// of its primary display.
type ActiveMsg struct {
	T         string `json:"t"`
	ID        string `json:"id"`
	From      string `json:"from"`
	Enter     *Enter `json:"enter,omitempty"`
	Buttons   uint8  `json:"buttons"`
	Modifiers uint8  `json:"modifiers"`
	Seq       uint32 `json:"seq"`
}

// ClaimMsg is `claim`: the sender (ID) wants to become active.
type ClaimMsg struct {
	T   string `json:"t"`
	ID  string `json:"id"`
	Seq uint32 `json:"seq"`
}

// ClipboardMsg is `clipboard` (v1: text only).
type ClipboardMsg struct {
	T    string `json:"t"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	B64  string `json:"b64,omitempty"`
}

// PingMsg is `ping` / `pong`.
type PingMsg struct {
	T  string `json:"t"`
	TS int64  `json:"ts"`
}

// SecureInputMsg is `secure_input`.
type SecureInputMsg struct {
	T  string `json:"t"`
	On bool   `json:"on"`
}

// ErrorMsg is `error`.
type ErrorMsg struct {
	T       string `json:"t"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// EncodeControl builds a control frame payload (TypeControl || JSON).
func EncodeControl(v any) ([]byte, error) { return securechan.EncodeControl(v) }

// MustControl is EncodeControl for message structs that cannot fail.
func MustControl(v any) []byte {
	b, err := EncodeControl(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ---------------------------------------------------------------- binary

// MouseMove is a 0x02 frame: relative motion in the sender's pixels.
type MouseMove struct {
	DX, DY int16
}

// MouseButton is a 0x03 frame.
type MouseButton struct {
	Button     uint8
	Down       bool
	ClickCount uint8
}

// Scroll is a 0x04 frame.
type Scroll struct {
	DX, DY int16
	Flags  uint8
}

// Discrete reports the wheel-notch flag.
func (s Scroll) Discrete() bool { return s.Flags&ScrollFlagDiscrete != 0 }

// Key is a 0x05 frame.
type Key struct {
	Usage     uint16
	State     uint8
	Modifiers uint8
}

func clampI16(v int) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// EncodeMouseMove builds a 0x02 frame (deltas are clamped to int16).
func EncodeMouseMove(dx, dy int) []byte {
	b := make([]byte, 5)
	b[0] = TypeMouseMove
	binary.BigEndian.PutUint16(b[1:], uint16(clampI16(dx)))
	binary.BigEndian.PutUint16(b[3:], uint16(clampI16(dy)))
	return b
}

// EncodeMouseButton builds a 0x03 frame.
func EncodeMouseButton(button uint8, down bool, clicks int) []byte {
	state := byte(0)
	if down {
		state = 1
	}
	if clicks < 0 {
		clicks = 0
	}
	if clicks > 255 {
		clicks = 255
	}
	return []byte{TypeMouseButton, button, state, byte(clicks)}
}

// EncodeScroll builds a 0x04 frame.
func EncodeScroll(dx, dy int, flags uint8) []byte {
	b := make([]byte, 6)
	b[0] = TypeScroll
	binary.BigEndian.PutUint16(b[1:], uint16(clampI16(dx)))
	binary.BigEndian.PutUint16(b[3:], uint16(clampI16(dy)))
	b[5] = flags
	return b
}

// EncodeKey builds a 0x05 frame.
func EncodeKey(usage int, state uint8, mods uint8) []byte {
	b := make([]byte, 5)
	b[0] = TypeKey
	binary.BigEndian.PutUint16(b[1:], uint16(usage))
	b[3] = state
	b[4] = mods
	return b
}

// EncodeKeyText builds a 0x06 frame.
func EncodeKeyText(text string) []byte {
	return append([]byte{TypeKeyText}, text...)
}

// Message is a decoded frame: Type says which field is set. Control frames
// keep their raw JSON in Control with the `t` field pre-parsed in T.
type Message struct {
	Type        byte
	T           string
	Control     json.RawMessage
	MouseMove   MouseMove
	MouseButton MouseButton
	Scroll      Scroll
	Key         Key
	KeyText     string
}

// IsInput reports whether the frame carries mouse/keyboard input.
func (m Message) IsInput() bool { return m.Type >= TypeMouseMove && m.Type <= TypeKeyText }

// Decode parses a decrypted payload (type byte || body).
func Decode(payload []byte) (Message, error) {
	if len(payload) == 0 {
		return Message{}, fmt.Errorf("%w: empty payload", ErrMalformed)
	}
	m := Message{Type: payload[0]}
	body := payload[1:]
	switch m.Type {
	case TypeControl:
		var probe struct {
			T string `json:"t"`
		}
		if err := json.Unmarshal(body, &probe); err != nil || probe.T == "" {
			return m, fmt.Errorf("%w: control JSON", ErrMalformed)
		}
		m.T = probe.T
		m.Control = json.RawMessage(append([]byte(nil), body...))
	case TypeMouseMove:
		if len(body) != 4 {
			return m, fmt.Errorf("%w: mouse_move body is %d bytes", ErrMalformed, len(body))
		}
		m.MouseMove = MouseMove{int16(binary.BigEndian.Uint16(body[0:])), int16(binary.BigEndian.Uint16(body[2:]))}
	case TypeMouseButton:
		if len(body) != 3 {
			return m, fmt.Errorf("%w: mouse_button body is %d bytes", ErrMalformed, len(body))
		}
		if body[1] > 1 {
			return m, fmt.Errorf("%w: mouse_button state %d", ErrMalformed, body[1])
		}
		m.MouseButton = MouseButton{Button: body[0], Down: body[1] == 1, ClickCount: body[2]}
	case TypeScroll:
		if len(body) != 5 {
			return m, fmt.Errorf("%w: scroll body is %d bytes", ErrMalformed, len(body))
		}
		m.Scroll = Scroll{int16(binary.BigEndian.Uint16(body[0:])), int16(binary.BigEndian.Uint16(body[2:])), body[4]}
	case TypeKey:
		if len(body) != 4 {
			return m, fmt.Errorf("%w: key body is %d bytes", ErrMalformed, len(body))
		}
		if body[2] > KeyRepeat {
			return m, fmt.Errorf("%w: key state %d", ErrMalformed, body[2])
		}
		m.Key = Key{Usage: binary.BigEndian.Uint16(body[0:]), State: body[2], Modifiers: body[3]}
	case TypeKeyText:
		if !utf8.Valid(body) {
			return m, fmt.Errorf("%w: key_text is not UTF-8", ErrMalformed)
		}
		m.KeyText = string(body)
	default:
		return m, fmt.Errorf("%w: 0x%02x", ErrUnknownType, m.Type)
	}
	return m, nil
}

// ParseControl decodes the control body into v.
func (m Message) ParseControl(v any) error {
	if m.Type != TypeControl {
		return fmt.Errorf("%w: not a control frame", ErrMalformed)
	}
	if err := json.Unmarshal(m.Control, v); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrMalformed, m.T, err)
	}
	return nil
}
