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
  an outer edge of its desktop at a point where another machine's screen is on the
  other side (a **boundary**), control moves to that machine: the active machine
  broadcasts `active` naming it and where the cursor should appear.
* The **layout** is one shared **world canvas**. Every machine reports its displays
  in `info`/`displays` in native desktop units (macOS points, Windows pixels),
  origin top-left, y down, the primary display's top-left at (0,0). The layout
  places each machine on the canvas by an integer offset
  `placement = {id, x, y}`; the world rect of display `d` of machine `m` is
  `(m.x + d.x, m.y + d.y, d.w, d.h)`. A machine's displays move together — their
  relative arrangement belongs to the OS (System Settings ▸ Displays, Windows
  Display settings) and Edgehop never moves one monitor alone.
  * A layout is **valid** when no two displays of *different* machines overlap
    (positive-area intersection), no machine id appears twice, and
    `|x|, |y| ≤ 1 000 000`.
  * The canvas is to scale in native units: there is no DPI normalisation between
    machines, and boundaries are simply where the user made screens touch.
  * `tiles` (the v1 5×5 grid, one tile per machine) is still carried alongside
    `machines` so a v1 peer keeps working. See §6 `layout`.
  * The layout is edited on any machine and broadcast; the newest `updatedAt` wins.
* **Boundaries.** Two displays A (machine M) and B (machine N, N ≠ M) touch on A's
  right edge when `|A.right − B.left| ≤ 2` in world units (right = x + w) and their
  y ranges overlap by at least 1; the **boundary segment** is that y overlap. Same
  for left/top/bottom with the roles of x and y swapped. The set of segments — per
  machine, per local display, per edge — follows from the layout plus every
  machine's last known displays, and is recomputed when either changes. Peers'
  last known displays are persisted, so an offline machine still occupies the
  canvas and its boundaries still exist.
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
| 0x04 | scroll | `i16 dx, i16 dy, u8 flags` — units: 120 = one wheel notch; continuous (trackpad) deltas are pixels; positive `dy` = wheel rotated forward (away from the user), positive `dx` = right; `flags bit0` = discrete wheel, `bit1` = momentum phase |
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
| `info` | both, right after auth | `name, plat, version, port?, displays: [{id, x, y, w, h, scale, primary}]` — desktop union coordinates, origin top-left, y down (macOS implementations convert from AppKit's flipped coordinates); `port` is the sender's listening TCP port so a peer first reached by a typed address can be re-dialled |
| `displays` | both, on change | same `displays` field |
| `layout` | both | `{updatedAt (ms epoch), machines: [{id, x, y}], tiles: [{id, col, row}]}` newest wins; receivers persist it. A v2 sender fills **both**: `machines` is the canvas (§1), `tiles` is derived from it for v1 peers. A receiver that sees `machines` uses it and ignores `tiles`; a receiver that sees only `tiles` (a v1 peer, or an old config file) converts and is v2 from then on — see the conversion rules below |
| `active` | the machine handing over control → **every** connected peer | `{id: newActive, from: previousActive, enter: {edge: "left"\|"right"\|"top"\|"bottom", frac: 0..1, pt?: {x, y}}, buttons: u8 bitmask, modifiers: u8, seq: u32}` — `enter.edge` is the edge of the **new** machine the cursor arrives on; `frac` is the position along that edge (0 = top/left), kept for v1 receivers. `enter.pt` is the exact entry point in the **new** machine's own display coordinates: a receiver that understands it places the cursor there (clamped/snapped onto its displays) and ignores `frac`. `seq` increases per transition; a receiver ignores an `active` with a `seq` lower than the last one seen from any peer |
| `claim` | a non-active machine asking to become active (hotkey, menu) → every peer | `{id, seq}` — treated like `active` without `enter` |
| `clipboard` | the machine losing control → the one gaining it | `{kind: "text", text}` (v1: text only; `kind:"png"` + `b64` reserved) |
| `ping` / `pong` | both, every 5 s | `{ts}` — a peer silent for 15 s is considered gone; if it was active, the machine with the physical pointer that last forwarded to it becomes active again |
| `locate` | the machine with the physical mouse → the active machine | `{}` (no fields) — show the shake-to-locate overlay at the receiver's current cursor (§6.3). Gated by the sender's `shakeToLocate` setting |
| `secure_input` | active → peers | `{on: bool}` macOS secure-input mode: senders show a hint that keystrokes are blocked |
| `error` | either | `{code, message}` then close |

### 6.1 Transition rules (on the active machine)

1. Compute the desktop union `U` (bounding box of all displays) and the per-display
   rectangles.
2. After every pointer event, let `p` be the cursor position on local display `A`
   of the active machine `M`. `p` must sit on an **outer** edge of `M`'s desktop at
   that point — `p.x ≤ U.minX` for `left`, the last pixel column for `right`, and
   so on — with a display actually owning the row `p.y` (or column `p.x`) there, no
   gaps. Corners: ignore hits within `edgeCornerDeadZonePx` (default 32) of either
   end of the merged run of displays on that edge (Hot Corners on macOS live
   there).
   Then convert to world coordinates, `w = (M.x + p.x, M.y + p.y)`, and look for a
   boundary segment (§1) on `A`'s matching edge whose span contains `w`'s
   coordinate along that edge. Additionally ignore hits within
   `edgeCornerDeadZonePx` of either end of that *segment*, but only where the end
   is also an end of `M`'s merged run. No segment → no hop: the cursor simply
   stops, as the OS clamps it.
3. The pointer must remain on the edge for `switchDelayMs` (default 0, user
   configurable) with no button held, then: broadcast `active` naming the machine
   `N` that owns the segment, with the current button/modifier state,
   `enter.frac = (p.y − U.minY)/U.height` for left/right (or the x fraction for
   top/bottom, for v1 receivers) **and** `enter.pt = q`, the entry point in `N`'s
   own coordinates: `q = w − (N.x, N.y)`, then placed on the facing edge of the
   target display `B`, inset 1 px (hopping right: `q.x = B.x + 1`; hopping left:
   `q.x = B.x + B.w − 2`; and the y equivalents), with the along-edge coordinate
   clamped into `B`. Then stop treating this machine as active and begin
   forwarding.
4. The newly active machine places its cursor at `enter.pt` (clamped and snapped
   onto one of its displays), or, when the message has no `pt`, at the opposite
   edge at the same fraction of ITS union inset by 1 px; it un-hides the cursor
   and raises any modifiers that the message said were held (so drag-across
   works).
5. The machine that still owns the physical mouse (the one receiving raw
   hardware events) locks its cursor at the centre of its primary display and
   hides it while it is not active. While forwarding it sends `mouse_move`
   deltas taken from the raw event (macOS `kCGMouseEventDeltaX/Y`; Windows
   re-centre deltas or Raw Input).

### 6.2 Hotkeys

Default `Ctrl+Alt+←/→/↑/↓` (configurable) moves control in that direction from the
active machine. The target is:

1. the machine sharing a boundary with the active machine on that side — the one
   whose segment is nearest the cursor along the edge, ties going to the widest
   segment (with no known cursor, e.g. when the active machine is remote, the
   widest segment wins);
2. otherwise the machine whose world-union centre is nearest in that direction
   within a ±60° cone;
3. otherwise nothing happens.

The cursor is placed in the centre of the new machine's primary display (no
`enter`). `Ctrl+Alt+Home` returns control to the machine the hotkey was pressed
on (`claim`).

### 6.3 Resolutions (normative; both implementations follow these)

* `seq`: every node keeps the maximum it has seen; `claim` and `active` both increment it; a message with an equal `seq` is accepted, only a smaller one is stale.
* Entry point: `enter.pt` when the receiver understands it (clamped and snapped onto one of its displays); otherwise the opposite edge at the same union fraction, inset 1 px, and if no display owns that point, the nearest point of any display.
* Layout conversion (both directions, deterministic):
  * `machines` → `tiles`: machines whose world unions overlap vertically form one row; rows are ordered top to bottom; within a row the column is the rank by x centre (ties by id). It only has to preserve left/right and up/down roughly.
  * `tiles` → `machines`: for each row in ascending order, place the machines left to right by column with each world union touching the previous one's right edge, the row's top at the cumulative height of the rows above it. A machine whose displays are not known yet counts as 1920×1080.
  * A machine whose displays are unknown contributes no display to the canvas, so it can never overlap another machine.
* An unknown control `t` MUST be **ignored** (no `error`, no close) so later additions such as `locate` are additive. This is the opposite of an unknown frame **type** byte, which is still an `error` + close (§7).
* Edge geometry: the right/bottom edge is the last pixel column/row (where the OS clamps the cursor); left/top is `≤ minX/minY`. The corner dead zone is measured along the merged run of displays that touch that edge, so a join between two monitors on one edge is not a corner.
* Re-entry guard: after becoming active through `enter.edge`, that edge cannot hand control back until the cursor has moved 40 px (`ReentryGuardPx`) inward from the arrival point, so a wobble or a stray first delta does not bounce control.
* Warp in the first delta (macOS sender): parking the cursor at the centre can be folded into the next reported mouse delta; the sender removes it from the first forwarded delta that clearly contains it (same sign, at least half its size, within 0.5 s).
* The machine that was active locks and hides its cursor when control leaves it. A machine that is not active also locks and hides its own cursor the first time it forwards its OWN pointer input (move, button, scroll) to the active machine, so its pointer does not keep moving on its own screen while its clicks go elsewhere; keyboard-only forwarding locks nothing. It unlocks when it becomes active. (macOS: the park is re-asserted whenever the parked cursor drifts, because the OS can re-attach it when another app activates.)
* Touch to reclaim (`touchToReclaim` setting, default on): a machine that is **not** active and **not** driving takes control back as soon as its OWN pointer is used — mouse travel of `|dx|+|dy| ≥ 24` adding up within a 500 ms window, or any physical button-down, which reclaims at once. It becomes active through the normal `claim` path; the triggering events are swallowed (never forwarded), and the matching up of a swallowed down is swallowed too, so no machine sees an unmatched release. Keyboard input never reclaims: it keeps following the pointer.
* Driving: a machine is *driving* while its own input is what moves the active machine's pointer. It starts driving when it hands control away from itself and whenever it forwards physical mouse, button or scroll input (a translated trackpad gesture counts too). An `active` or `claim` from another machine that moves control elsewhere ends it, **unless** this machine forwarded physical pointer input to the machine control is leaving within the last 750 ms (a chained hop its own hand caused: it keeps driving and keeps forwarding).
* Placement on a claim: a machine that takes control back itself (touch to reclaim, Ctrl+Alt+Home, `claim`) puts the pointer back where it was on its own screen when control left it — the point recorded when it parked or handed over, snapped onto its current displays — and only centres it when no such point is known. That point usually sits on the edge the pointer was pushed against, so the re-entry guard is armed for that edge as if control had arrived through it. An `active` with no `enter` (a hand-over directed from the UI or by hotkey) still centres the pointer.
* Peer vanished (15 s silence or a closed socket): control returns to a machine only if it had forwarded to or handed to that peer (it then sends `claim`); otherwise it reverts to itself silently.
* Clipboard is sent only by the hand-over side, to the new active machine, only when it holds non-empty text.
* Shake to locate: the machine receiving the **physical** mouse events watches the raw deltas, whether or not it is forwarding. Within a sliding 600 ms window, 4 or more reversals of horizontal direction where each stroke between reversals travels 40 units or more fire a locate; then 1500 ms of cooldown. If that machine is itself active: macOS does nothing (it has its own shake-to-locate), Windows shows the overlay locally. If it is forwarding, it sends `{"t":"locate"}` to the active machine, which shows the overlay at its own cursor. The overlay is borderless, click-through, topmost, on all Spaces/desktops, about 1 s long: a ring (6 px stroke in the accent colour with a 2 px white outline) shrinking from radius 140 to 28 around the cursor and following it, then fading out; one overlay at a time. The whole feature is gated by the `shakeToLocate` setting (default on).
* `hello.pair = true` from an unknown key always gets a code (`hello_ack.pair = !known`); the code is shown only on the acceptor.
* A trusted dial to an acceptor that does not trust the initiator yields `{"t":"error","code":"unpaired"}`; the initiator reports "pair again".

## 7. Safety and limits

* Frames > 1 MiB, unknown `type` bytes, or malformed JSON → `error` + close.
  An unknown control `t` is **not** malformed: it is ignored silently (§6.3), so
  that control messages can be added without breaking older peers.
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
