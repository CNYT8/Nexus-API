import React, { useEffect, useId, useRef, useState } from 'react';
import { Button, Input, Space, Switch, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../helpers';
import { readAccountId, useAccountSession } from '../../hooks/playground/accountSession';
import { LOG_POLICY_KEYS, MAX_LOG_RANGE_DAYS, parseLogPolicyOptions } from '../../hooks/usage-logs/logPolicy';

const requestOptions = { disableDuplicate: true, skipErrorHandler: true };
async function readOptions(signal) {
  const { data } = await API.get('/api/option/', { ...requestOptions, signal, headers: { 'New-Api-User': readAccountId() || '' } });
  signal.throwIfAborted();
  if (!data?.success) throw new Error('Log policy options unavailable');
  return parseLogPolicyOptions(data.data);
}

export default function LogPolicySettings() {
  const { t } = useTranslation();
  const id = useId();
  const account = useAccountSession();
  const active = useRef(null);
  const [attempt, setAttempt] = useState(0);
  const [saving, setSaving] = useState(false);
  const [draft, setDraft] = useState({ revision: -1, days: '', enabled: false });

  useEffect(() => {
    const controller = new AbortController();
    active.current = controller;
    const stop = account.onInvalidate(() => controller.abort());
    setSaving(false);
    setDraft({ revision: account.revision, days: '', enabled: false });
    if (account.isCurrent()) {
      void readOptions(controller.signal).then((original) => {
        if (!controller.signal.aborted && account.isCurrent()) {
          setDraft({ revision: account.revision, original, days: String(original.days), enabled: original.enabled });
        }
      }).catch(() => {
        if (!controller.signal.aborted && account.isCurrent()) {
          setDraft({ revision: account.revision, days: '', enabled: false, error: t('读取日志策略失败，请重试。') });
        }
      }).finally(() => { if (active.current === controller) active.current = null; });
    }
    return () => { stop(); controller.abort(); active.current?.abort(); active.current = null; };
  }, [account, attempt, t]);

  const view = draft.revision === account.revision && account.isCurrent() ? draft : undefined;
  const loading = account.userId !== null && !view?.original && !view?.error;
  const days = Number(view?.days);
  const valid = /^\d+$/.test(view?.days || '') && Number.isSafeInteger(days) && days >= 1 && days <= MAX_LOG_RANGE_DAYS;
  const dirty = !!view?.original && (days !== view.original.days || view.enabled !== view.original.enabled);
  const refresh = () => {
    if (saving) return;
    active.current?.abort();
    setDraft({ revision: account.revision, days: '', enabled: false });
    setAttempt((value) => value + 1);
  };
  const save = async () => {
    if (active.current || saving || !view?.original || !valid || !dirty || !account.isCurrent()) return;
    const controller = new AbortController();
    active.current = controller;
    const stop = account.onInvalidate(() => controller.abort());
    const current = () => !controller.signal.aborted && active.current === controller && account.isCurrent();
    const requested = { days, enabled: view.enabled };
    const failure = t('保存日志策略失败，部分设置可能已生效；请刷新确认后重试。');
    setSaving(true);
    setDraft((previous) => ({ ...previous, error: undefined }));
    try {
      const changes = [
        ...(requested.days !== view.original.days ? [{ key: LOG_POLICY_KEYS.days, value: String(requested.days) }] : []),
        ...(requested.enabled !== view.original.enabled ? [{ key: LOG_POLICY_KEYS.enabled, value: String(requested.enabled) }] : []),
      ];
      for (const change of changes) {
        if (!current()) return;
        const { data } = await API.put('/api/option/', change, { ...requestOptions, signal: controller.signal, headers: { 'New-Api-User': account.userId || '' } });
        if (!current()) return;
        if (!data?.success) throw new Error('Log policy save rejected');
      }
      const original = await readOptions(controller.signal);
      if (!current()) return;
      const confirmed = original.days === requested.days && original.enabled === requested.enabled;
      setDraft({ revision: account.revision, original, days: String(requested.days), enabled: requested.enabled, error: confirmed ? undefined : failure });
      if (confirmed) showSuccess(t('日志策略已保存。'));
      else showError(failure);
    } catch {
      if (!current()) return;
      let original;
      try { original = await readOptions(controller.signal); } catch { /* Require a refresh if readback fails. */ }
      if (!current()) return;
      setDraft({ revision: account.revision, original, days: String(requested.days), enabled: requested.enabled, error: failure });
      showError(failure);
    } finally {
      stop();
      if (active.current === controller) { active.current = null; if (account.isCurrent()) setSaving(false); }
    }
  };

  return (
    <section className='mt-4 space-y-4 rounded-lg border p-4' aria-labelledby={`${id}-title`} aria-busy={loading || saving}>
      <div>
        <Typography.Title id={`${id}-title`} heading={6}>{t('日志查询与导出策略')}</Typography.Title>
        <Typography.Text type='tertiary'>{t('查询跨度默认 365 天；导出未指定时间时默认最近 31 天，并受查询跨度限制。')}</Typography.Text>
      </div>
      {loading && <p role='status'>{t('正在读取日志策略…')}</p>}
      {view?.error && <p style={{ color: 'var(--semi-color-danger)' }} role='alert'>{view.error}</p>}
      <div className='space-y-2'>
        <label htmlFor={`${id}-days`}>{t('日志查询最大跨度（天）')}</label>
        <div className='w-full sm:max-w-xs'>
          <Input id={`${id}-days`} type='text' inputMode='numeric' value={view?.days || ''} disabled={loading || saving || !view?.original}
            aria-invalid={!!view?.original && !valid} aria-describedby={`${id}-help`}
            onChange={(value) => setDraft((previous) => ({ ...previous, days: value }))} />
        </div>
        <p id={`${id}-help`} style={{ color: view?.original && !valid ? 'var(--semi-color-danger)' : 'var(--semi-color-text-2)' }}>
          {t('请输入 1 至 {{max}} 之间的整数天数。', { max: MAX_LOG_RANGE_DAYS })}
        </p>
      </div>
      <div className='flex items-center justify-between gap-4'>
        <div className='min-w-0 space-y-1'>
          <label htmlFor={`${id}-enabled`}>{t('允许普通用户导出日志')}</label>
          <p style={{ color: 'var(--semi-color-text-2)' }}>{t('关闭后普通用户不能导出；管理员仍可导出，但同样受时间跨度和每账户单任务限制。')}</p>
        </div>
        <Switch id={`${id}-enabled`} aria-label={t('允许普通用户导出日志')} checked={view?.enabled || false}
          disabled={loading || saving || !view?.original}
          onChange={(enabled) => setDraft((previous) => ({ ...previous, enabled }))} />
      </div>
      <Space wrap>
        <Button theme='solid' loading={saving} disabled={loading || saving || !valid || !dirty || !view?.original} onClick={save}>
          {saving ? t('正在保存日志策略…') : t('保存日志策略')}
        </Button>
        <Button disabled={loading || saving || !account.isCurrent()} onClick={refresh}>{t('刷新日志策略')}</Button>
      </Space>
    </section>
  );
}
