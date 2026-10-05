#!/usr/bin/env python3
"""
Build an Apple .icns file from a single square PNG, without macOS tooling.

    Tools/make_icns.py icon-1024.png AppIcon.icns

Writes the PNG-payload icon types macOS reads directly (ic07..ic14), so the
result works for Finder, the Dock and the menu bar on macOS 10.7+.
Requires Pillow.
"""
import struct
import sys
from io import BytesIO

from PIL import Image

# (type code, pixel size)
TYPES = [
    (b"icp4", 16), (b"icp5", 32), (b"icp6", 64),
    (b"ic07", 128), (b"ic08", 256), (b"ic09", 512), (b"ic10", 1024),
    (b"ic11", 32), (b"ic12", 64), (b"ic13", 256), (b"ic14", 512),
]


def main(src, dst):
    img = Image.open(src).convert("RGBA")
    if img.width != img.height:
        sys.exit("icon must be square")
    chunks = []
    for code, size in TYPES:
        buf = BytesIO()
        img.resize((size, size), Image.LANCZOS).save(buf, format="PNG", optimize=True)
        data = buf.getvalue()
        chunks.append(code + struct.pack(">I", 8 + len(data)) + data)
    body = b"".join(chunks)
    with open(dst, "wb") as f:
        f.write(b"icns" + struct.pack(">I", 8 + len(body)) + body)
    print(f"wrote {dst} ({len(body) // 1024} KiB, {len(TYPES)} sizes)")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2])
