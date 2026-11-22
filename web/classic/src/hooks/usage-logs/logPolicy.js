import { useEffect, useRef, useState } from 'react';
import i18next from 'i18next';
import { API } from '../../helpers';
import { readAccountId, useAccountSession } from '../playground/accountSession';

// floor(MaxInt64 / 86400), matching operation_setting.MaxLogRangeDays.
export const MAX_LOG_RANGE_DAYS = 106751991167300;
export const LOG_POLICY_KEYS = {
  days: 'log_setting.max_range_days',
  enabled: 'log_setting.user_export_enabled',
};

// Only locally generated messages may be shown; never echo raw response bodies.
export class LogPolicyError extends Error {}

export function logExportErrorMessage(error) {
  if (error instanceof LogPolicyError) return error.message;
  const status = error?.response?.status;
  if (status === 401) return i18next.t('登录状态已失效，请重新登录后重试。');
  if (status === 403) return i18next.t('当前账户无日志导出权限，请刷新日志策略后重试。');
  if (status === 429) return i18next.t('当前账户已有日志导出任务，请稍后重试。');
  if (status === 400) return i18next.t('日志导出筛选无效，请检查时间范围后重试。');
  return i18next.t('导出日志失败');
}

export function parseLogPolicyOptions(options) {
  if (!Array.isArray(options)) throw new LogPolicyError(i18next.t('读取日志策略失败，请重试。'));
  const days = options.find((item) => item?.key === LOG_POLICY_KEYS.days)?.value;
  const enabled = options.find((item) => item?.key === LOG_POLICY_KEYS.enabled)?.value;
  const parsedDays = Number(days);
  if (
    !Number.isSafeInteger(parsedDays) || parsedDays < 1 || parsedDays > MAX_LOG_RANGE_DAYS ||
    (enabled !== 'true' && enabled !== 'false')
  ) throw new LogPolicyError(i18next.t('读取日志策略失败，请重试。'));
  return { days: parsedDays, enabled: enabled === 'true' };
}

export async function loadLogPolicy(signal) {
  const { data } = await API.get('/api/log/settings', {
    signal, disableDuplicate: true, skipErrorHandler: true,
    headers: { 'New-Api-User': readAccountId() || '' },
  });
  signal.throwIfAborted();
  const policy = data?.data;
  if (!data?.success || !policy ||
    !Number.isSafeInteger(policy.max_range_days) || policy.max_range_days < 1 || policy.max_range_days > MAX_LOG_RANGE_DAYS ||
    !Number.isSafeInteger(policy.default_range_days) || policy.default_range_days < 1 || policy.default_range_days > Math.min(policy.max_range_days, 31) ||
    !Number.isSafeInteger(policy.export_max_rows) || policy.export_max_rows < 1 ||
    typeof policy.user_export_enabled !== 'boolean' || typeof policy.export_enabled !== 'boolean'
  ) throw new LogPolicyError(i18next.t('读取日志策略失败，请重试。'));
  return policy;
}

export function normalizeLogExportWindow(start, end, policy) {
  if ([start, end].some((value) => value !== undefined && (!Number.isSafeInteger(value) || value < 0))) {
    throw new LogPolicyError(i18next.t('请选择有效的日志起止时间。'));
  }
  const endTimestamp = end || Math.floor(Date.now() / 1000);
  const startTimestamp = start || Math.max(0, endTimestamp - policy.default_range_days * 86400);
  if (startTimestamp > endTimestamp || (endTimestamp - startTimestamp) / 86400 > policy.max_range_days) {
    throw new LogPolicyError(i18next.t('日志查询跨度不能超过 {{days}} 天，且开始时间不能晚于结束时间。', { days: policy.max_range_days }));
  }
  return { start_timestamp: startTimestamp, end_timestamp: endTimestamp };
}

export function useLogPolicy() {
  const account = useAccountSession();
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState({ revision: -1 });
  const active = useRef(null);
  useEffect(() => {
    const controller = new AbortController();
    active.current = controller;
    const stop = account.onInvalidate(() => controller.abort());
    setState({ revision: account.revision });
    if (account.isCurrent()) {
      void loadLogPolicy(controller.signal).then((policy) => {
        if (!controller.signal.aborted && account.isCurrent()) setState({ revision: account.revision, policy });
      }).catch(() => {
        if (!controller.signal.aborted && account.isCurrent()) setState({ revision: account.revision, error: true });
      });
    }
    return () => { stop(); controller.abort(); if (active.current === controller) active.current = null; };
  }, [account, attempt]);
  const current = state.revision === account.revision && account.isCurrent() ? state : undefined;
  return {
    account, policy: current?.policy, error: current?.error === true,
    loading: account.userId !== null && !current?.policy && !current?.error,
    refresh: () => { active.current?.abort(); setState({ revision: account.revision }); setAttempt((value) => value + 1); },
  };
}
