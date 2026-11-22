// Targets are tab-local, bound to the server state, and consumed once.
const PREFIX = 'nexus_oauth_return:';
const MAX_AGE = 15 * 60 * 1000;

export function safeOAuthTarget(value, origin = window.location.origin) {
  if (typeof value !== 'string' || !value || value !== value.trim()) return null;
  try {
    if (/[\\\u0000-\u0020\u007f]/.test(value) || value.startsWith('//')) return null;
    const url = new URL(value, origin);
    if (!/^https?:$/.test(url.protocol) || url.origin !== origin || url.username || url.password) return null;
    let path = url.pathname;
    for (let i = 0; i < 6; i++) {
      const next = decodeURIComponent(path);
      if (next === path) break;
      path = next;
    }
    if (decodeURIComponent(path) !== path || /[\\\u0000-\u0020\u007f]/.test(path) || path.startsWith('//')) return null;
    // Normalize decoded dot segments too, without decoding legitimate query text.
    const normalized = new URL(path, origin);
    if (normalized.origin !== origin || /^\/(?:oauth|api|login|register|sign-in|sign-up)(?:\/|$)/i.test(normalized.pathname)) return null;
    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    return null;
  }
}

export function rememberOAuthReturn(state, login) {
  if (!state) return;
  try {
    const from = window.history.state?.usr?.from;
    const original = typeof from === 'string' ? from : from?.pathname
      ? `${from.pathname}${from.search || ''}${from.hash || ''}` : null;
    const requested = new URLSearchParams(window.location.search).get('redirect');
    const target = login ? safeOAuthTarget(requested) || safeOAuthTarget(original) : null;
    const storage = window.sessionStorage;
    for (let i = storage.length - 1; i >= 0; i--) {
      const key = storage.key(i);
      if (key?.startsWith(PREFIX)) storage.removeItem(key);
    }
    storage.setItem(PREFIX + state, JSON.stringify({ target, bind: !login, at: Date.now() }));
  } catch {
    // Private browsing/storage denial must not prevent authorization.
  }
}

export function consumeOAuthReturn(state) {
  try {
    const storage = window.sessionStorage;
    const raw = state ? storage.getItem(PREFIX + state) : null;
    if (state) storage.removeItem(PREFIX + state);
    let pendingBind = false;
    // Missing/mismatched state must not recover any login destination.
    if (!raw) {
      for (let i = storage.length - 1; i >= 0; i--) {
        const key = storage.key(i);
        if (!key?.startsWith(PREFIX)) continue;
        const pending = storage.getItem(key);
        storage.removeItem(key);
        try {
          const entry = JSON.parse(pending);
          const age = Date.now() - entry?.at;
          if (entry?.bind === true && Number.isFinite(age) && age >= 0 && age <= MAX_AGE) pendingBind = true;
        } catch { /* Malformed records are discarded. */ }
      }
      return { target: null, bind: pendingBind, matched: false };
    }
    const entry = JSON.parse(raw);
    const age = Date.now() - entry?.at;
    if (!entry || !Number.isFinite(age) || age < 0 || age > MAX_AGE) return { target: null, bind: false, matched: false };
    return { target: safeOAuthTarget(entry.target), bind: entry.bind === true, matched: true };
  } catch {
    return { target: null, bind: false, matched: false };
  }
}
