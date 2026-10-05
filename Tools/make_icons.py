#!/usr/bin/env python3
"""
Draw the app icons in code (so they are diffable and regenerate identically),
then emit the macOS .icns and Windows .ico files.

    Tools/make_icons.py            # all apps
    Tools/make_icons.py vitals     # one app

Writes apps/<app>/assets/icon-1024.png, apps/<app>/mac/AppIcon.icns and
apps/<app>/win/icon.ico. Requires Pillow.
"""
import math
import os
import struct
import subprocess
import sys
from io import BytesIO

from PIL import Image, ImageDraw

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
S = 1024          # final size
K = 4             # supersampling factor
W = S * K


def rounded_bg(draw, c1, c2):
    """macOS-style squircle-ish rounded square with a vertical gradient."""
    inset = int(W * 0.08)
    r = int(W * 0.20)
    grad = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    g = ImageDraw.Draw(grad)
    for y in range(inset, W - inset):
        t = (y - inset) / (W - 2 * inset)
        col = tuple(int(c1[i] + (c2[i] - c1[i]) * t) for i in range(3)) + (255,)
        g.line([(inset, y), (W - inset, y)], fill=col)
    mask = Image.new("L", (W, W), 0)
    ImageDraw.Draw(mask).rounded_rectangle([inset, inset, W - inset, W - inset], radius=r, fill=255)
    return grad, mask


def stroke_polyline(draw, pts, width, fill):
    draw.line(pts, fill=fill, width=width, joint="curve")
    r = width // 2
    for (x, y) in (pts[0], pts[-1]):
        draw.ellipse([x - r, y - r, x + r, y + r], fill=fill)


def icon_vitals():
    # Teal→indigo card with a white heartbeat trace and a faint grid.
    grad, mask = rounded_bg(None, (18, 184, 166), (79, 70, 229))
    layer = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    # grid
    for i in range(1, 6):
        x = int(W * (0.16 + 0.68 * i / 6))
        d.line([(x, W * 0.22), (x, W * 0.78)], fill=(255, 255, 255, 40), width=K * 6)
        d.line([(W * 0.16, x), (W * 0.84, x)], fill=(255, 255, 255, 40), width=K * 6)
    # heartbeat
    base = W * 0.52
    pts = [(W * 0.16, base), (W * 0.33, base), (W * 0.39, W * 0.30), (W * 0.46, W * 0.74),
           (W * 0.53, W * 0.36), (W * 0.58, base), (W * 0.84, base)]
    stroke_polyline(d, pts, K * 56, (255, 255, 255, 255))
    out = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    out.paste(grad, (0, 0), mask)
    out.alpha_composite(layer)
    return out


def icon_edgehop():
    # Orange→pink card, two monitors side by side, an arrow hopping across the gap.
    grad, mask = rounded_bg(None, (249, 115, 22), (219, 39, 119))
    layer = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    white = (255, 255, 255, 255)
    mono_h = W * 0.34
    top = W * 0.30
    gap = W * 0.06
    mw = (W * 0.68 - gap) / 2
    for i in range(2):
        x0 = W * 0.16 + i * (mw + gap)
        d.rounded_rectangle([x0, top, x0 + mw, top + mono_h], radius=K * 28, outline=white, width=K * 34)
        # stand
        cx = x0 + mw / 2
        d.rectangle([cx - K * 22, top + mono_h, cx + K * 22, top + mono_h + W * 0.06], fill=white)
        d.rounded_rectangle([cx - W * 0.09, top + mono_h + W * 0.055, cx + W * 0.09, top + mono_h + W * 0.09], radius=K * 14, fill=white)
    # arrow arc from left screen centre to right screen centre
    lx, rx = W * 0.16 + mw / 2 + W * 0.05, W * 0.16 + mw + gap + mw / 2 - W * 0.05
    cy = top + mono_h * 0.55
    pts = []
    for t in range(0, 101):
        u = t / 100
        x = lx + (rx - lx) * u
        y = cy - math.sin(u * math.pi) * W * 0.17
        pts.append((x, y))
    stroke_polyline(d, pts, K * 30, white)
    # arrow head
    ax, ay = pts[-1]
    d.polygon([(ax + K * 8, ay + K * 8), (ax - W * 0.06, ay - W * 0.02), (ax - W * 0.015, ay - W * 0.075)], fill=white)
    out = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    out.paste(grad, (0, 0), mask)
    out.alpha_composite(layer)
    return out


def icon_reach():
    # Blue→violet card, a phone outline with three signal arcs radiating to the right.
    grad, mask = rounded_bg(None, (14, 165, 233), (124, 58, 237))
    layer = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    white = (255, 255, 255, 255)
    px0, py0, px1, py1 = W * 0.22, W * 0.22, W * 0.50, W * 0.78
    d.rounded_rectangle([px0, py0, px1, py1], radius=K * 60, outline=white, width=K * 36)
    d.rounded_rectangle([px0 + (px1 - px0) * 0.35, py0 + K * 40, px1 - (px1 - px0) * 0.35, py0 + K * 70], radius=K * 15, fill=white)
    d.ellipse([(px0 + px1) / 2 - K * 22, py1 - K * 90, (px0 + px1) / 2 + K * 22, py1 - K * 46], fill=white)
    cx, cy = W * 0.56, W * 0.50
    for i, rad in enumerate((W * 0.08, W * 0.16, W * 0.24)):
        d.arc([cx - rad, cy - rad, cx + rad, cy + rad], start=-50, end=50, fill=white, width=K * 34)
    d.ellipse([cx - K * 26, cy - K * 26, cx + K * 26, cy + K * 26], fill=white)
    out = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    out.paste(grad, (0, 0), mask)
    out.alpha_composite(layer)
    return out


def write_ico(img, path):
    """ICO container with PNG payloads (Vista+), sizes 16–256."""
    sizes = [16, 24, 32, 48, 64, 128, 256]
    entries, blobs = [], []
    offset = 6 + 16 * len(sizes)
    for s in sizes:
        buf = BytesIO()
        img.resize((s, s), Image.LANCZOS).save(buf, format="PNG", optimize=True)
        data = buf.getvalue()
        entries.append(struct.pack("<BBBBHHII", s if s < 256 else 0, s if s < 256 else 0, 0, 0, 1, 32, len(data), offset))
        blobs.append(data)
        offset += len(data)
    with open(path, "wb") as f:
        f.write(struct.pack("<HHH", 0, 1, len(sizes)))
        f.write(b"".join(entries))
        f.write(b"".join(blobs))


ICONS = {"vitals": icon_vitals, "edgehop": icon_edgehop, "reach": icon_reach}


def main(apps):
    for app in apps:
        big = ICONS[app]()
        img = big.resize((S, S), Image.LANCZOS)
        assets = os.path.join(ROOT, "apps", app, "assets")
        os.makedirs(assets, exist_ok=True)
        png = os.path.join(assets, "icon-1024.png")
        img.save(png, optimize=True)
        img.resize((256, 256), Image.LANCZOS).save(os.path.join(assets, "icon-256.png"), optimize=True)
        subprocess.run([sys.executable, os.path.join(ROOT, "Tools", "make_icns.py"), png,
                        os.path.join(ROOT, "apps", app, "mac", "AppIcon.icns")], check=True)
        write_ico(img, os.path.join(ROOT, "apps", app, "win", "icon.ico"))
        # PWA / web UI icons
        static = os.path.join(ROOT, "apps", app, "ui", "static")
        os.makedirs(static, exist_ok=True)
        for s in (192, 512):
            img.resize((s, s), Image.LANCZOS).save(os.path.join(static, f"icon-{s}.png"), optimize=True)
        print(f"{app}: icon-1024.png, AppIcon.icns, icon.ico, icon-192/512.png")


if __name__ == "__main__":
    main(sys.argv[1:] or list(ICONS))
