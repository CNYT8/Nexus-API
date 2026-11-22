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
import { useState, useCallback, useRef, useEffect } from 'react'
import { useAuthStore } from '@/stores/auth-store'
import i18next from 'i18next'
import { toast } from 'sonner'
import { requestWaffoPayment, isApiSuccess } from '../api'

function getPaymentUrl(data: unknown): string | null {
  if (!data || typeof data !== 'object') {
    return null
  }

  if ('payment_url' in data && typeof data.payment_url === 'string') {
    try {
      const url = new URL(data.payment_url)
      if (url.protocol === 'https:' || url.protocol === 'http:') return url.href
    } catch { /* invalid checkout URL */ }
  }

  return null
}

function getErrorMessage(message: string | undefined, data: unknown): string {
  if (typeof data === 'string' && data.trim()) {
    return data
  }

  return message || i18next.t('Payment request failed')
}

/**
 * Hook for handling Waffo payment processing
 */
export function useWaffoPayment() {
  const [processing, setProcessing] = useState(false)
  const busy = useRef(false)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  const processWaffoPayment = useCallback(
    async (topupAmount: number, payMethodIndex?: number) => {
      if (busy.current || !mounted.current) return false
      const account = useAuthStore.getState().auth.user?.id
      const isCurrent = () => mounted.current && account === useAuthStore.getState().auth.user?.id
      busy.current = true
      setProcessing(true)

      try {
        const response = await requestWaffoPayment({
          amount: Math.floor(topupAmount),
          pay_method_index: payMethodIndex,
        })

        if (!isCurrent()) return false
        if (isApiSuccess(response)) {
          const paymentUrl = getPaymentUrl(response.data)

          if (paymentUrl) {
            window.location.assign(paymentUrl)
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        toast.error(getErrorMessage(response.message, response.data))
        return false
      } catch (_error) {
        if (isCurrent()) toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        busy.current = false
        if (mounted.current) setProcessing(false)
      }
    },
    []
  )

  return { processing, processWaffoPayment }
}
