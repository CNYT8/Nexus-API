import type { QueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { getSystemOptions, resetModelRatios } from '../api'

// A failed POST may still have persisted. Always reconcile from a new read,
// and never report success from the write acknowledgement alone.
export async function resetModelRatiosAndRefresh(queryClient: QueryClient) {
  const errors: string[] = []
  try {
    const response = await resetModelRatios()
    if (response.success !== true) {
      throw new Error(
        response.message || i18next.t('Failed to reset model ratios')
      )
    }
  } catch (cause) {
    errors.push(
      cause instanceof Error && cause.message
        ? cause.message
        : i18next.t('Failed to reset model ratios')
    )
  }

  try {
    await queryClient.cancelQueries({ queryKey: ['system-options'] })
    await queryClient.invalidateQueries({
      queryKey: ['system-options'],
      refetchType: 'none',
    })
    await queryClient.fetchQuery({
      queryKey: ['system-options'],
      queryFn: getSystemOptions,
      staleTime: 0,
      retry: false,
    })
  } catch {
    errors.push(i18next.t('Failed to refresh settings'))
  }

  try {
    await queryClient.cancelQueries({ queryKey: ['pricing'] })
    await queryClient.invalidateQueries(
      { queryKey: ['pricing'] },
      { throwOnError: true }
    )
  } catch {
    errors.push(i18next.t('Failed to refresh settings'))
  }

  if (errors.length) throw new Error([...new Set(errors)].join('\n'))
}
