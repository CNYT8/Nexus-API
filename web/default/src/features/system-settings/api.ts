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
import i18next from 'i18next'
import { api } from '@/lib/api'
import type {
  ConfirmPaymentComplianceResponse,
  DatabaseIntegrityResponse,
  LogCleanupTaskResponse,
  FetchUpstreamRatiosRequest,
  InitializeWalletLedgerResponse,
  OutputSensitiveConfig,
  OutputSensitiveConfigResponse,
  SystemOptionsResponse,
  UpdateOptionRequest,
  UpdateOptionResponse,
  UpstreamChannelsResponse,
  UpstreamRatiosResponse,
} from './types'

export async function getSystemOptions() {
  // A post-save read must not reuse an older in-flight option snapshot.
  const res = await api.get<SystemOptionsResponse>('/api/option/', {
    disableDuplicate: true,
  })
  if (!res.data.success) {
    throw new Error(res.data.message || i18next.t('Failed to load settings'))
  }
  return res.data
}

export async function updateSystemOption(request: UpdateOptionRequest) {
  const res = await api.put<UpdateOptionResponse>('/api/option/', request)
  return res.data
}

export type RebateConfigSnapshot = {
  fields: Record<string, string>
  revision: string
}

type RebateConfigResponse = {
  success: boolean
  message: string
  data?: RebateConfigSnapshot
}

function confirmedRebateConfig(response: RebateConfigResponse) {
  if (!response.success || !response.data?.revision || !response.data.fields) {
    throw new Error(response.message || 'Failed to update setting')
  }
  return response.data
}

export async function getRebateConfig(signal?: AbortSignal) {
  const { data } = await api.get<RebateConfigResponse>('/api/option/rebate', {
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
    signal,
  })
  return confirmedRebateConfig(data)
}

export async function updateRebateConfig(request: RebateConfigSnapshot) {
  const { data } = await api.put<RebateConfigResponse>(
    '/api/option/rebate',
    request,
    { skipBusinessError: true, skipErrorHandler: true }
  )
  // Business failures must reject mutateAsync so the form retains its draft.
  return confirmedRebateConfig(data)
}

export async function getOutputSensitiveConfig() {
  const res = await api.get<OutputSensitiveConfigResponse>(
    '/api/option/output-sensitive'
  )
  return res.data
}

export async function updateOutputSensitiveConfig(
  request: OutputSensitiveConfig
) {
  const res = await api.put<UpdateOptionResponse>(
    '/api/option/output-sensitive',
    request
  )
  return res.data
}

export async function confirmPaymentCompliance() {
  const res = await api.post<ConfirmPaymentComplianceResponse>(
    '/api/option/payment_compliance',
    { confirmed: true }
  )
  return res.data
}

function confirmedLogCleanupTask(response: LogCleanupTaskResponse) {
  const task = response.data
  if (
    !response.success ||
    !task ||
    !Number.isSafeInteger(task.id) ||
    task.id <= 0 ||
    task.kind !== 'log_cleanup' ||
    !['queued', 'running', 'succeeded', 'failed'].includes(task.status)
  ) {
    throw new Error(response.message || i18next.t('Failed to load cleanup task'))
  }
  return task
}

export async function createLogCleanupTask(targetTimestamp: number) {
  const res = await api.post<LogCleanupTaskResponse>('/api/log/cleanup', null, {
    params: { target_timestamp: targetTimestamp },
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return confirmedLogCleanupTask(res.data)
}

export async function getLogCleanupTask(id: number, signal?: AbortSignal) {
  const res = await api.get<LogCleanupTaskResponse>(`/api/log/cleanup/${id}`, {
    signal,
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return confirmedLogCleanupTask(res.data)
}

export async function resetModelRatios() {
  const res = await api.post<UpdateOptionResponse>(
    '/api/option/rest_model_ratio'
  )
  return res.data
}

export async function getUpstreamChannels() {
  const res = await api.get<UpstreamChannelsResponse>(
    '/api/ratio_sync/channels'
  )
  return res.data
}

export async function fetchUpstreamRatios(request: FetchUpstreamRatiosRequest) {
  const res = await api.post<UpstreamRatiosResponse>(
    '/api/ratio_sync/fetch',
    request
  )
  return res.data
}

export async function checkDatabaseIntegrity() {
  const res = await api.get<DatabaseIntegrityResponse>(
    '/api/option/db_integrity',
    { disableDuplicate: true }
  )
  return res.data
}

export async function initializeWalletLedger() {
  const res = await api.post<InitializeWalletLedgerResponse>(
    '/api/option/wallet_ledger/init',
    undefined,
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return res.data
}
