import { useCallback, useEffect, useRef, useState } from 'react'
import i18next from 'i18next'
import { useAuthStore } from '@/stores/auth-store'
import { isVerificationRequiredError } from '@/lib/secure-verification'
import type { ApiResponse } from '../types'
import { securityErrorKey } from '../lib/security-management'
import { bindSecuritySession, useSecuritySession } from '../lib/security-session'

// Retain the last confirmed list on failure. Every mutation refreshes it because
// a lost response does not prove that the server rejected the operation.
export function useSecurityResource<T>(load: (signal: AbortSignal, owner: number) => Promise<ApiResponse<T>>) {
  const owner = useAuthStore((state) => state.auth.user?.id)
  const { snapshot, isCurrent: isSessionCurrent, current } = useSecuritySession()
  const lifetime = useRef<AbortController | null>(null)
  const sequence = useRef(0)
  const mutation = useRef(false)
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const isCurrent = useCallback((controller: AbortController) =>
    lifetime.current === controller && !controller.signal.aborted &&
    owner !== undefined && isSessionCurrent(), [owner, isSessionCurrent])

  const refresh = useCallback(async (clearError = true) => {
    const controller = lifetime.current
    if (!controller || owner === undefined || !isCurrent(controller)) return
    const request = ++sequence.current
    setLoading(true)
    if (clearError) setError('')
    try {
      const response = await load(controller.signal, owner)
      if (!response.success || response.data === undefined) throw new Error(response.message || i18next.t('加载安全管理数据失败，请刷新重试'))
      if (isCurrent(controller) && sequence.current === request) setData(response.data)
    } catch (cause) {
      if (isCurrent(controller) && sequence.current === request) setError(i18next.t(securityErrorKey(cause, '加载安全管理数据失败，请刷新重试')))
    } finally {
      if (isCurrent(controller) && sequence.current === request) setLoading(false)
    }
  }, [isCurrent, load, owner])

  useEffect(() => {
    const controller = new AbortController()
    lifetime.current = controller
    if (owner !== undefined && isSessionCurrent()) {
      bindSecuritySession(controller.signal, owner, snapshot)
      void refresh()
    } else {
      controller.abort()
      setData(null)
      setLoading(false)
      setBusy(false)
    }
    return () => {
      controller.abort()
      if (lifetime.current === controller) lifetime.current = null
    }
  }, [refresh, owner, snapshot, isSessionCurrent])

  const run = async <R,>(action: (signal: AbortSignal, owner: number) => Promise<ApiResponse<R>>, accept: (result: R | undefined) => void) => {
    const controller = lifetime.current
    if (!controller || owner === undefined || !isCurrent(controller) || mutation.current) return false
    mutation.current = true
    ++sequence.current
    setLoading(false)
    setBusy(true)
    setError('')
    try {
      const response = await action(controller.signal, owner)
      if (!isCurrent(controller)) return false
      if (!response.success) throw new Error(response.message || i18next.t('操作失败，已尝试刷新状态，请核对后重试'))
      accept(response.data)
      await refresh(false)
      return true
    } catch (cause) {
      if (!isCurrent(controller)) return false
      if (isVerificationRequiredError(cause)) throw cause
      setError(i18next.t(securityErrorKey(cause, '操作失败，已尝试刷新状态，请核对后重试')))
      await refresh(false)
      return false
    } finally {
      mutation.current = false
      if (isCurrent(controller)) setBusy(false)
    }
  }

  return {
    data: current ? data : null,
    loading: current && loading,
    busy: current && busy,
    error: current ? error : i18next.t('登录身份或权限已变化，请关闭后重新打开'),
    isSessionCurrent,
    refresh,
    run,
  }
}
