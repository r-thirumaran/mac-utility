---
name: Bug report
about: Something does not work as described
labels: bug
---

**What happened, and what did you expect instead?**


**Which feature?**
- [ ] Window snapping
- [ ] Clipboard history / the ⌘⇧V panel
- [ ] Screenshots
- [ ] Settings, launch at login, or install

**If it is a snapping problem**, these three things narrow it down almost every time:
- Which app was the window in?
- How many displays, and roughly how are they arranged?
- What did you press, and what happened instead?

**Setup**
- macOS version:
- Mac model (Apple silicon or Intel):
- Clipwell version (Settings ▸ General ▸ About):
- Installed from the DMG, or built from source?

**Did you grant Accessibility?**
Settings ▸ Privacy & Security ▸ Accessibility. Nothing snaps or pastes without it, and macOS
silently drops the permission when the app is rebuilt — if snapping stopped after an update,
try removing Clipwell from that list and adding it again.
