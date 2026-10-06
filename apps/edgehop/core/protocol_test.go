package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestBinaryLayouts(t *testing.T) {
	// Exact byte layouts of docs/edgehop-protocol.md §5 (big-endian).
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"mouse_move +3,-2", EncodeMouseMove(3, -2), []byte{0x02, 0x00, 0x03, 0xFF, 0xFE}},
		{"mouse_move clamp", EncodeMouseMove(70000, -70000), []byte{0x02, 0x7F, 0xFF, 0x80, 0x00}},
		{"mouse_button right down ×2", EncodeMouseButton(1, true, 2), []byte{0x03, 0x01, 0x01, 0x02}},
		{"mouse_button left up", EncodeMouseButton(0, false, 1), []byte{0x03, 0x00, 0x00, 0x01}},
		{"scroll one notch down, discrete", EncodeScroll(0, -120, ScrollFlagDiscrete), []byte{0x04, 0x00, 0x00, 0xFF, 0x88, 0x01}},
		{"key A down with LShift", EncodeKey(0x04, KeyDown, 0x02), []byte{0x05, 0x00, 0x04, 0x01, 0x02}},
		{"key play/pause up", EncodeKey(0x10CD, KeyUp, 0), []byte{0x05, 0x10, 0xCD, 0x00, 0x00}},
		{"key repeat", EncodeKey(0x50, KeyRepeat, 0x01), []byte{0x05, 0x00, 0x50, 0x02, 0x01}},
		{"key_text", EncodeKeyText("é😀"), append([]byte{0x06}, "é😀"...)},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: % x, want % x", c.name, c.got, c.want)
		}
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	m, err := Decode(EncodeMouseMove(-7, 12))
	if err != nil || m.Type != TypeMouseMove || m.MouseMove != (MouseMove{-7, 12}) {
		t.Fatalf("mouse_move: %+v %v", m, err)
	}
	m, err = Decode(EncodeMouseButton(4, true, 3))
	if err != nil || m.MouseButton != (MouseButton{4, true, 3}) {
		t.Fatalf("mouse_button: %+v %v", m, err)
	}
	m, err = Decode(EncodeScroll(15, -30, ScrollFlagMomentum))
	if err != nil || m.Scroll != (Scroll{15, -30, 2}) || m.Scroll.Discrete() {
		t.Fatalf("scroll: %+v %v", m, err)
	}
	m, err = Decode(EncodeKey(0xE3, KeyDown, 0x08))
	if err != nil || m.Key != (Key{0xE3, KeyDown, 0x08}) {
		t.Fatalf("key: %+v %v", m, err)
	}
	m, err = Decode(EncodeKeyText("hi"))
	if err != nil || m.KeyText != "hi" || !m.IsInput() {
		t.Fatalf("key_text: %+v %v", m, err)
	}
	ctl := MustControl(ActiveMsg{T: TActive, ID: "b", From: "a", Enter: &Enter{EdgeLeft, 0.5}, Buttons: 1, Modifiers: 2, Seq: 9})
	if ctl[0] != TypeControl {
		t.Fatalf("control type byte %x", ctl[0])
	}
	want := `{"t":"active","id":"b","from":"a","enter":{"edge":"left","frac":0.5},"buttons":1,"modifiers":2,"seq":9}`
	if string(ctl[1:]) != want {
		t.Fatalf("active JSON = %s", ctl[1:])
	}
	m, err = Decode(ctl)
	if err != nil || m.T != TActive || m.IsInput() {
		t.Fatalf("control: %+v %v", m, err)
	}
	var a ActiveMsg
	if err := m.ParseControl(&a); err != nil || a.Enter == nil || a.Enter.Edge != EdgeLeft || a.Seq != 9 {
		t.Fatalf("ParseControl: %+v %v", a, err)
	}
	// claim/active without enter omits the field
	cl := MustControl(ActiveMsg{T: TActive, ID: "b", From: "a", Seq: 1})
	if bytes.Contains(cl, []byte("enter")) {
		t.Fatalf("enter must be omitted: %s", cl)
	}
	// JSON control messages are never HTML-escaped
	cb := MustControl(ClipboardMsg{T: TClipboard, Kind: "text", Text: "<a&b>"})
	if !bytes.Contains(cb, []byte("<a&b>")) {
		t.Fatalf("clipboard escaped: %s", cb)
	}
}

func TestDecodeMalformed(t *testing.T) {
	bad := [][]byte{
		{},
		{0x02, 0x00},
		{0x03, 0x00, 0x02, 0x01},
		{0x04, 0x00, 0x00, 0x00},
		{0x05, 0x00, 0x04, 0x03, 0x00},
		{0x06, 0xFF, 0xFE},
		{0x01, '{', '}'},
		{0x01, 'x'},
	}
	for i, b := range bad {
		if _, err := Decode(b); !errors.Is(err, ErrMalformed) {
			t.Errorf("case %d (% x): err = %v, want ErrMalformed", i, b, err)
		}
	}
	if _, err := Decode([]byte{0x09, 1, 2}); !errors.Is(err, ErrUnknownType) {
		t.Errorf("unknown type: %v", err)
	}
}

func TestControlJSONShapes(t *testing.T) {
	// Field names of §6 as the Swift side will parse them.
	info := MustControl(InfoMsg{T: TInfo, Name: "n", Plat: "windows", Version: "1", Displays: []Display{{ID: "d", X: 1, Y: 2, W: 3, H: 4, Scale: 1.5, Primary: true}}})
	var m map[string]any
	if err := json.Unmarshal(info[1:], &m); err != nil {
		t.Fatal(err)
	}
	d := m["displays"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "x", "y", "w", "h", "scale", "primary"} {
		if _, ok := d[k]; !ok {
			t.Errorf("displays entry lacks %q: %v", k, d)
		}
	}
	lay := MustControl(LayoutMsg{T: TLayout, UpdatedAt: 5, Tiles: []Tile{{ID: "a", Col: 1, Row: 2}}})
	if string(lay[1:]) != `{"t":"layout","updatedAt":5,"tiles":[{"id":"a","col":1,"row":2}]}` {
		t.Errorf("layout JSON = %s", lay[1:])
	}
	ping := MustControl(PingMsg{T: TPing, TS: 123})
	if string(ping[1:]) != `{"t":"ping","ts":123}` {
		t.Errorf("ping JSON = %s", ping[1:])
	}
	claim := MustControl(ClaimMsg{T: TClaim, ID: "a", Seq: 3})
	if string(claim[1:]) != `{"t":"claim","id":"a","seq":3}` {
		t.Errorf("claim JSON = %s", claim[1:])
	}
}

func TestSwapGUICtrl(t *testing.T) {
	if SwapGUICtrl(0xE0) != 0xE3 || SwapGUICtrl(0xE3) != 0xE0 || SwapGUICtrl(0xE4) != 0xE7 || SwapGUICtrl(0xE7) != 0xE4 || SwapGUICtrl(0x04) != 0x04 {
		t.Error("SwapGUICtrl usages")
	}
	cases := map[uint8]uint8{0x01: 0x08, 0x08: 0x01, 0x10: 0x80, 0x80: 0x10, 0x02: 0x02, 0x09: 0x09, 0x0B: 0x0B, 0x05: 0x0C}
	for in, want := range cases {
		if got := SwapGUICtrlMods(in); got != want {
			t.Errorf("SwapGUICtrlMods(%#x) = %#x, want %#x", in, got, want)
		}
	}
}

func TestHotkeyMatch(t *testing.T) {
	cases := []struct {
		hk   string
		mods uint8
		want bool
	}{
		{"ctrl+alt", 0x05, true},  // LCtrl + LAlt
		{"ctrl+alt", 0x50, true},  // RCtrl + RAlt
		{"ctrl+alt", 0x14, true},  // RCtrl + LAlt
		{"ctrl+alt", 0x01, false}, // ctrl only
		{"ctrl+alt", 0x07, false}, // extra shift
		{"ctrl+alt", 0x00, false},
		{"cmd+shift", 0x0A, true},
		{"Ctrl + Shift", 0x03, true},
		{"", 0x05, false},
	}
	for _, c := range cases {
		if got := HotkeyMatches(c.hk, c.mods); got != c.want {
			t.Errorf("HotkeyMatches(%q, %#x) = %v", c.hk, c.mods, got)
		}
	}
}
