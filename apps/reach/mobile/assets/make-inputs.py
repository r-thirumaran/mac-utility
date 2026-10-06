#!/usr/bin/env python3
"""Derive the @capacitor/assets inputs in this directory from apps/reach/assets/icon-1024.png.

The source icon is the macOS-style one: a rounded square with a vertical
gradient on a transparent 1024 canvas. Native launchers want other shapes:

  icon-only.png        1024² full-bleed square (iOS masks it, Android < 8 shows it
                       as is): the rounded square cropped and its corners filled
                       row by row with the gradient, which is vertical.
  icon-foreground.png  the same full-bleed square as the adaptive-icon foreground
                       (Android 8+). @capacitor/assets maps it onto the 72 dp the
                       launcher mask can show, so the glyph (64 % of the width)
                       stays inside the 66 dp safe zone whatever the mask shape.
  icon-background.png  the bare gradient (no glyph) as the adaptive-icon
                       background, so a mask or a parallax shift shows more
                       gradient instead of a flat colour.
  splash.png           2732² light splash: the rounded icon centred on #f5f5f7.
  splash-dark.png      the same on #1c1c1e.

Then, from apps/reach/mobile:
  npx @capacitor/assets generate --android --ios --assetPath assets \
      --splashBackgroundColor '#f5f5f7' --splashBackgroundColorDark '#1c1c1e'

Needs Pillow (pip install pillow).
"""
from pathlib import Path

from PIL import Image

here = Path(__file__).resolve().parent
src = Image.open(here.parent.parent / 'assets' / 'icon-1024.png').convert('RGBA')

# --- icon-only.png: crop to the rounded square and square its corners off.
l, t, r, b = src.getbbox()
inset = 2  # skip the anti-aliased outermost rows
sq = src.crop((l + inset, t + inset, r - inset, b - inset))
px = sq.load()
w, h = sq.size
filled = []
for y in range(h):
    row = [x for x in range(w) if px[x, y][3] >= 250]
    if not row:
        continue
    lc, rc = px[row[0], y][:3] + (255,), px[row[-1], y][:3] + (255,)
    for x in range(0, row[0]):
        px[x, y] = lc
    for x in range(row[-1] + 1, w):
        px[x, y] = rc
    for x in range(row[0], row[-1] + 1):
        if px[x, y][3] < 255:
            px[x, y] = px[x, y][:3] + (255,)
    filled.append(y)
for y in range(h):  # the anti-aliased top and bottom rows: copy the nearest solid row
    if y not in filled:
        near = min(filled, key=lambda f: abs(f - y))
        for x in range(w):
            px[x, y] = px[x, near]
icon_only = sq.resize((1024, 1024), Image.LANCZOS)
icon_only.save(here / 'icon-only.png', optimize=True)
icon_only.save(here / 'icon-foreground.png', optimize=True)

# --- icon-background.png: the gradient alone, one colour per row taken at the edge.
bg = Image.new('RGBA', (1024, 1024))
bpx = bg.load()
for y in range(1024):
    c = icon_only.getpixel((4, y))
    for x in range(1024):
        bpx[x, y] = c
bg.save(here / 'icon-background.png', optimize=True)

# --- splash.png / splash-dark.png: the rounded icon centred on the app background colours.
for name, colour in (('splash.png', (0xf5, 0xf5, 0xf7, 255)), ('splash-dark.png', (0x1c, 0x1c, 0x1e, 255))):
    canvas = Image.new('RGBA', (2732, 2732), colour)
    logo = src.crop((l, t, r, b)).resize((640, 640), Image.LANCZOS)
    canvas.paste(logo, ((2732 - 640) // 2, (2732 - 640) // 2), logo)
    canvas.convert('RGB').save(here / name, optimize=True)

print('wrote', ', '.join(p.name for p in sorted(here.glob('*.png'))))
