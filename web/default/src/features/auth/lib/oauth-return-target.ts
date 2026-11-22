const PREFIX = 'nexus_oauth_return:'
const MAX_AGE = 15 * 60 * 1000

export function safeOAuthTarget(value: unknown, origin = window.location.origin): string | null {
  if (typeof value !== 'string' || !value || value !== value.trim()) return null
  try {
    if (/[\\\u0000-\u0020\u007f]/.test(value) || value.startsWith('//')) return null
    const url = new URL(value, origin)
    if (!/^https?:$/.test(url.protocol) || url.origin !== origin || url.username || url.password) return null
    let path = url.pathname
    for (let i = 0; i < 6; i++) {
      const next = decodeURIComponent(path)
      if (next === path) break
      path = next
    }
    if (decodeURIComponent(path) !== path || /[\\\u0000-\u0020\u007f]/.test(path) || path.startsWith('//')) return null
    const normalized = new URL(path, origin)
    if (normalized.origin !== origin || /^\/(?:oauth|api|login|register|sign-in|sign-up)(?:\/|$)/i.test(normalized.pathname)) return null
    return `${url.pathname}${url.search}${url.hash}`
  } catch {
    return null
  }
}

export function rememberOAuthReturn(state: string) {
  if (!state) return
  try {
    const target = safeOAuthTarget(new URLSearchParams(window.location.search).get('redirect'))
    const storage = window.sessionStorage
    for (let i = storage.length - 1; i >= 0; i--) {
      const key = storage.key(i)
      if (key?.startsWith(PREFIX)) storage.removeItem(key)
    }
    storage.setItem(PREFIX + state, JSON.stringify({ target, at: Date.now() }))
  } catch {
    // Storage may be disabled in private browsing. Authorization still works.
  }
}

export function consumeOAuthReturn(state: string): string | null {
  try {
    const storage = window.sessionStorage
    const raw = state ? storage.getItem(PREFIX + state) : null
    if (state) storage.removeItem(PREFIX + state)
    if (!raw) {
      for (let i = storage.length - 1; i >= 0; i--) {
        const key = storage.key(i)
        if (key?.startsWith(PREFIX)) storage.removeItem(key)
      }
      return null
    }
    const entry = raw ? JSON.parse(raw) : null
    const age = Date.now() - entry?.at
    if (!entry || !Number.isFinite(age) || age < 0 || age > MAX_AGE) return null
    return safeOAuthTarget(entry.target)
  } catch {
    return null
  }
}
