import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import { CanceledError, type AxiosRequestHeaders } from 'axios'
import { useAuthStore } from '@/stores/auth-store'
import type { ApiRequestConfig } from '@/lib/api'
import {
  getServerErrorMessage,
  isServerErrorCancelled,
} from '@/lib/server-error-message'
import type { AccountSession } from '@/features/playground/hooks/use-account-session'

// Every plugin panel shares one write lane for the captured account session.
const mutations = new WeakMap<AccountSession, AbortController>()

// Each read lane owns its controller; stale results cannot update a newer view.
// Writes use replace=false so rapid confirmation cannot submit twice.
export function useScriptPluginRequest(
  session: AccountSession,
  replace = true
) {
  const mounted = useRef(false)
  const active = useRef<AbortController | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const cancel = useCallback(() => {
    active.current?.abort()
    active.current = null
    if (mounted.current) {
      setBusy(false)
      setError('')
    }
  }, [])
  const clearError = useCallback(() => setError(''), [])

  useLayoutEffect(() => {
    mounted.current = true
    const unsubscribe = session.onInvalidate(cancel)
    return () => {
      mounted.current = false
      unsubscribe()
      cancel()
    }
  }, [session, cancel])

  const run = useCallback(
    async <T>(
      request: (config: ApiRequestConfig) => Promise<T>,
      onSuccess: (value: T) => void,
      fallback: string
    ) => {
      const authorized = () => {
        const user = useAuthStore.getState().auth.user
        return (
          mounted.current &&
          session.isCurrent() &&
          user?.role === 100 &&
          String(user.id) === session.userId
        )
      }
      if (!authorized() || (active.current && !replace)) return
      if (!replace && mutations.has(session)) {
        setError(fallback)
        return
      }
      active.current?.abort()
      const controller = new AbortController()
      active.current = controller
      if (!replace) mutations.set(session, controller)
      const current = () =>
        authorized() &&
        active.current === controller &&
        !controller.signal.aborted
      setBusy(true)
      setError('')
      try {
        const value = await request({
          signal: controller.signal,
          disableDuplicate: true,
          skipBusinessError: true,
          skipErrorHandler: true,
          headers: {
            'Content-Type': 'application/json',
            'New-Api-User': session.userId,
          },
          // Runs after the shared interceptor reads uid. Recheck synchronously
          // and pin the owner so queued storage events cannot retarget a draft.
          transformRequest: [
            (data: unknown, headers: AxiosRequestHeaders) => {
              if (!current()) {
                controller.abort()
                throw new CanceledError('Account session changed')
              }
              headers.set('New-Api-User', session.userId)
              return data === undefined ? undefined : JSON.stringify(data)
            },
          ],
        })
        if (current()) onSuccess(value)
      } catch (cause: unknown) {
        if (current() && !isServerErrorCancelled(cause)) {
          setError(getServerErrorMessage(cause, fallback))
        }
      } finally {
        if (mutations.get(session) === controller) mutations.delete(session)
        if (current()) {
          active.current = null
          setBusy(false)
        }
      }
    },
    [replace, session]
  )

  return { busy, error, run, cancel, clearError }
}
