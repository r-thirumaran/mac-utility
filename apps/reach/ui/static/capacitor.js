// capacitor.js — the optional native layer of the Reach app. app.js imports it
// only when the page runs inside the Capacitor wrapper (apps/reach/mobile); the
// PWA never loads it. It talks to the native plugins through the bridge that
// Capacitor injects into its WebView (window.Capacitor) instead of the
// @capacitor/* JS packages, so the app stays plain ES modules with no bundler:
//   • a light haptic tick on trackpad clicks (@capacitor/haptics)
//   • the Android back button: close an open sheet or dialog, else go back a
//     tab, else send the app to the background (@capacitor/app)
import { hooks } from './core.js';

export function init() {
  const cap = window.Capacitor;
  if (!cap?.isNativePlatform?.() || typeof cap.nativePromise !== 'function') return;
  const call = (plugin, method, options = {}) => cap.nativePromise(plugin, method, options).catch(() => undefined);

  hooks.haptic = () => { call('Haptics', 'impact', { style: 'LIGHT' }); };

  if (cap.getPlatform?.() === 'android' && typeof cap.addListener === 'function') {
    // Registering a listener replaces Capacitor's default (WebView history back, then exit).
    cap.addListener('App', 'backButton', (ev) => {
      if (!ev) return;
      const open = document.querySelector('dialog[open]');
      if (open) { open.close('cancel'); return; }
      if (ev.canGoBack) { history.back(); return; }
      call('App', 'minimizeApp');
    });
  }
}
