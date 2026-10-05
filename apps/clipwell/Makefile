# Clipwell — build, test and install.
#
# Two build paths, same output bundle:
#   * `xcodebuild` when a full Xcode is installed (the path the project was specified around)
#   * `Tools/build.sh` (swiftc + codesign) when only the Command Line Tools are present
#
# `make build` picks whichever is available, so neither is a prerequisite for the other.

APP_NAME    := Clipwell
BUNDLE_ID   := com.thiru.clipwell
BUILD_DIR   := build
APP         := $(BUILD_DIR)/$(APP_NAME).app
INSTALL_DIR := /Applications
INSTALLED   := $(INSTALL_DIR)/$(APP_NAME).app
CONFIG      := Release

HAS_XCODE := $(shell xcodebuild -version >/dev/null 2>&1 && echo yes)

.PHONY: help
help:
	@echo "Clipwell"
	@echo ""
	@echo "  make build      Build $(APP_NAME).app into ./$(BUILD_DIR)"
	@echo "  make install    Build and install to $(INSTALL_DIR)"
	@echo "  make uninstall  Remove $(INSTALLED)"
	@echo "  make run        Launch the built app"
	@echo "  make stop       Quit any running instance"
	@echo "  make test       Run the clipboard smoke tests"
	@echo "  make project    Regenerate $(APP_NAME).xcodeproj from the sources on disk"
	@echo "  make clean      Delete ./$(BUILD_DIR)"
	@echo ""
ifeq ($(HAS_XCODE),yes)
	@echo "  Build backend:  xcodebuild (full Xcode detected)"
else
	@echo "  Build backend:  swiftc via Tools/build.sh (no full Xcode detected)"
	@echo "                  Install Xcode to use the xcodebuild path."
endif

# ---------------------------------------------------------------------------- build

.PHONY: build
ifeq ($(HAS_XCODE),yes)
build:
	xcodebuild -project $(APP_NAME).xcodeproj \
	           -scheme $(APP_NAME) \
	           -configuration $(CONFIG) \
	           -derivedDataPath $(BUILD_DIR)/DerivedData \
	           build
	@rm -rf "$(APP)"
	@mkdir -p "$(BUILD_DIR)"
	@cp -R "$(BUILD_DIR)/DerivedData/Build/Products/$(CONFIG)/$(APP_NAME).app" "$(APP)"
	@echo "==> Built $(APP)"
else
build:
	@Tools/build.sh release
endif

.PHONY: project
project:
	@python3 Tools/generate_xcodeproj.py

# ---------------------------------------------------------------------------- install

.PHONY: install
install: build stop
	@# Only ever replace a bundle that is actually Clipwell — never blindly rm -rf a path
	@# under /Applications.
	@if [ -e "$(INSTALLED)" ]; then \
		existing=$$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \
			"$(INSTALLED)/Contents/Info.plist" 2>/dev/null); \
		if [ "$$existing" != "$(BUNDLE_ID)" ]; then \
			echo "refusing to replace $(INSTALLED): bundle id is '$$existing', expected '$(BUNDLE_ID)'"; \
			exit 1; \
		fi; \
		rm -rf "$(INSTALLED)"; \
	fi
	@cp -R "$(APP)" "$(INSTALLED)"
	@echo ""
	@echo "==> Installed $(INSTALLED)"
	@echo ""
	@echo "  ┌──────────────────────────────────────────────────────────────────────┐"
	@echo "  │  One more step: grant Accessibility access                            │"
	@echo "  └──────────────────────────────────────────────────────────────────────┘"
	@echo ""
	@echo "  Clipwell cannot move windows or paste for you until macOS allows it."
	@echo ""
	@echo "    1. Open Clipwell:      open $(INSTALLED)"
	@echo "    2. It opens a setup window with a button to the right settings pane."
	@echo "       (Or go there directly: System Settings > Privacy & Security >"
	@echo "        Accessibility.)"
	@echo "    3. Turn ON the switch next to Clipwell."
	@echo ""
	@echo "  If Clipwell is ALREADY in that list from an earlier build, select it,"
	@echo "  press '-' to remove it, then add the new copy. macOS ties the permission"
	@echo "  to the code signature, and an ad-hoc signature changes on every rebuild."
	@echo ""
	@echo "  Then: click the menu bar icon, or press Ctrl+Cmd+Left to snap a window."
	@echo ""

.PHONY: uninstall
uninstall: stop
	@if [ -e "$(INSTALLED)" ]; then rm -rf "$(INSTALLED)"; echo "removed $(INSTALLED)"; \
	else echo "$(INSTALLED) is not installed"; fi
	@echo "note: clipboard history in ~/Library/Application Support/$(APP_NAME) was left in place"

# ---------------------------------------------------------------------------- run / test

.PHONY: run
run: build
	@open "$(APP)"

.PHONY: stop
stop:
	@pkill -x $(APP_NAME) 2>/dev/null || true

.PHONY: test
test: build
	@Tools/smoketest.sh

.PHONY: clean
clean:
	@rm -rf "$(BUILD_DIR)"
	@echo "cleaned $(BUILD_DIR)"

# ---------------------------------------------------------------------------- distribution

VERSION := $(shell /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' Support/Info.plist)
DMG     := $(BUILD_DIR)/Clipwell-$(VERSION).dmg
STAGE   := $(BUILD_DIR)/dmg-stage

## Build a drag-to-install disk image for sharing with other people.
.PHONY: dmg
dmg: build
	@rm -rf "$(STAGE)" "$(DMG)"
	@mkdir -p "$(STAGE)"
	@cp -R "$(APP)" "$(STAGE)/"
	@ln -s /Applications "$(STAGE)/Applications"
	@cp dist/READ-ME-FIRST.txt "$(STAGE)/" 2>/dev/null || true
	@hdiutil create -volname "Clipwell $(VERSION)" -srcfolder "$(STAGE)" \
		-ov -format UDZO "$(DMG)" >/dev/null
	@rm -rf "$(STAGE)"
	@echo "==> $(DMG)  ($$(du -h '$(DMG)' | cut -f1))"
	@echo ""
	@echo "    Ad-hoc signed. Recipients MUST run this after copying to /Applications:"
	@echo "      xattr -dr com.apple.quarantine /Applications/Clipwell.app"
	@echo "    See dist/DISTRIBUTION.md for the Developer ID path that removes that step."

# ------------------------------------------------------- Developer ID + notarisation
#
# The committed Xcode project stays ad-hoc signed so that anyone can clone and build with no
# Apple account at all. Real distribution signing is applied here as xcodebuild overrides
# rather than baked into project.pbxproj — nothing to remember to revert, and no secret ever
# lands in the repo.
#
# Prerequisites, one-time (see `make notary-setup`):
#   * a "Developer ID Application" certificate in your keychain
#   * a stored notarytool credential profile

# Auto-detected; override with `make notarized-dmg SIGN_IDENTITY="Developer ID Application: ..."`
SIGN_IDENTITY  ?= $(shell security find-identity -v -p codesigning 2>/dev/null \
                    | grep "Developer ID Application" | head -1 \
                    | sed -E 's/.*"(.*)".*/\1/')
NOTARY_PROFILE ?= clipwell-notary
SIGNED_DMG     := $(BUILD_DIR)/Clipwell-$(VERSION).dmg

## Print the exact one-time setup commands. Run them yourself — they involve your password.
.PHONY: notary-setup
notary-setup:
	@echo ""
	@echo "  One-time setup for notarised releases"
	@echo "  ====================================="
	@echo ""
	@echo "  1. Create a Developer ID Application certificate."
	@echo "     Xcode > Settings > Accounts > (your Apple ID) > Manage Certificates"
	@echo "       > the + button > 'Developer ID Application'"
	@echo "     Requires a paid Apple Developer Program membership."
	@echo ""
	@echo "  2. Create an app-specific password at https://appleid.apple.com"
	@echo "     (Sign-In and Security > App-Specific Passwords). It is NOT your"
	@echo "     Apple ID password."
	@echo ""
	@echo "  3. Store the notarytool credentials. RUN THIS YOURSELF in your own"
	@echo "     terminal — it prompts for the password above, and that password"
	@echo "     should never be pasted into a script, a chat, or this Makefile:"
	@echo ""
	@echo "       xcrun notarytool store-credentials $(NOTARY_PROFILE) \\"
	@echo "         --apple-id YOUR_APPLE_ID --team-id YOUR_TEAM_ID"
	@echo ""
	@echo "     Your team id is the 10-character code at"
	@echo "     https://developer.apple.com/account under Membership Details."
	@echo ""
	@echo "  Then: make notarized-dmg"
	@echo ""
	@echo "  Detected now:"
	@if [ -n "$(SIGN_IDENTITY)" ]; then echo "    certificate : $(SIGN_IDENTITY)"; \
	 else echo "    certificate : NONE — step 1 not done yet"; fi
	@if xcrun notarytool history --keychain-profile $(NOTARY_PROFILE) >/dev/null 2>&1; \
	 then echo "    credentials : stored as '$(NOTARY_PROFILE)'"; \
	 else echo "    credentials : NONE — step 3 not done yet"; fi
	@echo ""

## Build signed with Developer ID + hardened runtime (required for notarisation).
.PHONY: signed-build
signed-build:
	@if [ -z "$(SIGN_IDENTITY)" ]; then \
		echo "no 'Developer ID Application' certificate found in your keychain."; \
		echo "run 'make notary-setup' for instructions."; exit 1; fi
	@echo "==> Signing with: $(SIGN_IDENTITY)"
	@rm -rf "$(BUILD_DIR)/DerivedData"
	xcodebuild -project $(APP_NAME).xcodeproj \
	           -scheme $(APP_NAME) \
	           -configuration $(CONFIG) \
	           -derivedDataPath $(BUILD_DIR)/DerivedData \
	           CODE_SIGN_IDENTITY="$(SIGN_IDENTITY)" \
	           CODE_SIGN_STYLE=Manual \
	           ENABLE_HARDENED_RUNTIME=YES \
	           OTHER_CODE_SIGN_FLAGS="--timestamp" \
	           build
	@rm -rf "$(APP)"
	@cp -R "$(BUILD_DIR)/DerivedData/Build/Products/$(CONFIG)/$(APP_NAME).app" "$(APP)"
	@codesign --verify --strict --deep --verbose=1 "$(APP)"
	@echo "==> Signed $(APP)"

## Full release: signed build -> dmg -> notarise -> staple -> verify.
.PHONY: notarized-dmg
notarized-dmg: signed-build
	@if ! xcrun notarytool history --keychain-profile $(NOTARY_PROFILE) >/dev/null 2>&1; then \
		echo "no stored notarytool profile '$(NOTARY_PROFILE)'."; \
		echo "run 'make notary-setup' for instructions."; exit 1; fi
	@rm -rf "$(STAGE)" "$(SIGNED_DMG)"
	@mkdir -p "$(STAGE)"
	@cp -R "$(APP)" "$(STAGE)/"
	@ln -s /Applications "$(STAGE)/Applications"
	@cp dist/READ-ME-FIRST.txt "$(STAGE)/" 2>/dev/null || true
	@hdiutil create -volname "Clipwell $(VERSION)" -srcfolder "$(STAGE)" \
		-ov -format UDZO "$(SIGNED_DMG)" >/dev/null
	@rm -rf "$(STAGE)"
	@echo "==> Signing the disk image itself"
	@codesign --force --sign "$(SIGN_IDENTITY)" --timestamp "$(SIGNED_DMG)"
	@echo "==> Submitting to Apple (this usually takes 1-5 minutes)"
	xcrun notarytool submit "$(SIGNED_DMG)" --keychain-profile $(NOTARY_PROFILE) --wait
	@echo "==> Stapling the ticket so it validates offline"
	xcrun stapler staple "$(SIGNED_DMG)"
	@echo ""
	@echo "==> Verifying the way Gatekeeper will see it"
	@xcrun stapler validate "$(SIGNED_DMG)"
	@spctl -a -vvv -t open --context context:primary-signature "$(SIGNED_DMG)" 2>&1 || true
	@echo ""
	@echo "==> $(SIGNED_DMG)  ($$(du -h '$(SIGNED_DMG)' | cut -f1))"
	@echo "    Recipients can now just drag and open. No xattr, no warning."

## Report what kind of signature the built app carries.
.PHONY: signing-status
signing-status: build
	@echo "== signature =="
	@codesign -dv --verbose=2 "$(APP)" 2>&1 | grep -E 'Authority|Signature|TeamIdentifier|flags' || true
	@echo ""
	@echo "== notarization =="
	@spctl -a -vvv -t install "$(APP)" 2>&1 || true
	@echo ""
	@echo "== available identities =="
	@security find-identity -v -p codesigning 2>/dev/null | grep -E 'Developer ID|Apple Development' \
		|| echo "  (none — ad-hoc signing only)"
