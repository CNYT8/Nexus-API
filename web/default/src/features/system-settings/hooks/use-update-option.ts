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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'
import {
  getRebateConfig,
  getSystemOptions,
  updateRebateConfig,
  updateSystemOption,
} from '../api'
import type { UpdateOptionRequest } from '../types'

export function useRebateConfig() {
  return useQuery({
    queryKey: ['rebate-config'],
    queryFn: ({ signal }) => getRebateConfig(signal),
    retry: false,
    staleTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
  })
}

export function useUpdateRebateConfig() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: updateRebateConfig,
    onMutate: () => queryClient.cancelQueries({ queryKey: ['rebate-config'] }),
    onSuccess: async () => {
      // Even an unmounted mutation may finish late. Only a new GET can
      // establish the current group; never publish the PUT response.
      await queryClient.cancelQueries({ queryKey: ['rebate-config'] })
      await queryClient.invalidateQueries({ queryKey: ['rebate-config'] })
      void queryClient.invalidateQueries({ queryKey: ['system-options'] })
      toast.success(i18next.t('Setting updated successfully'))
    },
    onError: async () => {
      await queryClient.cancelQueries({ queryKey: ['rebate-config'] })
      await queryClient.invalidateQueries({
        queryKey: ['rebate-config'],
        refetchType: 'none',
      })
      toast.error(i18next.t('Failed to update setting'))
    },
  })
}

// Configuration keys that require status refresh
const STATUS_RELATED_KEYS = [
  'theme.frontend',
  'TurnstileCheckEnabled',
  'TurnstileSiteKey',
  'HeaderNavModules',
  'SidebarModulesAdmin',
  'Notice',
  'LogConsumeEnabled',
  'QuotaPerUnit',
  'QuotaRemindEnabled',
  'USDExchangeRate',
  'DisplayInCurrencyEnabled',
  'DisplayTokenStatEnabled',
  'DefaultRecordIpLogEnabled',
  'DefaultRecordIpLogForced',
  'general_setting.quota_display_type',
  'general_setting.custom_currency_symbol',
  'general_setting.custom_currency_exchange_rate',
]

type OptionBatch = {
  requests: UpdateOptionRequest[]
  additionalSave?: () => Promise<void>
  sequential?: boolean
}

export function useUpdateOption() {
  const queryClient = useQueryClient()

  const mutation = useMutation({
    mutationFn: async (
      request: UpdateOptionRequest | UpdateOptionRequest[] | OptionBatch
    ) => {
      const batch = !Array.isArray(request) && 'requests' in request ? request : undefined
      const requests = batch
        ? batch.requests
        : Array.isArray(request)
          ? request
          : 'key' in request ? [request] : []
      const writeOption = async (item: UpdateOptionRequest) => {
        const response = await updateSystemOption(item)
        if (!response.success) {
          throw new Error(
            response.message || i18next.t('Failed to update setting')
          )
        }
        return response
      }
      const results: PromiseSettledResult<unknown>[] = []
      if (batch?.sequential) {
        // Turnstile credentials must finish before enabling; stop on failure.
        for (const item of requests) {
          const [result] = await Promise.allSettled([writeOption(item)])
          results.push(result)
          if (result.status === 'rejected') break
        }
      } else {
        const pending: Promise<unknown>[] = requests.map(writeOption)
        if (batch?.additionalSave) {
          pending.push(Promise.resolve().then(batch.additionalSave))
        }
        // Include independent payment writes in the same drain/read boundary.
        results.push(...await Promise.allSettled(pending))
      }
      const errors: string[] = []
      results.forEach((result, index) => {
        if (result.status === 'rejected') {
          errors.push(
            `${requests[index]?.key ?? 'Waffo Pancake'}: ${result.reason instanceof Error ? result.reason.message : i18next.t('Failed to update setting')}`
          )
        }
      })

      let options: Awaited<ReturnType<typeof getSystemOptions>> | undefined
      try {
        await queryClient.cancelQueries({ queryKey: ['system-options'] })
        await queryClient.invalidateQueries({
          queryKey: ['system-options'],
          refetchType: 'none',
        })
        options = await queryClient.fetchQuery({
          queryKey: ['system-options'],
          queryFn: getSystemOptions,
          staleTime: 0,
          retry: false,
        })
      } catch {
        errors.push(i18next.t('Failed to refresh settings'))
      }

      const refreshes: Promise<void>[] = []
      if (requests.some(({ key }) => STATUS_RELATED_KEYS.includes(key))) {
        try {
          window.localStorage.removeItem('status')
        } catch {
          /* Storage may be unavailable; the query still needs refreshing. */
        }
        refreshes.push(
          queryClient.invalidateQueries(
            { queryKey: ['status'] },
            { throwOnError: true }
          )
        )
      }
      if (
        requests.some(({ key }) =>
          /Ratio$|Price$|^billing_setting\.|^tool_(price_)?setting\./.test(key)
        )
      ) {
        refreshes.push(
          queryClient.invalidateQueries(
            { queryKey: ['pricing'] },
            { throwOnError: true }
          )
        )
      }
      const refreshed = await Promise.allSettled(refreshes)
      if (refreshed.some((result) => result.status === 'rejected')) {
        errors.push(i18next.t('Failed to refresh settings'))
      }
      // Refresh failures reject too: callers must never advance a baseline
      // or announce success based only on acknowledged writes.
      if (errors.length) throw new Error(errors.join('\n'))
      return { success: true, message: '', options }
    },
    onMutate: () => queryClient.cancelQueries({ queryKey: ['system-options'] }),
    onError: (error: Error) => {
      toast.error(error.message || i18next.t('Failed to update setting'))
    },
    onSuccess: () => {
      toast.success(i18next.t('Setting updated successfully'))
    },
  })

  const saveOptions = async (
    requests: UpdateOptionRequest[],
    additionalSave?: () => Promise<void>
  ) => {
    if (!requests.length && !additionalSave) return true
    try {
      await mutation.mutateAsync({ requests, additionalSave })
      return true
    } catch {
      // The mutation reports failures and refreshes partially persisted values.
      return false
    }
  }

  const saveOptionsInOrder = async (requests: UpdateOptionRequest[]) => {
    try {
      const result = await mutation.mutateAsync({ requests, sequential: true })
      return result.options
    } catch {
      // Failures are reported above; callers retain unconfirmed drafts.
      return undefined
    }
  }

  return { ...mutation, saveOptions, saveOptionsInOrder }
}
