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

import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { api } from '@/lib/api'
import {
  WEB_RISK_REQUIRED_EVENT,
  dispatchWebRiskVerificationRequired,
} from '@/lib/web-risk'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Turnstile } from '@/components/turnstile'

type WebRiskRequiredEvent = CustomEvent<{ siteKey?: string }>

export function WebRiskVerificationDialog() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [required, setRequired] = useState(false)
  const [siteKey, setSiteKey] = useState('')
  const [verifying, setVerifying] = useState(false)
  const [widgetKey, setWidgetKey] = useState(0)

  useEffect(() => {
    const handleRequired = (event: Event) => {
      const detail = (event as WebRiskRequiredEvent).detail
      if (detail?.siteKey) setSiteKey(detail.siteKey)
      setRequired(true)
    }
    window.addEventListener(WEB_RISK_REQUIRED_EVENT, handleRequired)
    return () =>
      window.removeEventListener(WEB_RISK_REQUIRED_EVENT, handleRequired)
  }, [])

  useEffect(() => {
    if (!userId) return
    api
      .get('/api/user/web-risk/status', {
        skipErrorHandler: true,
        disableDuplicate: true,
      })
      .then((response) => {
        const data = response?.data?.data
        if (response?.data?.success && data?.required) {
          dispatchWebRiskVerificationRequired(data.turnstile_site_key || '')
        }
      })
      .catch(() => {})
  }, [userId])

  const resetWidget = useCallback(() => {
    setWidgetKey((value) => value + 1)
  }, [])

  const verify = useCallback(
    async (token: string) => {
      if (!token) return
      setVerifying(true)
      try {
        const response = await api.post(
          '/api/user/web-risk/verify',
          { turnstile: token },
          { skipErrorHandler: true }
        )
        if (response?.data?.success) {
          window.location.reload()
          return
        }
        toast.error(response?.data?.message || t('验证失败，请重试'))
      } catch (error) {
        const message = (
          error as { response?: { data?: { message?: string } } }
        )?.response?.data?.message
        toast.error(message || t('验证失败，请重试'))
      } finally {
        setVerifying(false)
        resetWidget()
      }
    },
    [resetWidget, t]
  )

  return (
    <Dialog open={required} onOpenChange={() => {}}>
      <DialogContent showCloseButton={false} className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('请验证您的真实性')}</DialogTitle>
          <DialogDescription>
            {t('检测到账号短时间内使用多个网络地址，请完成验证后继续')}
          </DialogDescription>
        </DialogHeader>
        <div className='flex min-h-20 flex-col items-center justify-center gap-3 py-2'>
          {siteKey && (
            <Turnstile
              key={widgetKey}
              siteKey={siteKey}
              onVerify={verify}
              onExpire={resetWidget}
            />
          )}
          {verifying && (
            <p className='text-muted-foreground text-sm'>{t('正在验证')}</p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
