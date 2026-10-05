# mac-utility — a suite of small desktop utilities. One repo, one build entry point.
#
#   make help                 list targets
#   make mac                  build every macOS app into ./build (needs macOS + CLT)
#   make dmg                  build every macOS app and wrap each in a DMG in ./dist-out
#   make windows              cross-compile every Windows exe into ./dist-out (any OS with Go)
#   make test                 run the cross-platform test suites (Go + web UI)
#   make <app>                build one macOS app          (clipwell vitals edgehop reach)
#   make dmg-<app>            build one macOS DMG
#   make exe-<app>            build one Windows exe        (vitals edgehop reach)

SHELL      := /bin/bash
ROOT       := $(CURDIR)
DIST       := $(ROOT)/dist-out
BUILD      := $(ROOT)/build
MAC_APPS   := clipwell vitals edgehop reach
WIN_APPS   := vitals edgehop reach
GOOS_WIN   := windows
GOARCH_WIN ?= amd64

# Display name / bundle executable per app directory (mac/Info.plist is the source of truth;
# these are only used to name outputs before the plist exists on a build machine).
name_clipwell := Clipwell
name_vitals   := Vitals
name_edgehop  := Edgehop
name_reach    := Reach

.PHONY: help
help:
	@sed -n 's/^#   //p' $(MAKEFILE_LIST) | head -12

# ------------------------------------------------------------------ macOS

.PHONY: mac dmg
mac: $(MAC_APPS)
dmg: $(addprefix dmg-,$(MAC_APPS))

.PHONY: clipwell
clipwell:
	@$(MAKE) -C apps/clipwell build

.PHONY: dmg-clipwell
dmg-clipwell:
	@$(MAKE) -C apps/clipwell dmg
	@mkdir -p $(DIST) && cp apps/clipwell/build/Clipwell-*.dmg $(DIST)/

define MAC_APP_RULES
.PHONY: $(1) dmg-$(1)
$(1):
	@Tools/build_mac_app.sh apps/$(1) release
dmg-$(1): $(1)
	@mkdir -p $(DIST)
	@v=$$$$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' apps/$(1)/mac/Info.plist); \
	 Tools/make_dmg.sh $(BUILD)/$(name_$(1)).app $(DIST)/$(name_$(1))-$$$$v.dmg "$(name_$(1)) $$$$v" apps/$(1)/dist/READ-ME-FIRST.txt
endef
$(foreach a,vitals edgehop reach,$(eval $(call MAC_APP_RULES,$(a))))

# ------------------------------------------------------------------ Windows

.PHONY: windows
windows: $(addprefix exe-,$(WIN_APPS))

define WIN_APP_RULES
.PHONY: exe-$(1)
exe-$(1):
	@mkdir -p $(DIST)
	@v=$$$$(cat apps/$(1)/VERSION 2>/dev/null || echo 0.0.0); \
	 echo "==> $(name_$(1)) $$$$v for $(GOOS_WIN)/$(GOARCH_WIN)"; \
	 cd apps/$(1)/win && GOOS=$(GOOS_WIN) GOARCH=$(GOARCH_WIN) CGO_ENABLED=0 \
	   go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=$$$$v" \
	   -o $(DIST)/$(name_$(1))-$$$$v-windows-$(GOARCH_WIN).exe . && \
	 echo "    $(DIST)/$(name_$(1))-$$$$v-windows-$(GOARCH_WIN).exe"
endef
$(foreach a,$(WIN_APPS),$(eval $(call WIN_APP_RULES,$(a))))

# ------------------------------------------------------------------ tests

.PHONY: test test-go test-ui
test: test-go test-ui

test-go:
	@go test ./... 2>&1 | tail -40

test-ui:
	@if [ -f package.json ]; then npm test; else echo "no package.json yet"; fi

.PHONY: clean
clean:
	@rm -rf $(BUILD) $(DIST) apps/clipwell/build
	@echo cleaned
