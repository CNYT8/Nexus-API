import { useCallback, useEffect, useState } from 'react'
import { CanceledError } from 'axios'
import { useAuthStore } from '@/stores/auth-store'

export type SecuritySession = { owner: number; user: string; uid: string; auth: string }

// Cookies are HttpOnly: the server remains authoritative about session validity.
// Bind work to the visible account/permission snapshot and fail closed on change.
export function captureSecuritySession(owner: number): SecuritySession | null {
  try {
    const user = localStorage.getItem('user')
    const uid = localStorage.getItem('uid')
    const stored = user ? JSON.parse(user) : null
    const current = useAuthStore.getState().auth.user
    if (!user || !current || current.id !== owner || stored?.id !== owner ||
      uid !== String(owner) || stored.role !== current.role ||
      (current.status !== undefined && current.status !== 1)) return null
    return { owner, user, uid, auth: JSON.stringify(current) }
  } catch {
    return null
  }
}

export function isSecuritySessionCurrent(snapshot: SecuritySession | null) {
  if (!snapshot) return false
  const current = captureSecuritySession(snapshot.owner)
  return !!current && current.user === snapshot.user && current.uid === snapshot.uid && current.auth === snapshot.auth
}

const sessions = new WeakMap<AbortSignal, SecuritySession>()
export function bindSecuritySession(signal: AbortSignal, owner: number, snapshot = captureSecuritySession(owner)) {
  const bound = sessions.get(signal) ?? snapshot
  if (!bound || bound.owner !== owner || signal.aborted || !isSecuritySessionCurrent(bound)) {
    throw new CanceledError('Security session changed')
  }
  sessions.set(signal, bound)
  return () => {
    if (signal.aborted || !isSecuritySessionCurrent(bound)) throw new CanceledError('Security session changed')
  }
}

export function useSecuritySession() {
  const user = useAuthStore((state) => state.auth.user)
  const [snapshot] = useState(() => user ? captureSecuritySession(user.id) : null)
  const [invalid, setInvalid] = useState(false)
  const isCurrent = useCallback(() => !invalid && isSecuritySessionCurrent(snapshot), [invalid, snapshot])
  useEffect(() => {
    const check = () => { if (!isSecuritySessionCurrent(snapshot)) setInvalid(true) }
    const storage = (event: StorageEvent) => {
      if (event.key === null || event.key === 'user' || event.key === 'uid') setInvalid(true)
    }
    check()
    window.addEventListener('storage', storage)
    window.addEventListener('focus', check)
    document.addEventListener('visibilitychange', check)
    return () => {
      window.removeEventListener('storage', storage)
      window.removeEventListener('focus', check)
      document.removeEventListener('visibilitychange', check)
    }
  }, [snapshot, user])
  return { snapshot, isCurrent, current: isCurrent() }
}

// Synchronous clipboard events/execCommand keep the final guard next to the
// actual write, including on older iOS where navigator.clipboard is absent.
export function copySecuritySecret(value: string, isCurrent: () => boolean) {
  if (!value || !isCurrent()) return false
  const field = document.createElement('textarea')
  field.value = value
  field.readOnly = true
  field.style.position = 'fixed'
  field.style.opacity = '0'
  document.body.appendChild(field)
  try {
    field.focus()
    field.select()
    field.setSelectionRange(0, value.length)
    return isCurrent() && document.execCommand('copy')
  } finally {
    field.remove()
  }
}
