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
import { useEffect, useRef } from 'react'
import { createFileRoute, useNavigate, useSearch } from '@tanstack/react-router'
import i18next from 'i18next'
import { toast } from 'sonner'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'
import { getSelf } from '@/lib/api'
import { wechatLoginByCode } from '@/features/auth/api'
import { consumeOAuthReturn } from '@/features/auth/lib/oauth-return-target'

function OAuthComponent() {
  const navigate = useNavigate()
  const search = useSearch({ from: '/(auth)/oauth' }) as {
    redirect?: string
    provider?: 'github' | 'discord' | 'oidc' | 'linuxdo' | 'telegram' | 'wechat'
    code?: string
    state?: string
    error?: string
  }
  const executed = useRef(false)

  useEffect(() => {
    if (executed.current) return
    executed.current = true
    // Keep the state entry for the provider callback, which consumes it once.
    if (search.provider && search.provider !== 'wechat' && search.provider !== 'telegram' && /^[a-zA-Z0-9_-]+$/.test(search.provider)) {
      const query = new URLSearchParams()
      if (search.code) query.set('code', search.code)
      if (search.state) query.set('state', search.state)
      if (search.error) query.set('error', search.error)
      window.location.replace(`/oauth/${encodeURIComponent(search.provider)}?${query}`)
      return
    }
    const target = consumeOAuthReturn(search.state || '')
    ;(async () => {
      try {
        if (search.error || search.provider !== 'wechat' || !search.code) throw new Error('Invalid callback')
        const login = await wechatLoginByCode(search.code)
        if (!login.success) throw new Error('Authorization failed')
        const res = await getSelf()
        if (res?.success && res.data?.id != null) {
          useAuthStore.getState().auth.setUser(res.data as AuthUser)
          navigate({ to: target || '/dashboard', replace: true })
          return
        }
      } catch {
        /* empty */
      }
      toast.error(i18next.t('授权失败'))
      navigate({ to: '/sign-in', search: target ? { redirect: target } : {}, replace: true })
    })()
  }, [navigate, search])

  return null
}

export const Route = createFileRoute('/(auth)/oauth')({
  component: OAuthComponent,
})
