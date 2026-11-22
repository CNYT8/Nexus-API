import React from 'react';
import { useTranslation } from 'react-i18next';

// Use server-wide counts, never the length of the bounded device list.
export default function SessionLimitsNotice({ data }) {
  const { t } = useTranslation();
  if (!data) return null;
  const values = [data.active_count, data.active_limit, data.issuance_count, data.issuance_limit, data.window_seconds];
  // An older server can still supply a valid device list without limit fields.
  if (values.every((value) => value === undefined)) return null;
  const { active_count, active_limit, issuance_count, issuance_limit, window_seconds } = data;
  if (values.some((value) => typeof value !== 'number' || !Number.isSafeInteger(value)) ||
    active_count < 0 || issuance_count < 0 || active_limit <= 0 || issuance_limit <= 0 || window_seconds <= 0) {
    return <p role='status' className='text-xs text-gray-500'>{t('会话限额信息暂不可用，请刷新重试')}</p>;
  }
  return (
    <div className='space-y-2 rounded-lg border p-3 text-sm' aria-live='polite'>
      <p>{t('活跃登录会话 {{count}} / {{limit}}', { count: active_count, limit: active_limit })}</p>
      <p>{t('最近 {{seconds}} 秒内创建的登录会话 {{count}} / {{limit}}', { seconds: window_seconds, count: issuance_count, limit: issuance_limit })}</p>
      <p className='text-xs text-gray-500'>{t('撤销登录不会重置当前窗口内的累计次数；限额以服务器实时校验为准')}</p>
      {active_count >= active_limit && <p role='alert' className='text-red-600'>{t('活跃会话已达上限，请先退出其他设备')}</p>}
      {issuance_count >= issuance_limit && <p role='alert' className='text-red-600'>{t('当前窗口内登录次数已达上限，请稍后重试')}</p>}
    </div>
  );
}
