// native.js — the one bridge between a web UI and its native backend.
//
//   import { native } from './shared/native.js';
//   const info = await native.call('app.info');
//   const stop = native.on('vitals.sample', s => render(s));
//
// Wire format (identical on every transport, see docs/ARCHITECTURE.md §4):
//   request   {id, method, params}
//   response  {id, result} | {id, error: {code, message, details?}}
//   event     {event, data}
//
// Transports, chosen automatically:
//   • WKWebView (macOS): window.webkit.messageHandlers.native.postMessage(req) → Promise
//   • WebView2 (Windows): window.__nativeCall(json) → Promise<string>
//   • Browser / phone:   WebSocket to ws(s)://<host>/ws (+ ?token=…)
// Backends push events by calling window.__nativeEvent(json) (WKWebView / WebView2) or by
// sending an event frame on the socket.

const listeners = new Map();   // event name → Set<fn>
const pending = new Map();     // id → {resolve, reject}
let nextId = 1;

function emit(name, data) {
  const set = listeners.get(name);
  if (set) for (const fn of [...set]) { try { fn(data); } catch (e) { console.error(e); } }
  const any = listeners.get('*');
  if (any) for (const fn of [...any]) { try { fn(name, data); } catch (e) { console.error(e); } }
}

function makeError(err) {
  const e = new Error(err?.message || 'error');
  e.code = err?.code || 'internal';
  e.details = err?.details;
  return e;
}

function handleIncoming(msg) {
  if (typeof msg === 'string') { try { msg = JSON.parse(msg); } catch { return; } }
  if (!msg || typeof msg !== 'object') return;
  if (msg.event) { emit(msg.event, msg.data); return; }
  if (msg.id !== undefined && pending.has(msg.id)) {
    const p = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) p.reject(makeError(msg.error)); else p.resolve(msg.result);
  }
}

// Backends call this to push an event or a late response.
window.__nativeEvent = (json) => handleIncoming(json);

// ---------------------------------------------------------------- transports

function wkTransport() {
  const h = window.webkit?.messageHandlers?.native;
  if (!h) return null;
  return {
    kind: 'wkwebview',
    send(req) {
      // WKScriptMessageHandlerWithReply resolves the promise with the backend's reply.
      return Promise.resolve(h.postMessage(req)).then(handleReply);
    },
  };
}

function webview2Transport() {
  if (typeof window.__nativeCall !== 'function') return null;
  return {
    kind: 'webview2',
    send(req) { return window.__nativeCall(JSON.stringify(req)).then(handleReply); },
  };
}

function handleReply(reply) {
  if (typeof reply === 'string') { try { reply = JSON.parse(reply); } catch { reply = { result: reply }; } }
  if (reply && reply.error) throw makeError(reply.error);
  return reply ? reply.result : undefined;
}

function socketTransport(url) {
  let ws = null, openPromise = null, closedByUs = false, backoff = 500;
  const queue = [];

  function connect() {
    if (openPromise) return openPromise;
    openPromise = new Promise((resolve, reject) => {
      ws = new WebSocket(url);
      ws.binaryType = 'arraybuffer';
      ws.onopen = () => { backoff = 500; emit('native.connected', { url }); while (queue.length) ws.send(queue.shift()); resolve(); };
      ws.onmessage = (ev) => {
        if (typeof ev.data === 'string') handleIncoming(ev.data);
        else emit('native.binary', ev.data);
      };
      ws.onerror = () => { /* onclose follows */ };
      ws.onclose = (ev) => {
        openPromise = null; ws = null;
        for (const [id, p] of pending) { pending.delete(id); p.reject(Object.assign(new Error('disconnected'), { code: 'disconnected' })); }
        emit('native.disconnected', { code: ev.code, reason: ev.reason });
        reject(Object.assign(new Error('disconnected'), { code: 'disconnected' }));
        if (!closedByUs) { setTimeout(connect, backoff); backoff = Math.min(backoff * 2, 10000); }
      };
    });
    openPromise.catch(() => {});
    return openPromise;
  }

  connect();
  return {
    kind: 'websocket',
    send(req) {
      return new Promise((resolve, reject) => {
        pending.set(req.id, { resolve, reject });
        const json = JSON.stringify(req);
        if (ws && ws.readyState === WebSocket.OPEN) ws.send(json);
        else { queue.push(json); connect(); }
      });
    },
    close() { closedByUs = true; ws?.close(); },
    get connected() { return !!ws && ws.readyState === WebSocket.OPEN; },
  };
}

function defaultSocketURL() {
  const u = new URL(location.href);
  const proto = u.protocol === 'https:' ? 'wss:' : 'ws:';
  const token = u.searchParams.get('token') || localStorage.getItem('native.token') || '';
  return `${proto}//${u.host}/ws${token ? `?token=${encodeURIComponent(token)}` : ''}`;
}

let transport = null;
function getTransport() {
  if (transport) return transport;
  transport = wkTransport() || webview2Transport() || socketTransport(defaultSocketURL());
  return transport;
}

// ---------------------------------------------------------------- public API

export const native = {
  /** Call a backend method. Rejects with an Error carrying .code on failure. */
  call(method, params = {}) {
    const req = { id: nextId++, method, params };
    return getTransport().send(req);
  },
  /** Subscribe to an event ('*' receives (name, data) for everything). Returns an unsubscribe fn. */
  on(event, fn) {
    if (!listeners.has(event)) listeners.set(event, new Set());
    listeners.get(event).add(fn);
    return () => listeners.get(event)?.delete(fn);
  },
  /** Which transport is in use: 'wkwebview' | 'webview2' | 'websocket'. */
  get transport() { return getTransport().kind; },
  /** Explicitly connect to a remote agent (Reach phone app). */
  connect(url) {
    if (transport && transport.close) transport.close();
    transport = socketTransport(url);
    return transport;
  },
  get connected() { const t = getTransport(); return t.kind === 'websocket' ? t.connected : true; },
  /** Test hook: inject an incoming frame as if the backend sent it. */
  _inject: handleIncoming,
};
