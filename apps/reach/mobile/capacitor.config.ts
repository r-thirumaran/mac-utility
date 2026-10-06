import type { CapacitorConfig } from '@capacitor/cli';

// Reach — native wrapper around the phone web app in apps/reach/ui/static.
// `npm run sync:www` copies that app (plus shared/ui) into ./www before every
// `cap sync`; nothing in www/ is edited by hand.
const config: CapacitorConfig = {
  appId: 'com.thiru.reach',
  appName: 'Reach',
  webDir: 'www',
  server: {
    // The app talks plain http:// and ws:// to the agents on the LAN
    // (docs/reach-api.md §1). Serving the bundle from http://localhost keeps the
    // WebView a secure context (localStorage, wake lock) while an http: origin
    // does not treat those LAN calls as blocked mixed content on Android.
    androidScheme: 'http',
    cleartext: true,
  },
  android: {
    // Belt and braces next to androidScheme http: the WebView never blocks a
    // plain http/ws request as mixed content. android:usesCleartextTraffic in
    // android/app/src/main/AndroidManifest.xml lets the OS network stack allow it.
    allowMixedContent: true,
    backgroundColor: '#f5f5f7',
  },
  ios: {
    // The page handles its own insets with env(safe-area-inset-*) (viewport-fit=cover).
    contentInset: 'never',
    backgroundColor: '#f5f5f7',
  },
};

export default config;
