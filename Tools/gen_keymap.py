#!/usr/bin/env python3
"""
Generate shared/keymap/hid-keymap.json from Chromium's dom_code_data.inc.

    Tools/gen_keymap.py [Tools/dom_code_data.inc] [shared/keymap/hid-keymap.json]

dom_code_data.inc (ui/events/keycodes/dom/, BSD licence) is the one public table
that lists, per key, the USB HID usage, the Windows Set-1 scan code (0xE0xx for
extended keys), the macOS virtual key code (kVK_*) and the DOM `code` name.
Edgehop/Reach send USB HID usages on the wire; each platform maps them to its
native code with this table. Media keys (usage page 0x0C) are kept with their
full 24-bit usage so the wire value is `0x1000 + usage` as the protocol says.

The output is deterministic; re-running on an unchanged input yields an
identical file.
"""
import json
import re
import sys

SRC = sys.argv[1] if len(sys.argv) > 1 else "Tools/dom_code_data.inc"
DST = sys.argv[2] if len(sys.argv) > 2 else "shared/keymap/hid-keymap.json"

# Consumer-page keys we carry on the wire (page 0x0C). Everything else on that
# page is dropped to keep the table small.
CONSUMER_KEEP = {
    0x0C00B5, 0x0C00B6, 0x0C00B7, 0x0C00CD, 0x0C00E2, 0x0C00E9, 0x0C00EA,
    0x0C006F, 0x0C0070,  # brightness up/down
    0x0C0192, 0x0C0194, 0x0C0221, 0x0C0223, 0x0C0224, 0x0C0225, 0x0C0226,
    0x0C0227, 0x0C022A, 0x0C018A, 0x0C0183,
}
# System-page keys (page 0x01): power/sleep/wake — informational.
SYSTEM_KEEP = {0x010081, 0x010082, 0x010083}

text = open(SRC, encoding="utf-8").read()
# Join multi-line DOM_CODE(...) invocations, drop comments.
text = re.sub(r"//[^\n]*", "", text)
entries = re.findall(r"DOM_CODE\(\s*([^)]*?)\)", text, flags=re.S)

rows = []
for e in entries:
    parts = [p.strip() for p in e.split(",")]
    if len(parts) != 7:
        continue
    usb, evdev, xkb, win, mac, code, ident = parts
    usb, evdev, xkb, win, mac = (int(x, 16) for x in (usb, evdev, xkb, win, mac))
    page = usb >> 16
    if page == 0x07:
        keep = 0x070004 <= usb <= 0x0700E7
    elif page == 0x0C:
        keep = usb in CONSUMER_KEEP
    elif page == 0x01:
        keep = usb in SYSTEM_KEEP
    else:
        keep = False
    if not keep:
        continue
    dom = None if code == "NULL" else code.strip('"')
    win_ext = (win & 0xFF00) == 0xE000
    rows.append({
        "usage": usb if page != 0x07 else (usb & 0xFFFF),
        "page": page,
        "dom": dom,
        "ident": ident,
        "mac": -1 if mac == 0xFFFF else mac,
        "winScan": win & 0xFF if win else 0,
        "winExt": win_ext,
        "evdev": evdev,
    })

rows.sort(key=lambda r: (r["page"], r["usage"]))

# Wire value: page 0x07 → usage; page 0x0C → 0x1000 + usage; page 0x01 → 0x2000 + usage.
for r in rows:
    if r["page"] == 0x07:
        r["wire"] = r["usage"]
    elif r["page"] == 0x0C:
        r["wire"] = 0x1000 + (r["usage"] & 0xFFF)
    else:
        r["wire"] = 0x2000 + (r["usage"] & 0xFFF)

out = {
    "source": "chromium/ui/events/keycodes/dom/dom_code_data.inc (BSD-3-Clause)",
    "notes": [
        "usage: USB HID usage ID; for page 0x07 the 16-bit usage, for other pages the full 24-bit (page<<16|usage).",
        "wire: the value sent in Edgehop key frames and used by Reach key names: page 7 = usage, page 0x0C = 0x1000+usage, page 0x01 = 0x2000+usage.",
        "mac: macOS virtual key code (kVK_*), -1 when the key has none (media keys use NX_KEYTYPE events instead).",
        "winScan: Windows Set-1 scan code byte; winExt true means the E0 prefix (KEYEVENTF_EXTENDEDKEY).",
        "modifiers byte on the wire: bit0 LCtrl, bit1 LShift, bit2 LAlt, bit3 LGUI, bit4 RCtrl, bit5 RShift, bit6 RAlt, bit7 RGUI (usages 0xE0..0xE7).",
    ],
    "keys": rows,
}
with open(DST, "w", encoding="utf-8") as f:
    json.dump(out, f, indent=1)
    f.write("\n")
print(f"wrote {DST}: {len(rows)} keys "
      f"({sum(1 for r in rows if r['page'] == 7)} keyboard, "
      f"{sum(1 for r in rows if r['page'] == 0x0C)} consumer, "
      f"{sum(1 for r in rows if r['page'] == 1)} system); "
      f"{sum(1 for r in rows if r['mac'] >= 0)} have mac codes, "
      f"{sum(1 for r in rows if r['winScan'])} have win scancodes")
