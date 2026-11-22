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
import type { GetLogsParams } from '../types'
import { loadLogPolicy, LogPolicyError, normalizeLogExportWindow } from './log-policy'

export function validateLogExport(text: string, isAdmin = true) {
  if (typeof text !== 'string') {
    throw new Error(i18next.t('日志导出响应无效'))
  }
  let count = 0
  let summary: Record<string, unknown> | null = null
  for (const line of text.replace(/^\uFEFF/, '').split(/\r?\n/)) {
    if (!line.trim()) continue
    let row: unknown
    try {
      row = JSON.parse(line)
    } catch {
      throw new Error(i18next.t('日志导出响应无效'))
    }
    if (!row || typeof row !== 'object' || Array.isArray(row)) {
      throw new Error(i18next.t('日志导出响应无效'))
    }
    const record = row as Record<string, unknown>
    // The summary must be a standalone, final non-empty NDJSON record.
    if (summary) {
      throw new Error(i18next.t('日志导出不完整，请重试或缩小筛选范围。'))
    }
    if (Object.prototype.hasOwnProperty.call(record, '_export')) {
      if (
        Object.keys(record).length !== 1 ||
        !record._export ||
        typeof record._export !== 'object' ||
        Array.isArray(record._export)
      ) {
        throw new Error(i18next.t('日志导出不完整，请重试或缩小筛选范围。'))
      }
      summary = record._export as Record<string, unknown>
      continue
    }
    if (record.success === false) {
      // Never surface raw backend/upstream error details from an export stream.
      throw new Error(i18next.t('导出日志失败'))
    }
    if (!isAdmin && record.channel_name) {
      throw new Error(i18next.t('日志导出响应无效'))
    }
    if (!Number.isSafeInteger(record.id)) {
      throw new Error(i18next.t('日志导出响应无效'))
    }
    count++
  }
  if (
    !summary ||
    summary.version !== 1 ||
    !Number.isSafeInteger(summary.count) ||
    summary.count !== count ||
    typeof summary.max_rows !== 'number' ||
    !Number.isSafeInteger(summary.max_rows) ||
    summary.max_rows <= 0 ||
    count > summary.max_rows ||
    (summary.status !== 'complete' && summary.status !== 'limit_reached') ||
    (summary.status === 'complete' && count === summary.max_rows) ||
    (summary.status === 'limit_reached' && count !== summary.max_rows)
  ) {
    throw new Error(i18next.t('日志导出不完整，请重试或缩小筛选范围。'))
  }
  return {
    count,
    complete: summary.status === 'complete',
    limited: summary.status === 'limit_reached',
  }
}

export async function downloadLogExport(
  params: GetLogsParams,
  isAdmin: boolean,
  signal: AbortSignal,
  isCurrent: () => boolean
) {
  const checkAccount = () => {
    signal.throwIfAborted()
    if (!isCurrent()) throw new DOMException('Account changed', 'AbortError')
  }
  checkAccount()
  const policy = await loadLogPolicy(signal)
  checkAccount()
  if (!policy.export_enabled || (!isAdmin && !policy.user_export_enabled)) {
    throw new LogPolicyError(i18next.t('管理员已关闭普通用户日志导出。'))
  }
  const filters = { ...params, ...normalizeLogExportWindow(params.start_timestamp, params.end_timestamp, policy) }
  delete filters.p
  delete filters.page_size
  if (!isAdmin) {
    delete filters.username
    delete filters.channel
  }
  const response = await api.get<string>(
    isAdmin ? '/api/log/export' : '/api/log/self/export',
    {
      params: { ...filters, include_summary: true },
      responseType: 'text',
      transformResponse: [(value: string) => value],
      signal,
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  checkAccount()
  if (!(response.status >= 200 && response.status < 300)) {
    throw new Error(i18next.t('导出日志失败'))
  }
  const mediaType = String(response.headers['content-type'] || '')
    .split(';', 1)[0]
    .trim()
    .toLowerCase()
  if (mediaType !== 'application/x-ndjson') {
    throw new Error(i18next.t('日志导出响应无效'))
  }
  let result: ReturnType<typeof validateLogExport>
  try {
    result = validateLogExport(response.data, isAdmin)
  } catch {
    throw new LogPolicyError(i18next.t('日志导出不完整，请重试或缩小筛选范围。'))
  }
  checkAccount()
  // Preserve the summary in the downloaded file for independent verification.
  // It is never counted as a log, and no browser-inaccessible trailer is used.
  const blob = new Blob([response.data], {
    type: 'application/x-ndjson;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'logs.ndjson'
  link.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  return result
}
