# Edgehop wire protocol — version 1

Edgehop lets one physical mouse and keyboard drive several computers. Every
computer runs the same program; there is no fixed server. This document is the
contract between the Swift (macOS) and Go (Windows, Linux-test) implementations.
Both MUST interoperate byte-for-byte with it.

## 1. Model

* A **group** is the set of machines the user has paired with each other.
* Exactly one machine in the group is **active** at any time: it is the one the
  pointer and keyboard currently drive. At start every machine considers itself
  active until it learns otherwise.
* Any machine that observes **physical** input (a non-injected event) while it is
  not active forwards that input to the active machine. So a mouse attached to
  machine A and a keyboard attached to machine B both work, and "the keyboard
  follows the mouse" falls out naturally.
* The **active** machine watches its own cursor. When the cursor is pushed against
  an outer edge of its desktop that has a neighbour in the **layout**, control
  moves to that neighbour: the active machine broadcasts `active` naming the
  neighbour and where the cursor should appear.
* The **layout** is a grid of tiles (one tile per machine, no gaps required).
  Neighbours are tiles directly adjacent on the left/right/top/bottom. The layout
  is edited on any machine and broadcast; the newest `updatedAt` wins.
* Machines that are not active keep their own cursor still and hidden (best
  effort) so the user does not see a second pointer drifting.

## 2. Identity, discovery, connections

* Each machine generates a long-term X25519 key pair on first run. `machineId` is
  the lowercase hex of the first 16 bytes of SHA-256(static public key).
* Discovery: Bonjour/mDNS service type `_edgehop._tcp`, instance name = the
  machine's display name, TXT record keys: `v=1`, `id=<machineId>`,
  `name=<display name>`, `plat=macos|windows|linux`, `pk=<base64 static pubkey>`.
* Every machine listens on a TCP port (default 24800, else any free port; the
  port is what Bonjour advertises).
* One TCP connection per pair of machines. The machine with the lexicographically
  smaller `machineId` is the **initiator** and connects; the other **accepts**. If
  the accepting side sees a second connection from the same peer it closes the
  older one. The initiator reconnects with exponential backoff (1 s … 30 s).
* Peers are remembered after pairing (id, name, static pubkey, last host:port) in
  the config file, so a machine reconnects even if Bonjour is flaky.

## 3. Framing

All traffic is a stream of frames:

```
u32 big-endian length N   (N ≤ 1 MiB)
N bytes payload
```

Before the handshake completes, payload = plaintext message (section 4).
After it, payload = `ChaCha20-Poly1305(key_dir, nonce_dir, plaintext, aad="")`
where plaintext = `u8 type || body` (section 5). Tag is the 16-byte suffix as
produced by the IETF construction (12-byte nonce), i.e. what CryptoKit's
`ChaChaPoly.seal` and Go's `chacha20poly1305.New` produce.

Nonce: 12 bytes = 4 zero bytes || u64 big-endian counter. Each direction has its
own key and counter; the counter starts at 0 after the handshake and increments
by one per frame. A receiver MUST close the connection on a decryption failure
or a counter wrap.

## 4. Handshake

Plaintext frames carry UTF-8 JSON objects with a `t` field.

```
initiator → acceptor   {"t":"hello","v":1,"id":"…","name":"…","plat":"macos",
                        "spk":"<b64 static pub>","epk":"<b64 ephemeral pub>",
                        "pair":true|false}
acceptor  → initiator  {"t":"hello_ack","v":1,"id":"…","name":"…","plat":"windows",
                        "spk":"<b64>","epk":"<b64>","pair":true|false}
```

`pair` is true when that side does not yet trust the other's static key and is
willing to pair now (the user initiated pairing in the UI). If either side's
`pair` is true the handshake is a **pairing handshake** and a 6-digit code is
involved; otherwise it is a **trusted handshake**.

Key derivation (both sides compute the same values; `X25519(priv, pub)`):

```
dh1 = X25519(e_init, e_acc)          ephemeral–ephemeral
dh2 = X25519(s_init, e_acc)          initiator static – acceptor ephemeral
dh3 = X25519(e_init, s_acc)          initiator ephemeral – acceptor static
psk = pairing ? HKDF-SHA256(ikm = UTF8(code), salt = "edgehop-pair", info = min(id)||max(id), 32)
              : 32 zero bytes
transcript = SHA-256( "edgehop-v1" || hello_bytes || hello_ack_bytes )   (exact JSON bytes as sent)
okm = HKDF-SHA256(ikm = dh1||dh2||dh3||psk, salt = transcript, info = "edgehop-v1-keys", 64)
key_init→acc = okm[0..32]      key_acc→init = okm[32..64]
```

Then both sides switch to encrypted frames. The initiator sends encrypted
`{"t":"auth"}`; the acceptor replies encrypted `{"t":"auth_ok"}` and, if this
was a pairing handshake, stores the initiator's static key as trusted (and vice
versa when the initiator decrypts `auth_ok`). A side that cannot decrypt the
first encrypted frame closes the connection and reports `pair_failed` to its UI.

The 6-digit code is generated by the **acceptor** when it receives a `hello`
with `pair:true` from an unknown key and shown in its UI for 120 s; the user
types it on the initiator. Rate limit: 5 failed pairings per peer per 10 minutes.

A side that receives `hello` from an **unknown** static key with `pair:false`
replies `{"t":"error","code":"unpaired"}` and closes.

## 5. Encrypted messages

`type` byte then body. Multi-byte integers are big-endian.

| type | name | body |
|---|---|---|
| 0x01 | control | UTF-8 JSON object with `t` (section 6) |
| 0x02 | mouse_move | `i16 dx, i16 dy` relative, in the sender's pixels |
| 0x03 | mouse_button | `u8 button (0 left, 1 right, 2 middle, 3 back, 4 forward), u8 state (1 down, 0 up), u8 clickCount` |
| 0x04 | scroll | `i16 dx, i16 dy, u8 flags` — units: 120 = one wheel notch; continuous (trackpad) deltas are pixels; `flags bit0` = discrete wheel, `bit1` = momentum phase |
| 0x05 | key | `u16 hidUsage, u8 state (1 down, 0 up, 2 repeat), u8 modifiers` — `modifiers` is the HID modifier byte (bit0 LCtrl, bit1 LShift, bit2 LAlt, bit3 LGUI, bit4 RCtrl, bit5 RShift, bit6 RAlt, bit7 RGUI); modifier keys are ALSO sent as their own key events (usage 0xE0…0xE7) |
| 0x06 | key_text | UTF-8 text to type verbatim (used for characters the key table cannot express, e.g. emoji, dead-key results) |

Key usages are USB HID usage page 0x07 IDs; media keys use page 0x0C IDs with
`0x1000` added (so play/pause = 0x10CD). The canonical table is
`shared/keymap/hid-keymap.json`. The receiver maps HID → its native keycode and
injects that; the OS applies the receiver's keyboard layout. Senders MUST NOT
translate characters; they send physical keys.

Optional modifier swap: when `swapCmdCtrl` is enabled on the receiver and the
peer is a different platform, GUI ⇄ Ctrl are exchanged at injection time (so ⌘C
on a Mac keyboard becomes Ctrl+C on the Windows machine). Default: enabled for
cross-platform pairs.

## 6. Control messages (type 0x01)

| `t` | direction | fields |
|---|---|---|
| `auth` / `auth_ok` | first encrypted frames (section 4) | — |
| `info` | both, right after auth | `name, plat, version, displays: [{id, x, y, w, h, scale, primary}]` — desktop union coordinates, origin top-left, y down (macOS implementations convert from AppKit's flipped coordinates) |
| `displays` | both, on change | same `displays` field |
| `layout` | both | `{updatedAt (ms epoch), tiles: [{id, col, row}]}` newest wins; receivers persist it |
| `active` | the machine handing over control → **every** connected peer | `{id: newActive, from: previousActive, enter: {edge: "left"\|"right"\|"top"\|"bottom", frac: 0..1}, buttons: u8 bitmask, modifiers: u8, seq: u32}` — `enter.edge` is the edge of the **new** machine the cursor arrives on; `frac` is the position along that edge (0 = top/left). `seq` increases per transition; a receiver ignores an `active` with a `seq` lower than the last one seen from any peer |
| `claim` | a non-active machine asking to become active (hotkey, menu) → every peer | `{id, seq}` — treated like `active` without `enter` |
| `clipboard` | the machine losing control → the one gaining it | `{kind: "text", text}` (v1: text only; `kind:"png"` + `b64` reserved) |
| `ping` / `pong` | both, every 5 s | `{ts}` — a peer silent for 15 s is considered gone; if it was active, the machine with the physical pointer that last forwarded to it becomes active again |
| `secure_input` | active → peers | `{on: bool}` macOS secure-input mode: senders show a hint that keystrokes are blocked |
| `error` | either | `{code, message}` then close |

### 6.1 Transition rules (on the active machine)

1. Compute the desktop union `U` (bounding box of all displays) and the per-display
   rectangles.
2. After every pointer event, let `p` be the cursor position. If `p.x ≤ U.minX`
   and the layout has a left neighbour **and** a display actually owns the row
   `p.y` at `U.minX` (no gaps), start the switch timer for edge `left`. Same for
   the other three edges. Corners: ignore hits within `edgeCornerDeadZonePx`
   (default 32) of a corner (Hot Corners on macOS live there).
3. The pointer must remain on the edge for `switchDelayMs` (default 0, user
   configurable) with no button held, then: broadcast `active` with
   `enter.frac = (p.y − U.minY)/U.height` for left/right (or x fraction for
   top/bottom) and the current button/modifier state; stop treating this machine
   as active; begin forwarding.
4. The newly active machine places its cursor at the opposite edge at the same
   fraction of ITS union, inset by 1 px, un-hides it, and raises any modifiers
   that the message said were held (so drag-across works).
5. The machine that still owns the physical mouse (the one receiving raw
   hardware events) locks its cursor at the centre of its primary display and
   hides it while it is not active. While forwarding it sends `mouse_move`
   deltas taken from the raw event (macOS `kCGMouseEventDeltaX/Y`; Windows
   re-centre deltas or Raw Input).

### 6.2 Hotkeys

Default `Ctrl+Alt+←/→/↑/↓` (configurable) moves control to the neighbour in that
direction from the active machine, placing the cursor in the centre of the new
machine. `Ctrl+Alt+Home` returns control to the machine the hotkey was pressed
on (`claim`).

## 7. Safety and limits

* Frames > 1 MiB, unknown `type` bytes, or malformed JSON → `error` + close.
* Input is only injected from authenticated peers; machines ignore
  `mouse_*`/`key*` frames while they are not active except within 250 ms after an
  `active` naming them (clock skew tolerance).
* Keyboard injection pauses while the local secure-input flag is on (macOS); the
  sender is told via `secure_input`.
* No message ever carries typed *characters* except `key_text`, which is only
  used for text the key table cannot express.
* Pairing codes are never logged.

## 8. Test vectors

`shared/testdata/edgehop/` holds JSON fixtures produced by the Go implementation
(`go test ./shared/go/securechan -update`): handshake transcripts with fixed
ephemeral keys and the expected derived keys and first ciphertexts. The Swift
Linux test package consumes the same fixtures. Both suites must pass.
