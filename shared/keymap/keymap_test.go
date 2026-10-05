package keymap

import "testing"

func TestKnownRows(t *testing.T) {
	m := Default()
	cases := []struct {
		dom     string
		wire    int
		mac     int
		winScan int
		winExt  bool
		page    int
	}{
		{"KeyA", 4, 0, 0x1E, false, 7},
		{"ArrowLeft", 0x50, 123, 0x4B, true, 7},
		{"MediaPlayPause", 0x10CD, -1, 0x22, true, 12},
		{"Enter", 40, 36, 0x1C, false, 7},
		{"NumpadEnter", 88, 76, 0x1C, true, 7},
		{"MetaLeft", 0xE3, 55, 0x5B, true, 7},
	}
	for _, tc := range cases {
		t.Run(tc.dom, func(t *testing.T) {
			k, ok := m.ByDom(tc.dom)
			if !ok {
				t.Fatalf("ByDom(%q) missing", tc.dom)
			}
			if k.Wire != tc.wire || k.Mac != tc.mac || k.WinScan != tc.winScan || k.WinExt != tc.winExt || k.Page != tc.page {
				t.Fatalf("row = %+v", k)
			}
			if w, ok := m.ByWire(tc.wire); !ok || w.Dom != tc.dom {
				t.Fatalf("ByWire(%#x) = %+v %v", tc.wire, w, ok)
			}
			if tc.mac >= 0 {
				if mk, ok := m.ByMac(tc.mac); !ok || mk.Dom != tc.dom {
					t.Fatalf("ByMac(%d) = %+v %v", tc.mac, mk, ok)
				}
			}
			if wk, ok := m.ByWinScan(tc.winScan, tc.winExt); !ok || wk.Dom != tc.dom {
				t.Fatalf("ByWinScan(%#x,%v) = %+v %v", tc.winScan, tc.winExt, wk, ok)
			}
		})
	}
}

func TestMisses(t *testing.T) {
	m := Default()
	if _, ok := m.ByWire(0x7FFF); ok {
		t.Fatal("unexpected hit for bogus wire")
	}
	if _, ok := m.ByDom("NoSuchKey"); ok {
		t.Fatal("unexpected hit for bogus dom")
	}
	if _, ok := m.ByMac(-1); ok {
		t.Fatal("mac -1 must never match")
	}
	if _, ok := m.ByWinScan(0, false); ok {
		t.Fatal("scan 0 must never match")
	}
	// Enter and NumpadEnter share scan 0x1C and differ only by the E0 prefix.
	a, _ := m.ByWinScan(0x1C, false)
	b, _ := m.ByWinScan(0x1C, true)
	if a.Dom != "Enter" || b.Dom != "NumpadEnter" {
		t.Fatalf("scan 0x1C: %q / %q", a.Dom, b.Dom)
	}
}

func TestModifiers(t *testing.T) {
	want := map[int]uint8{0xE0: 1, 0xE1: 2, 0xE2: 4, 0xE3: 8, 0xE4: 16, 0xE5: 32, 0xE6: 64, 0xE7: 128}
	for wire, bit := range want {
		got, ok := ModifierBit(wire)
		if !ok || got != bit {
			t.Fatalf("ModifierBit(%#x) = %d,%v want %d", wire, got, ok, bit)
		}
		if !IsModifier(wire) {
			t.Fatalf("IsModifier(%#x) false", wire)
		}
	}
	for _, wire := range []int{0, 4, 0xDF, 0xE8, 0x10CD} {
		if _, ok := ModifierBit(wire); ok || IsModifier(wire) {
			t.Fatalf("%#x wrongly treated as modifier", wire)
		}
	}
}

func TestTableIntegrity(t *testing.T) {
	m := Default()
	if n := len(m.Keys()); n < 150 {
		t.Fatalf("only %d keys", n)
	}
	for _, k := range m.Keys() {
		switch k.Page {
		case 7:
			if k.Wire != k.Usage {
				t.Fatalf("%s: page 7 wire %#x != usage %#x", k.Ident, k.Wire, k.Usage)
			}
		case 12:
			if k.Wire != 0x1000+(k.Usage&0xFFFF) {
				t.Fatalf("%s: page 12 wire %#x usage %#x", k.Ident, k.Wire, k.Usage)
			}
		case 1:
			if k.Wire != 0x2000+(k.Usage&0xFFFF) {
				t.Fatalf("%s: page 1 wire %#x usage %#x", k.Ident, k.Wire, k.Usage)
			}
		default:
			t.Fatalf("%s: unexpected page %d", k.Ident, k.Page)
		}
	}
	if _, err := Parse([]byte(`{"keys":[]}`)); err == nil {
		t.Fatal("empty table must fail")
	}
	if _, err := Parse([]byte(`{"keys":[{"wire":1},{"wire":1}]}`)); err == nil {
		t.Fatal("duplicate wire must fail")
	}
}
