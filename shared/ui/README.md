# shared/ui — the web UI kit

Plain CSS and ES modules shared by the Vitals, Edgehop and Reach UIs. No
framework, no bundler, no CDN, no web fonts: the same files render inside
WKWebView (macOS), WebView2 (Windows) and phone browsers.

```
shared/ui/
  native.js   the bridge to the native backend (docs/ARCHITECTURE.md §4) — do not change its API
  base.css    design tokens, reset, layout primitives, components
  charts.js   Sparkline, LineChart, RingGauge, BarMeter + number formatters
  ui.js       DOM helpers: h(), tabs(), toast(), confirmDialog(), …
  demo.html   kitchen-sink page: every component and chart with fake live data
  demo.js     drives demo.html (also the Playwright target, tests/ui/01-ui-kit.test.mjs)
```

Open the demo with `node tests/ui/run.mjs` running, or serve the repo root with
any static server and visit `/shared/ui/demo.html`. Its source is the markup
reference for every component.

## Including the kit from an app

`Tools/build_mac_app.sh` copies `shared/ui/` into
`Contents/Resources/ui/shared/`, and the Go/Swift servers mount the same folder
at `/shared/`. An app's `index.html` therefore always references the kit with
`./shared/…` relative paths:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="color-scheme" content="light dark">
  <link rel="stylesheet" href="./shared/base.css">
  <link rel="stylesheet" href="./app.css">
</head>
<body>
  <main class="page stack gap-4" id="app"></main>
  <script type="module" src="./app.js"></script>
</body>
</html>
```

```js
import { native } from './shared/native.js';
import { Sparkline, LineChart, formatBytes } from './shared/charts.js';
import { h, mount, toast, applyStoredTheme } from './shared/ui.js';

applyStoredTheme();                       // re-apply a pinned light/dark choice before first paint
const cpu = new Sparkline(document.querySelector('#cpu'), { max: 100, seconds: 120 });
native.on('vitals.sample', (s) => cpu.push(s.cpu.total));
```

## base.css

### Tokens (`:root`)

| Group | Variables |
|---|---|
| Type | `--font-sans`, `--font-mono`, `--text-xs` (11) … `--text-3xl` (34), `--leading` |
| Spacing | `--space-1` (4) … `--space-8` (64), `--gutter` (16), `--page-max` |
| Shape | `--radius-xs` … `--radius-xl`, `--radius-full` |
| Controls | `--control-h` (28; 34 on coarse pointers), `--control-h-sm`, `--control-h-lg`, `--toggle-w/h` |
| Elevation | `--shadow-sm`, `--shadow-md`, `--shadow-lg`, `--shadow-btn` |
| Surfaces & text | `--bg`, `--surface`, `--surface-2`, `--surface-3`, `--border`, `--border-strong`, `--text`, `--text-muted`, `--text-faint`, `--text-on-accent`, `--backdrop` |
| Accent & status | `--accent`, `--accent-hover`, `--accent-soft`, `--accent-ring`; `--success`, `--warning`, `--danger` each with `-ink` (text step) and `-soft` (tinted background) |
| Chart palette | `--chart-1` … `--chart-8` (categorical, **fixed order**), `--chart-seq-1` … `--chart-seq-7` (one-hue ramp), `--chart-grid`, `--chart-axis`, `--chart-muted`, `--chart-surface`, `--chart-text` |

Dark mode: values swap under `@media (prefers-color-scheme: dark)` unless
`<html data-theme="light">` pins light, and `<html data-theme="dark">` pins dark
regardless of the OS. `color-scheme` follows, so native form controls,
scrollbars and `<dialog>` backdrops match. Touch devices (`pointer: coarse`)
get taller controls and a bigger toggle automatically.

The categorical chart colors come from the data-viz palette and were validated
for colour-vision-deficiency separation and contrast on both surfaces. Keep the
order: slot N is always `--chart-N`, a series keeps its colour when others are
hidden, and never generate a ninth hue (fold into "Other" instead). Status
colours (`--success/--warning/--danger`) are reserved for state and always ship
with a label or icon, never colour alone.

### Layout

| Class | What |
|---|---|
| `.page` | centred column, 16 px gutters (grows with `env(safe-area-inset-*)`), `--page-max` width |
| `.toolbar` | sticky app header (`.toolbar-title`) that respects the top safe area |
| `.stack` / `.row` / `.cluster` | flex column / flex row (centred) / wrapping row |
| `.grid` | `repeat(auto-fit, minmax(--grid-min, 1fr))`; set `--grid-min` inline (default 220 px) |
| `.gap-0` … `.gap-6` | gap for any primitive (sets `--gap`) |
| `.grow`, `.shrink-0`, `.wrap`, `.items-*`, `.justify-*`, `.ml-auto`, `.w-full`, `.divider`, `.sr-only`, `.hide-phone`, `.only-phone` | utilities |
| `.title`, `.subtitle`, `.section-title`, `.text-muted/faint/accent/success/warning/danger`, `.text-xs … lg`, `.mono`, `.tabular`, `.truncate`, `.weight-500/600` | type utilities |

### Components

| Component | Markup |
|---|---|
| Card | `.card` with optional `.card-header` (`.card-title`, `.card-subtitle`), `.card-footer`; `.card.is-flush` + `.card-section` for edge-to-edge content |
| Button | `.btn` + `.btn-primary` / `.btn-secondary` / `.btn-danger` / `.btn-ghost`; sizes `.btn-sm` / `.btn-lg`; `.btn-icon`, `.btn-block`, `.btn-group` |
| Input | `<input class="input">`, `textarea.input`, `.input-sm` / `.input-lg`, `.input-group`; wrap in `.field` with `.label` and `.hint` (`.field.is-invalid`) |
| Select | `<span class="select"><select>…</select></span>` (`.select-sm`, `.is-block`) |
| Toggle | `<label class="toggle"><input type="checkbox" role="switch"><span class="toggle-track"></span><span>Label</span></label>` (`.toggle-sm`, `.toggle-lg`) |
| Checkbox | `<label class="check"><input type="checkbox"> Label</label>` |
| Slider | `<input type="range" class="slider">`; `.slider-row` + `.slider-value` for end labels. `sliderFill(input)` from ui.js keeps the filled track (`--slider-fill`) in sync |
| Segmented | `<div class="segmented"><button data-value="a" aria-pressed="true">A</button>…</div>` (wire with `segmented()`), or `<label><input type="radio">A</label>` children; `.is-block`, `.segmented-lg` |
| Tabs | `.tabs` > `button.tab[data-tab][data-panel=id]` + `.tab-panel` elements (wire with `tabs()`); `.tabs.is-pills` |
| Badge | `.badge` + `.badge-accent/success/warning/danger/outline`, `.badge-dot`, `.badge-lg` |
| Kbd | `<span class="kbd">⌘</span>` (plain `<kbd>` is styled too) |
| Table | `.table-wrap` (scrolls; `--table-max-h`) > `table.table`; sticky `<th>`, 28 px rows, `.num` for right-aligned numbers, `th[aria-sort]`, `.is-striped`, `tr[aria-selected]` |
| Dialog | `<dialog class="dialog">` > `.dialog-body` (`.dialog-title`, `.dialog-text`) + `.dialog-actions`; `.is-wide`. See `confirmDialog()` |
| Sheet | `<dialog class="sheet">` > `.sheet-grabber`, `.sheet-title`, `.sheet-body`, `.sheet-actions` — bottom sheet with safe-area padding, `showModal()` to open |
| Toast | created by `toast()`; `.toast-region` > `.toast.toast-{kind}` |
| Stat | `.stat` > `.stat-label`, `.stat-value` (+ `.stat-unit`), `.stat-delta.is-up/.is-down` × `.is-good/.is-bad`, `.stat-trend` (holds a Sparkline canvas); `.stat-hero` |
| Meter | `<div class="meter" style="--value: 62%"><span class="meter-label">…</span><span class="meter-bar"><span class="meter-fill"></span></span><span class="meter-text">62%</span></div>`; `.meter-success/warning/danger`, `.meter-lg` |
| Settings list | `.settings` > `.setting` rows: `.setting-main` (`.setting-label`, `.setting-desc`) + `.setting-control` |
| Empty state | `.empty-state` > `.empty-state-icon`, `.empty-state-title`, `.empty-state-text`, a `.btn` |
| Chart box | `.chart` (`--chart-h`) > `<canvas>`; charts.js adds `.chart-legend` / `.chart-tooltip`; `.gauge` (`--gauge-size`) wraps a RingGauge canvas; `.barmeter` is built by BarMeter |

Focus rings use `:focus-visible` only; animations collapse under
`prefers-reduced-motion`. Text is not selectable by default (native feel) except
in inputs, code, tables and anything with `.selectable`.

## charts.js

Canvas charts that are devicePixelRatio-aware, resize themselves
(ResizeObserver), coalesce redraws onto one `requestAnimationFrame`, allocate
nothing in their draw loops, and read their colours from CSS variables at draw
time (so they re-skin when the theme or a variable changes). Pass colours as a
CSS colour, a custom property name (`'--chart-3'`) or `var(--chart-3)`.

```js
import { Sparkline, LineChart, RingGauge, BarMeter, formatBytes, formatPercent, formatDuration } from './shared/charts.js';
```

| API | Notes |
|---|---|
| `new Sparkline(canvas, {max, min=0, color, fill=true, seconds, interval=1000, capacity=120, lineWidth=1.5, marker=true})` | no axes; autoscales when `max` is omitted; `seconds` sets the window (capacity = seconds × 1000 / interval) |
| `.push(value)` · `.last` · `.values()` · `.clear()` · `.pushes` | ring buffer drops the oldest sample once full |
| `new LineChart(canvas, {series: [{name, color?}], max, min=0, window=120, interval=1000, legend=true, legendEl, tooltip=true, grid=true, xAxis=true, fill=false, format})` | hairline grid, y ticks, DOM legend with live values (click a key to hide/show a series), crosshair + tooltip on hover and keyboard (←/→/Home/End on the focused canvas). Put the canvas inside a `.chart` box |
| `.push([v1, v2, …])` · `.setVisible(i, bool)` · `.clear()` | one value per series |
| `new RingGauge(canvas, {label, color='--accent', thickness=8, thresholds: {warning, danger}})` | circular progress with the value in the middle; with `thresholds` the ring turns warning/danger past those fractions |
| `.set(fraction, text?)` | text defaults to `formatPercent(fraction)` |
| `new BarMeter(container, {segments: [{name, color?}], total, legend=true, format=formatBytes, label})` | DOM stacked bar with 2 px surface gaps and a legend with values; `total` leaves the remainder as track |
| `.set(values, total?)` | same order as `segments` |
| all charts: `.resize()`, `.destroy()`, `.requestDraw()` | `destroy()` disconnects observers and removes generated DOM |
| `formatBytes(n, {perSecond=false, decimal=false, digits})` | `1536 → "1.50 KB"`, `2e9, {perSecond: true} → "1.86 GB/s"` |
| `formatPercent(x, {digits})` | fraction in: `0.4231 → "42%"`, `0.042 → "4.2%"` |
| `formatDuration(sec)` | `45 → "45s"`, `125 → "2m 05s"`, `3900 → "1h 05m"`, `90000 → "1d 1h"` |

Chart rules baked in (from the data-viz guidance): 2 px lines, ≥ 8 px markers
with a 2 px surface ring, 10 % area washes, hairline solid gridlines, legends
present for ≥ 2 series (none for one), values in text tokens never in the
series colour, severity carried by the fill of gauges/meters.

## ui.js

```js
import { h, $, $$, mount, icon, tabs, segmented, sliderFill, toast, confirmDialog, debounce, throttle, relativeTime, setTheme, getTheme, applyStoredTheme, isDark } from './shared/ui.js';
```

| API | Notes |
|---|---|
| `h(tag, attrs?, ...children)` | `h('button.btn.btn-primary', {onclick, disabled, dataset: {id}, style: {'--value': '40%'}, aria: {label: 'x'}}, 'Save')`. Keys: `class`/`className` (string or array), `style` (string or object), `dataset`, `aria`, `text`, `ref(node)`, `on<event>` handlers, form properties (`value`, `checked`, `disabled`, `hidden`, …), anything else as an attribute. Children: nodes, strings, numbers, nested arrays; `null`/`false` skipped. Strings are text nodes (never HTML) |
| `$(sel, root?)` / `$$(sel, root?)` | `querySelector` / `querySelectorAll` as an array |
| `mount(parent, node)` | replaces the children of `parent` (element or selector) |
| `icon(name, {size=16, className, label})` | inline SVG: check, x, info, warning, danger, search, chevron-down, chevron-right, plus, minus, refresh, more, inbox, power, cpu |
| `tabs(container, {onChange(id, tab, panel)})` | wires `.tabs`/`.tab` with tablist roles, roving tabindex, arrow keys, shows `data-panel` panels → `{select(idOrIndex), index, tab, destroy()}` |
| `segmented(container, {onChange(value, button)})` | wires a `.segmented` of `<button data-value>` → `{select(value), value, destroy()}` |
| `sliderFill(input)` | keeps `--slider-fill` on a `.slider` current → `{update(), destroy()}` |
| `toast(message, {kind='info'|'success'|'warning'|'danger', timeout=3500, action, onAction})` | bottom-centre, `aria-live`, max three stacked → `{el, dismiss()}` |
| `confirmDialog({title, body, okLabel='OK', cancelLabel='Cancel', danger=false})` | modal `<dialog>` → `Promise<boolean>`; Escape/backdrop → `false`; `danger` makes OK red and focuses Cancel |
| `debounce(fn, wait=150)` / `throttle(fn, wait=100)` | both return a function with `.cancel()`; throttle is leading + trailing |
| `relativeTime(date, now?)` | `"just now"`, `"5 min ago"`, `"2 h ago"`, `"yesterday"`, `"3 d ago"`, `"in 2 h"` |
| `setTheme('light'|'dark'|'auto')` / `getTheme()` / `applyStoredTheme()` / `isDark()` | theme pin on `<html data-theme>`, remembered in `localStorage['ui.theme']` |

## Theming and overrides

Override tokens after `base.css`, globally or on a subtree — every component
and chart reads them live:

```css
/* app.css */
:root { --accent: #7c3aed; --accent-hover: #6d28d9; --accent-soft: rgba(124, 58, 237, .12); --accent-ring: rgba(124, 58, 237, .4); }
:root[data-theme="dark"], :root:not([data-theme="light"]) { /* dark values when needed */ }
.vitals-gpu { --chart-1: var(--chart-7); }   /* this card's charts default to violet */
```

Rules of thumb: keep `--chart-1…8` in order (the order is what keeps adjacent
series distinguishable for colour-blind readers); re-validate any new palette
with the data-viz validator against both `--surface` values; keep
`--success/--warning/--danger` for state only; give new text colours ≥ 4.5:1
against the surface they sit on.

Component density: set `--control-h` / `--toggle-w` / `--toggle-h` on a
container for a denser or roomier region, or rely on the automatic
`pointer: coarse` bump on phones.

## Tests

`node tests/ui/run.mjs` runs every `tests/ui/*.test.mjs`.
`01-ui-kit.test.mjs` opens `demo.html`, asserts no page errors, that every
section rendered, that the charts received pushes and painted, exercises tabs,
dialog, toast and sheet, toggles dark mode, screenshots
`test-results/ui-kit-light.png`, `ui-kit-dark.png` and `ui-kit-phone.png`, and
checks for horizontal overflow at 360 × 740.
