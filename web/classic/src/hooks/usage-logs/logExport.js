/*
Copyright (C) 2025 QuantumNous

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
import i18next from 'i18next';
import { API } from '../../helpers';
import { readAccountId } from '../playground/accountSession';
import { loadLogPolicy, LogPolicyError, normalizeLogExportWindow } from './logPolicy';

const exportTimestamp = (value) => value === undefined || value === null || value === ''
  ? undefined
  : Math.floor(Date.parse(value) / 1000);

export function buildLogExportParams(values, isAdmin) {
  return {
    include_summary: true,
    type: values.logType,
    start_timestamp: exportTimestamp(values.start_timestamp),
    end_timestamp: exportTimestamp(values.end_timestamp),
    model_name: values.model_name,
    token_name: values.token_name,
    group: values.group,
    request_id: values.request_id,
    ...(values.upstream_request_id
      ? { upstream_request_id: values.upstream_request_id }
      : {}),
    ...(isAdmin ? { username: values.username, channel: values.channel } : {}),
  };
}

export function validateLogExport(text, isAdmin = true) {
  if (typeof text !== 'string') {
    throw new Error(i18next.t('日志导出响应无效'));
  }
  let count = 0;
  let summary = null;
  for (const line of text.replace(/^\uFEFF/, '').split(/\r?\n/)) {
    if (!line.trim()) continue;
    let row;
    try {
      row = JSON.parse(line);
    } catch {
      throw new Error(i18next.t('日志导出响应无效'));
    }
    if (!row || typeof row !== 'object' || Array.isArray(row)) {
      throw new Error(i18next.t('日志导出响应无效'));
    }
    // The summary must be a standalone, final non-empty NDJSON record.
    if (summary) {
      throw new Error(i18next.t('日志导出不完整，请重试或缩小筛选范围。'));
    }
    if (Object.prototype.hasOwnProperty.call(row, '_export')) {
      if (
        Object.keys(row).length !== 1 ||
        !row._export ||
        typeof row._export !== 'object' ||
        Array.isArray(row._export)
      ) {
        throw new Error(i18next.t('日志导出不完整，请重试或缩小筛选范围。'));
      }
      summary = row._export;
      continue;
    }
    if (row.success === false) {
      throw new Error(i18next.t('导出日志失败'));
    }
    if (!isAdmin && row.channel_name) {
      throw new Error(i18next.t('日志导出响应无效'));
    }
    if (!Number.isSafeInteger(row.id)) {
      throw new Error(i18next.t('日志导出响应无效'));
    }
    count++;
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
    throw new Error(i18next.t('日志导出不完整，请重试或缩小筛选范围。'));
  }
  return {
    count,
    complete: summary.status === 'complete',
    limited: summary.status === 'limit_reached',
  };
}

export async function downloadLogExport(values, isAdmin, signal, isCurrent) {
  const checkAccount = () => {
    signal.throwIfAborted();
    if (!isCurrent()) throw new DOMException('Account changed', 'AbortError');
  };
  checkAccount();
  const policy = await loadLogPolicy(signal);
  checkAccount();
  if (!policy.export_enabled || (!isAdmin && !policy.user_export_enabled)) {
    throw new LogPolicyError(i18next.t('管理员已关闭普通用户日志导出。'));
  }
  const params = buildLogExportParams(values, isAdmin);
  Object.assign(params, normalizeLogExportWindow(params.start_timestamp, params.end_timestamp, policy));
  const response = await API.get(
    isAdmin ? '/api/log/export' : '/api/log/self/export',
    {
      params,
      headers: { 'New-Api-User': readAccountId() || '' },
      responseType: 'text',
      transformResponse: [(value) => value],
      signal,
      disableDuplicate: true,
      skipErrorHandler: true,
    },
  );
  checkAccount();
  if (!(response.status >= 200 && response.status < 300)) {
    throw new Error(i18next.t('导出日志失败'));
  }
  const mediaType = String(response.headers['content-type'] || '')
    .split(';', 1)[0]
    .trim()
    .toLowerCase();
  if (mediaType !== 'application/x-ndjson') {
    throw new Error(i18next.t('日志导出响应无效'));
  }
  let result;
  try {
    result = validateLogExport(response.data, isAdmin);
  } catch {
    throw new LogPolicyError(i18next.t('日志导出不完整，请重试或缩小筛选范围。'));
  }
  checkAccount();
  // Preserve the summary in the downloaded file for independent verification.
  // It is never counted as a log, and no browser-inaccessible trailer is used.
  const blob = new Blob([response.data], {
    type: 'application/x-ndjson;charset=utf-8',
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = 'logs.ndjson';
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  return result;
}
