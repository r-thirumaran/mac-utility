# Reach mobile — the phone app as native Android and iOS apps

[Capacitor](https://capacitorjs.com) 8 wrapper around the Reach phone web app.
It ships the exact files the agents serve (`apps/reach/ui/static` plus
`shared/ui`) inside a WebView and talks to the agents on the LAN the same way
the PWA does (`docs/reach-api.md`). An installed app keeps its pairings when the
browser clears site data, has a proper icon, vibrates on trackpad taps and
understands the Android back button; otherwise it *is* the PWA.

| path | what |
|---|---|
| `capacitor.config.json` | app id `com.thiru.reach`, name *Reach*, `webDir: www`, the http/cleartext settings explained below |
| `sync-www.mjs` | builds `www/` (git-ignored) from `../ui/static` and `shared/ui` → `www/shared`; `npm run sync:www` |
| `android/` | the native Android project (committed; Gradle, AndroidManifest, icons). `npx cap sync android` refreshes its web assets and plugin list |
| `ios/` | the native Xcode project (committed; Swift Package Manager, no CocoaPods). `npx cap sync ios` likewise |
| `assets/` | icon and splash inputs for `@capacitor/assets`, derived from `../assets/icon-1024.png` by `assets/make-inputs.py` |
| `package.json` | pins `@capacitor/{core,cli,android,ios}` 8.5.2, `@capacitor/app` (back button), `@capacitor/haptics` |

The web app itself notices the wrapper through `window.Capacitor`
(`WRAPPED` in `../ui/static/core.js`): it then skips the service worker and the
"add the laptop that served me" step, and loads `../ui/static/capacitor.js`,
the only file that talks to native plugins (haptics on clicks, back button
closes an open sheet or dialog, then goes back a tab, then backgrounds the app).
Nothing else in the web app changes, and a plain browser never loads that file.

## Prerequisites

* Node 22 or newer (`npm ci` in this directory installs Capacitor).
* Android: JDK **21** (Capacitor 8 compiles with Java 21) and the Android SDK
  with platform 36 — Android Studio Otter or newer installs it. Point Gradle at
  it with `ANDROID_HOME` or `android/local.properties` (`sdk.dir=…`, Android
  Studio writes it).
* iOS: a Mac with Xcode 26 or newer (deployment target iOS 15). The project
  uses Swift Package Manager, so no CocoaPods or Ruby.

## Build and run on Android (locally)

```sh
cd apps/reach/mobile
npm ci
npm run android          # = npm run sync:www && npx cap sync android
npx cap open android     # Android Studio: pick your phone, Run
```

Without the IDE:

```sh
cd android
./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

After changing anything under `apps/reach/ui/static` or `shared/ui`, run
`npm run android` again before building — `www/` and the native project's copy
of it are generated, never edited.

## The CI APK

Job **Reach Android APK (Capacitor)** in `.github/workflows/build.yml` runs on
every push: `actions/setup-java` (Temurin 21) and `setup-node` 22, then
`npm ci`, `npm run sync:www`, `npx cap sync android`,
`./gradlew assembleDebug --no-daemon` with the SDK preinstalled on the
`ubuntu-latest` image (`$ANDROID_HOME`; no licence step is needed there). The
result is renamed `Reach-<apps/reach/VERSION>-android-debug.apk`, checksummed
and uploaded as the artifact **`reach-android-apk`**; on a `v*` tag the release
job attaches it to the GitHub release next to the DMGs and exes.

It is a *debug* build: unsigned for the Play Store, signed with the debug key.
Gradle generates that key on the runner, so by default every CI build has a
different one and Android refuses to install a new build over an older one
("app not installed" / signature mismatch) — you would have to uninstall first,
which also deletes the app's data, i.e. the pairings. To keep builds
installable over each other, give the repository a secret
`ANDROID_DEBUG_KEYSTORE_B64` with a debug keystore of your own:

```sh
keytool -genkeypair -v -keystore debug.keystore -storepass android -keypass android \
  -alias androiddebugkey -keyalg RSA -keysize 2048 -validity 10000 \
  -dname "CN=Android Debug,O=Android,C=US"
base64 -w0 debug.keystore      # → the secret's value
```

The job restores it to `~/.android/debug.keystore` when the secret exists.
Keep the same keystore in `~/.android` on your Mac/PC if you also build locally.

## Sideloading the debug APK

1. Open the workflow run (or the release) on GitHub, download
   `reach-android-apk`, unzip it and copy `Reach-<version>-android-debug.apk`
   to the phone (or open the GitHub page on the phone and download it there).
2. Open the file. Android asks to allow installs from that app (browser, Files):
   *Settings ▸ Apps ▸ Special app access ▸ Install unknown apps*. Confirm.
   Play Protect may warn that it does not know the app; tap *Install anyway*.
3. Or with a cable: `adb install -r Reach-<version>-android-debug.apk`.

If a previous build came from a different debug key, uninstall it first (and
pair the laptops again afterwards), see above.

Check the checksum against `SHA256SUMS-reach-android.txt` from the same
artifact if the file travelled through a chat or a cloud drive.

## iOS with a personal Apple ID

There is no CI build for iOS (it needs Xcode). On a Mac:

```sh
cd apps/reach/mobile
npm ci                   # the Xcode project references node_modules/@capacitor/* packages
npm run ios              # = npm run sync:www && npx cap sync ios
npx cap open ios         # opens ios/App/App.xcodeproj in Xcode
```

In Xcode:

1. *Xcode ▸ Settings ▸ Accounts ▸ +* and sign in with your Apple ID (no paid
   developer programme needed).
2. Select the *App* target ▸ *Signing & Capabilities*: tick *Automatically
   manage signing* and pick your personal team. If Xcode says the bundle
   identifier is taken, change it (e.g. `com.yourname.reach`) — a free team can
   only sign ids nobody else registered.
3. Plug in the iPhone, enable *Developer Mode* on it (*Settings ▸ Privacy &
   Security ▸ Developer Mode*, iOS 16+), pick it as the run destination and
   press Run. The first time, the phone refuses to open the app until you trust
   the developer: *Settings ▸ General ▸ VPN & Device Management ▸ your Apple ID
   ▸ Trust*.
4. On first contact with a laptop iOS asks "Reach would like to find and
   connect to devices on your local network" — allow it, otherwise nothing on
   the LAN is reachable (later: *Settings ▸ Privacy & Security ▸ Local
   Network ▸ Reach*).

Free-account limits: the signature expires after **7 days** (run from Xcode
again to renew; the app's data and pairings survive), at most three such apps
per phone, and the Mac must be reachable to reinstall. A paid developer
account signs for a year.

## Configuration notes: plaintext on the LAN

Reach version 1 talks plain `http://` and `ws://` to the agents (see the
security caveat in `../README.md`): the pairing token and everything you type
cross the local network unencrypted, so use it on networks you trust and revoke
pairings you no longer use. Mobile OSes block cleartext by default; the wrapper
opts in explicitly and only for that:

* **Android** — `server.androidScheme: 'http'` serves the bundle from
  `http://localhost`, which is still a secure context (localStorage, wake lock
  work) while an http: origin does not treat `http://192.168…` and `ws://…` as
  blocked mixed content; `android.allowMixedContent: true` is belt and braces
  for the WebView, and `android:usesCleartextTraffic="true"` in
  `android/app/src/main/AndroidManifest.xml` lets the OS network stack allow
  the cleartext connections (plus the `INTERNET` and `ACCESS_NETWORK_STATE`
  permissions). Nothing in the app ever loads a remote page: `www/` is the only
  thing the WebView shows.
* **iOS** — `ios/App/App/Info.plist` declares `NSLocalNetworkUsageDescription`
  (the local-network permission prompt), `NSBonjourServices` (`_reach._tcp`,
  the agents' advertised service type) and App Transport Security exceptions
  `NSAllowsLocalNetworking` (http/ws to LAN addresses) and
  `NSAllowsArbitraryLoadsInWebContent` (a laptop entered by a DNS name that is
  not `.local`, e.g. `laptop.lan`, is still plain http inside the web view).

## Icons and splash screens

`assets/` holds the inputs for `@capacitor/assets`; `assets/make-inputs.py`
(Pillow) derives them from `../assets/icon-1024.png` — a full-bleed square for
iOS and the Android adaptive-icon foreground, the bare gradient as the adaptive
background, and light/dark splash images. When the icon changes:

```sh
cd apps/reach/mobile
python3 assets/make-inputs.py
npx @capacitor/assets generate --android --ios --assetPath assets \
    --splashBackgroundColor '#f5f5f7' --splashBackgroundColorDark '#1c1c1e'
```

which rewrites `android/app/src/main/res/mipmap-*`, `drawable-*/splash.png`
and `ios/App/App/Assets.xcassets`.

## Versions

`android/app/build.gradle` reads `apps/reach/VERSION` for `versionName`
(`0.1.0`) and derives `versionCode` from it (`0.1.0` → `100`), so Android
follows the agents automatically. The iOS `MARKETING_VERSION` lives in
`ios/App/App.xcodeproj/project.pbxproj` (set in Xcode under *General* or by
editing the two occurrences) and must be bumped by hand with `VERSION`.
