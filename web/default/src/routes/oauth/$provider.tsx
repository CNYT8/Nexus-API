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
import { useEffect, useRef, useState } from 'react'
import type { AxiosRequestConfig } from 'axios'
import {
  createFileRoute,
  useNavigate,
  useParams,
  useSearch,
} from '@tanstack/react-router'
import i18next from 'i18next'
import { toast } from 'sonner'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'
import { api } from '@/lib/api'
import { AuthLayout } from '@/features/auth/auth-layout'
import { OtpForm } from '@/features/auth/otp/components/otp-form'
import { OAuthCallbackScreen } from '@/features/auth/components/oauth-callback-screen'
import { OAUTH_BIND_STORAGE_KEY } from '@/features/auth/constants'
import { consumeOAuthReturn } from '@/features/auth/lib/oauth-return-target'
import {
  getOAuthSessionStorage,
  resolveOAuthCallbackMode,
} from '@/features/auth/lib/oauth-callback-mode'

type OAuthRequestConfig = AxiosRequestConfig & {
  skipBusinessError?: boolean
  skipErrorHandler?: boolean
}

function OAuthCallback() {
  const navigate = useNavigate()
  const { provider } = useParams({ from: '/oauth/$provider' }) as {
    provider: string
  }
  const search = useSearch({ from: '/oauth/$provider' }) as {
    code?: string
    state?: string
    redirect?: string
    error?: string
  }
  const executed = useRef(false)
  const bindingStateMatches = useRef(true)
  const [verificationTarget, setVerificationTarget] = useState<string | null>(null)
  const callbackState = search.state ?? ''
  const [mode] = useState<'login' | 'bind'>(() => {
    if (typeof window === 'undefined') return 'login'
    const storage = getOAuthSessionStorage(window)
    // The tab-local marker remains authoritative if the opener was closed.
    // A missing state is handled as failure below, never as a successful bind.
    try {
      const markedState = storage?.getItem(`oauth_bind_flow:${provider}`)
      if (markedState) {
        bindingStateMatches.current = markedState === callbackState
        return 'bind'
      }
    } catch { /* Keep authorization usable when storage is denied. */ }
    return resolveOAuthCallbackMode(provider, callbackState, { opener: window.opener, storage })
  })

  useEffect(() => {
    if (executed.current) return
    executed.current = true
    const returnTarget = consumeOAuthReturn(callbackState)
    try {
      window.sessionStorage.removeItem(`oauth_bind_flow:${provider}`)
    } catch { /* Storage denial must not block the callback. */ }
    ;(async () => {
      const safeNavigate = (target: string) => {
        navigate({ to: target as never, replace: true })
        if (typeof window !== 'undefined') {
          setTimeout(() => {
            const normalizedTarget = target.startsWith('/')
              ? target
              : `/${target}`
            const currentPath =
              window.location.pathname + window.location.search
            if (
              currentPath !== normalizedTarget &&
              currentPath !== `${normalizedTarget}/`
            ) {
              window.location.replace(target)
            }
          }, 100)
        }
      }

      const isBindingFlow = mode === 'bind'
      const notifyBindingResult = (status: 'success' | 'error') => {
        if (typeof window === 'undefined') return
        try {
          window.localStorage.setItem(
            OAUTH_BIND_STORAGE_KEY,
            JSON.stringify({
              provider,
              status,
              timestamp: Date.now(),
            })
          )
        } catch (_error) {
          // ignore storage write failures
          void _error
        }
      }

      const closeBindingWindow = () => {
        if (typeof window === 'undefined') return
        window.close()
        setTimeout(() => {
          if (!window.closed) {
            window.location.replace('/profile')
          }
        }, 200)
      }

      const redirectAfterLogin = () => {
        const to = returnTarget || '/dashboard'
        safeNavigate(to)
        toast.success(i18next.t('Signed in successfully!'))
      }

      const handleBindingFailure = (message: string) => {
        notifyBindingResult('error')
        toast.error(message)
        closeBindingWindow()
      }

      const handleLoginFailure = (message: string) => {
        toast.error(message)
        safeNavigate(returnTarget ? `/sign-in?${new URLSearchParams({ redirect: returnTarget })}` : '/sign-in')
      }

      if (!search.code || !callbackState || search.error || (isBindingFlow && !bindingStateMatches.current)) {
        if (isBindingFlow) handleBindingFailure(i18next.t('授权失败'))
        else handleLoginFailure(i18next.t('授权失败'))
        return
      }

      try {
        const config: OAuthRequestConfig = {
          params: { code: search.code, state: search.state },
          skipBusinessError: true,
          skipErrorHandler: true,
        }
        const res = await api.get(`/api/oauth/${encodeURIComponent(provider)}`, config)
        if (res?.data?.success) {
          const { message } = res.data
          const loginUser = (res.data?.data ?? null) as AuthUser | null
          // Check if this is a bind operation
          if (message === 'bind' || res.data?.data?.action === 'bind') {
            toast.success(i18next.t('Binding successful!'))
            notifyBindingResult('success')
            if (isBindingFlow) {
              // Close the callback window if we opened a new tab for binding
              closeBindingWindow()
            } else {
              safeNavigate('/profile')
            }
            return
          }
          if (isBindingFlow) {
            handleBindingFailure(i18next.t('授权失败'))
            return
          }
          if (res.data.data?.require_2fa === true) {
            setVerificationTarget(returnTarget || '/dashboard')
            return
          }
          // Never treat an existing session as proof of this OAuth attempt.
          if (loginUser?.id != null) {
            useAuthStore.getState().auth.setUser(loginUser)
            try {
              if (typeof window !== 'undefined' && loginUser.id != null) {
                window.localStorage.setItem('uid', String(loginUser.id))
              }
            } catch (_error) {
              void _error
            }
            redirectAfterLogin()
            return
          }
          handleLoginFailure(res?.data?.message || i18next.t('授权失败'))
          return
        }
        const message = res?.data?.message || i18next.t('授权失败')
        if (isBindingFlow) {
          handleBindingFailure(message)
        } else {
          await handleLoginFailure(message)
        }
        return
      } catch (error) {
        const message = ((error &&
          typeof error === 'object' &&
          'response' in error &&
          (error as { response?: { data?: { message?: string } } }).response
            ?.data?.message) ??
          (error instanceof Error ? error.message : undefined) ??
          i18next.t('授权失败')) as string

        if (isBindingFlow) {
          handleBindingFailure(message)
          return
        }
        await handleLoginFailure(message)
        return
      }
    })()
  }, [mode, navigate, provider, search])

  if (verificationTarget) {
    return (
      <AuthLayout>
        <div className='w-full space-y-6'>
          <h2 className='text-center text-2xl font-semibold'>{i18next.t('请完成双重验证以继续登录')}</h2>
          <OtpForm
            onVerified={() => window.location.replace(verificationTarget)}
            onVerificationBack={() => window.location.replace(`/sign-in?${new URLSearchParams({ redirect: verificationTarget })}`)}
          />
        </div>
      </AuthLayout>
    )
  }
  return <OAuthCallbackScreen provider={provider} mode={mode} />
}

export const Route = createFileRoute('/oauth/$provider')({
  component: OAuthCallback,
})
