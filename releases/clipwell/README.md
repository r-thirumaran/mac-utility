# Clipwell release disk images

Copied verbatim from the GitHub releases of
[r-thirumaran/Clipwell](https://github.com/r-thirumaran/Clipwell/releases).
Every file was re-hashed after download and matched the SHA-256 printed in the
corresponding release notes (see `manifest.json` for the per-file record).

| File | Tag | SHA-256 |
|---|---|---|
| `Clipwell-2.1.dmg` | v2.1 | `6ae52579d7ddb9b20496c074e8e3cbf21f788d83c3b99513bd7af48b325dd098` |
| `Clipwell-2.0.dmg` | v2.0 | `a351d7b8b984531520092d3516b38dbcb4d9d1b0cf4696b628e60e45592d56ab` |
| `SnapBoard-1.2.1.dmg` | v1.2.1 | `ef3daff7ce4e93abe9b5456d85ea65fa7b14754a2e0d8aaaad6e2d226035e8c4` |
| `SnapBoard-1.2.dmg` | v1.2 | `f42fdb06ecc8ad1fea58a4f51d88c2142640323acda81559e8b0ea61d84fefec` |
| `SnapBoard-1.1.dmg` | v1.1 | `24dc9fe6e8220243e7a9358b067db3a1291e2c7b18ad6d7cf6c5fa9b295d7a38` |
| `SnapBoard-1.0.dmg` | v1.0 | `b0452df11b09ff7adf6a0c82c4b047028156b8a5a752182bcbe758eb065523df` |

All builds are ad-hoc signed. After dragging to `/Applications`:

```bash
xattr -dr com.apple.quarantine /Applications/Clipwell.app
```

Current source lives in `apps/clipwell/`; `make -C apps/clipwell dmg` rebuilds a
fresh image from it.
