import { useEffect, useRef, useState } from 'react'
import i18next from 'i18next'
import { useAuthStore } from '@/stores/auth-store'
import { isVerificationRequiredError } from '@/lib/secure-verification'
import type {
  SecureVerificationState,
  VerificationMethod,
  VerificationMethods,
} from '@/features/auth/secure-verification/types'
import { getSecurityVerificationMethods, verifySecurityAction } from '../api'
import { securityErrorKey } from '../lib/security-management'

export function useSecurityVerification() {
  const owner = useAuthStore((state) => state.auth.user?.id)
  const active = useRef<AbortController | null>(null)
  const retry = useRef<(() => Promise<unknown>) | null>(null)
  const locked = useRef(false)
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const [methods, setMethods] = useState<VerificationMethods>({
    has2FA: false,
    hasPasskey: false,
    passkeySupported: false,
  })
  const [state, setState] = useState<SecureVerificationState>({
    method: null,
    code: '',
    loading: false,
  })

  useEffect(() => {
    return () => {
      active.current?.abort()
      active.current = null
      retry.current = null
    }
  }, [owner])

  const isCurrent = (controller: AbortController) =>
    active.current === controller &&
    !controller.signal.aborted &&
    useAuthStore.getState().auth.user?.id === owner &&
    localStorage.getItem('uid') === String(owner)

  const withVerification = async (action: () => Promise<unknown>) => {
    if (
      locked.current ||
      owner === undefined ||
      useAuthStore.getState().auth.user?.id !== owner ||
      localStorage.getItem('uid') !== String(owner)
    )
      return false
    active.current?.abort()
    const controller = new AbortController()
    active.current = controller
    retry.current = null
    locked.current = true
    setError('')
    try {
      try {
        return await action()
      } catch (cause) {
        if (!isCurrent(controller)) return false
        if (!isVerificationRequiredError(cause)) throw cause
      }
      const available = await getSecurityVerificationMethods(
        controller.signal,
        owner
      )
      if (!isCurrent(controller)) return false
      setMethods(available)
      const method =
        available.hasPasskey && available.passkeySupported
          ? 'passkey'
          : available.has2FA
            ? '2fa'
            : null
      if (!method) {
        setError(
          i18next.t('请完成安全验证；未设置验证方式时，请重新登录后重试')
        )
        return null
      }
      retry.current = action
      setState({ method, code: '', loading: false })
      setOpen(true)
      return null
    } catch (cause) {
      if (isCurrent(controller)) {
        setError(
          i18next.t(
            securityErrorKey(
              cause,
              '请完成安全验证；未设置验证方式时，请重新登录后重试'
            )
          )
        )
      }
      return false
    } finally {
      locked.current = false
    }
  }

  const executeVerification = async (method: VerificationMethod, code = '') => {
    const controller = active.current
    const action = retry.current
    if (
      !controller ||
      !action ||
      owner === undefined ||
      !isCurrent(controller) ||
      locked.current
    )
      return
    locked.current = true
    setState((previous) => ({ ...previous, loading: true }))
    setError('')
    try {
      await verifySecurityAction(method, code, controller.signal, owner)
      if (!isCurrent(controller)) return
      await action()
      if (!isCurrent(controller)) return
      retry.current = null
      setOpen(false)
    } catch (cause) {
      if (isCurrent(controller)) {
        setError(
          i18next.t(
            securityErrorKey(
              cause,
              '请完成安全验证；未设置验证方式时，请重新登录后重试'
            )
          )
        )
      }
    } finally {
      locked.current = false
      if (isCurrent(controller))
        setState((previous) => ({ ...previous, loading: false }))
    }
  }

  const cancel = () => {
    active.current?.abort()
    active.current = null
    retry.current = null
    setOpen(false)
    setState({ method: null, code: '', loading: false })
  }

  return {
    open,
    error,
    methods,
    state: { ...state, description: error || undefined },
    withVerification,
    executeVerification,
    cancel,
    setCode: (code: string) => setState((previous) => ({ ...previous, code })),
    switchMethod: (method: VerificationMethod) =>
      setState((previous) => ({ ...previous, method, code: '' })),
  }
}
