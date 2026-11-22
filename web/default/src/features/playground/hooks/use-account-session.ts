/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useSyncExternalStore } from 'react'
import { useAuthStore } from '@/stores/auth-store'

export function readAccountId(): string | null {
  try {
    const id: unknown = JSON.parse(localStorage.getItem('user') || 'null')?.id
    return id ? String(id) : null
  } catch {
    return null
  }
}

export interface AccountSession {
  userId: string | null
  revision: number
  isCurrent: () => boolean
  onInvalidate: (callback: () => void) => () => void
  invalidate: () => void
}

let revision = 0
const listeners = new Set<() => void>()
function createSession(userId: string | null): AccountSession {
  const invalidators = new Set<() => void>()
  let invalidated = false
  const session: AccountSession = {
    userId,
    revision: revision++,
    isCurrent: () => getSnapshot() === session && userId !== null,
    onInvalidate: (callback) => {
      if (invalidated) callback()
      else invalidators.add(callback)
      return () => {
        invalidators.delete(callback)
      }
    },
    invalidate: () => {
      invalidated = true
      invalidators.forEach((callback) => callback())
      invalidators.clear()
    },
  }
  return session
}
let current = createSession(readAccountId())
function refresh(force = false): AccountSession {
  const userId = readAccountId()
  if (force || userId !== current.userId) {
    const previous = current
    current = createSession(userId)
    previous.invalidate()
    listeners.forEach((listener) => listener())
  }
  return current
}
const getSnapshot = () => refresh()
let stopWatching = () => {}
function subscribe(listener: () => void) {
  listeners.add(listener)
  if (listeners.size === 1) {
    // Zustand 通知同步到每次登出/登录，React 批处理不能合并掉会话边界。
    const unsubscribe = useAuthStore.subscribe((state, previous) => {
      refresh(state.auth.user?.id !== previous.auth.user?.id)
    })
    const onStorage = (event: StorageEvent) => {
      if (event.storageArea && event.storageArea !== localStorage) return
      if (event.key !== null && event.key !== 'user') return
      const idOf = (raw: string | null) => {
        try {
          return String(JSON.parse(raw || 'null')?.id || '')
        } catch {
          return ''
        }
      }
      refresh(
        event.key === null || idOf(event.oldValue) !== idOf(event.newValue)
      )
    }
    window.addEventListener('storage', onStorage)
    refresh()
    stopWatching = () => {
      unsubscribe()
      window.removeEventListener('storage', onStorage)
    }
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0) stopWatching()
  }
}

export function useAccountSession(): AccountSession {
  return useSyncExternalStore(subscribe, getSnapshot)
}
