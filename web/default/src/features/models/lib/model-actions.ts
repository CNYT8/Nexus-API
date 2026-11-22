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
import { type QueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'
import { updateModelStatus, batchDeleteModels, type ModelDeleteOptions } from '../api'
import { modelsQueryKeys } from './query-keys'

// ============================================================================
// Model Status Actions
// ============================================================================

/**
 * Enable a model
 */
export async function handleEnableModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  try {
    const response = await updateModelStatus(id, 1)
    if (response.success) {
      toast.success(i18next.t('Model enabled successfully'))
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    } else {
      toast.error(response.message || i18next.t('Failed to enable model'))
    }
  } catch (error: unknown) {
    toast.error(
      (error as Error)?.message || i18next.t('Failed to enable model')
    )
  }
}

/**
 * Disable a model
 */
export async function handleDisableModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  try {
    const response = await updateModelStatus(id, 0)
    if (response.success) {
      toast.success(i18next.t('Model disabled successfully'))
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    } else {
      toast.error(response.message || i18next.t('Failed to disable model'))
    }
  } catch (error: unknown) {
    toast.error(
      (error as Error)?.message || i18next.t('Failed to disable model')
    )
  }
}

/**
 * Toggle model status
 */
export async function handleToggleModelStatus(
  id: number,
  currentStatus: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (currentStatus === 1) {
    await handleDisableModel(id, queryClient, onSuccess)
  } else {
    await handleEnableModel(id, queryClient, onSuccess)
  }
}

// ============================================================================
// Model Delete Actions
// ============================================================================

/**
 * Delete a single model
 */
export async function handleDeleteModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<boolean> {
  return handleBatchDeleteModels([id], queryClient, onSuccess)
}

/**
 * Batch delete models
 */
export async function handleBatchDeleteModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: (deletedIds: number[]) => void,
  options: ModelDeleteOptions = { remove_from_channels: false, remove_pricing: false }
): Promise<boolean> {
  const uniqueIds = [...new Set(ids)]
  if (uniqueIds.length === 0) {
    toast.error(i18next.t('Please select at least one model'))
    return false
  }
  try {
    const response = await batchDeleteModels(uniqueIds, options)
    if (!response.success) throw new Error(response.message || i18next.t('Batch delete failed'))
    const deleted = response.data?.deleted_count ?? 0
    if (deleted > 0) toast.success(i18next.t('Successfully deleted {{count}} model(s)', { count: deleted }))
    if (response.data?.updated_channels) {
      toast.success(i18next.t('Updated {{count}} channel(s)', { count: response.data.updated_channels }))
    }
    // The endpoint is transactional. Never infer successful IDs from a partial count.
    if (deleted !== uniqueIds.length) {
      toast.error(i18next.t('Failed to delete {{count}} model(s)', { count: Math.max(0, uniqueIds.length - deleted) }))
      return false
    }
    onSuccess?.(uniqueIds)
    return true
  } catch (error: unknown) {
    toast.error((error as Error)?.message || i18next.t('Batch delete failed'))
    return false
  } finally {
    await queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
    await queryClient?.invalidateQueries({ queryKey: ['pricing'] })
    if (options.remove_from_channels) await queryClient?.invalidateQueries({ queryKey: ['channels'] })
    if (options.remove_pricing) await queryClient?.invalidateQueries({ queryKey: ['system-options'] })
  }
}

// ============================================================================
// Batch Status Actions
// ============================================================================

/**
 * Batch enable models
 */
export async function handleBatchEnableModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (ids.length === 0) {
    toast.error(i18next.t('Please select at least one model'))
    return
  }

  try {
    const enablePromises = ids.map((id) => updateModelStatus(id, 1))
    const results = await Promise.all(enablePromises)

    let successCount = 0
    let failedCount = 0

    results.forEach((res) => {
      if (res.success) {
        successCount++
      } else {
        failedCount++
      }
    })

    if (successCount > 0) {
      toast.success(
        i18next.t('Successfully enabled {{count}} model(s)', {
          count: successCount,
        })
      )
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    }

    if (failedCount > 0) {
      toast.error(
        i18next.t('Failed to enable {{count}} model(s)', { count: failedCount })
      )
    }
  } catch (error: unknown) {
    toast.error((error as Error)?.message || i18next.t('Batch enable failed'))
  }
}

/**
 * Batch disable models
 */
export async function handleBatchDisableModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (ids.length === 0) {
    toast.error(i18next.t('Please select at least one model'))
    return
  }

  try {
    const disablePromises = ids.map((id) => updateModelStatus(id, 0))
    const results = await Promise.all(disablePromises)

    let successCount = 0
    let failedCount = 0

    results.forEach((res) => {
      if (res.success) {
        successCount++
      } else {
        failedCount++
      }
    })

    if (successCount > 0) {
      toast.success(
        i18next.t('Successfully disabled {{count}} model(s)', {
          count: successCount,
        })
      )
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    }

    if (failedCount > 0) {
      toast.error(
        i18next.t('Failed to disable {{count}} model(s)', {
          count: failedCount,
        })
      )
    }
  } catch (error: unknown) {
    toast.error((error as Error)?.message || i18next.t('Batch disable failed'))
  }
}
