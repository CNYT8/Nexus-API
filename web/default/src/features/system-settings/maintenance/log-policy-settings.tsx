import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { useAccountSession } from '@/features/playground/hooks/use-account-session'
import { LOG_POLICY_KEYS, MAX_LOG_RANGE_DAYS, parseLogPolicyOptions } from '@/features/usage-logs/lib/log-policy'
import { SettingsControlGroup, SettingsSwitchContent, SettingsSwitchRow } from '../components/settings-form-layout'

type PolicyOptions = ReturnType<typeof parseLogPolicyOptions>
type Draft = { revision: number; original?: PolicyOptions; days: string; enabled: boolean; error?: string }
const requestOptions = { disableDuplicate: true, skipBusinessError: true, skipErrorHandler: true }

async function readOptions(signal: AbortSignal) {
  const { data } = await api.get<{ success: boolean; data: unknown }>('/api/option/', { ...requestOptions, signal })
  signal.throwIfAborted()
  if (!data?.success) throw new Error('Log policy options unavailable')
  return parseLogPolicyOptions(data.data)
}

export function LogPolicySettings() {
  const { t } = useTranslation()
  const id = useId()
  const account = useAccountSession()
  const active = useRef<AbortController | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
  const [draft, setDraft] = useState<Draft>({ revision: -1, days: '', enabled: false })

  useEffect(() => {
    const controller = new AbortController()
    active.current = controller
    const stop = account.onInvalidate(() => controller.abort())
    setSaving(false)
    setDraft({ revision: account.revision, days: '', enabled: false })
    if (account.isCurrent()) {
      void readOptions(controller.signal).then((original) => {
        if (!controller.signal.aborted && account.isCurrent()) {
          setDraft({ revision: account.revision, original, days: String(original.days), enabled: original.enabled })
        }
      }).catch(() => {
        if (!controller.signal.aborted && account.isCurrent()) {
          setDraft({ revision: account.revision, days: '', enabled: false, error: t('读取日志策略失败，请重试。') })
        }
      }).finally(() => { if (active.current === controller) active.current = null })
    }
    return () => { stop(); controller.abort(); active.current?.abort(); active.current = null }
  }, [account, attempt, t])

  const view = draft.revision === account.revision && account.isCurrent() ? draft : undefined
  const loading = account.userId !== null && !view?.original && !view?.error
  const days = Number(view?.days)
  const valid = /^\d+$/.test(view?.days || '') && Number.isSafeInteger(days) && days >= 1 && days <= MAX_LOG_RANGE_DAYS
  const dirty = !!view?.original && (days !== view.original.days || view.enabled !== view.original.enabled)
  const refresh = () => {
    if (saving) return
    active.current?.abort()
    setDraft({ revision: account.revision, days: '', enabled: false })
    setAttempt((value) => value + 1)
  }
  const save = async () => {
    if (active.current || saving || !view?.original || !valid || !dirty || !account.isCurrent()) return
    const controller = new AbortController()
    active.current = controller
    const stop = account.onInvalidate(() => controller.abort())
    const current = () => !controller.signal.aborted && active.current === controller && account.isCurrent()
    const requested = { days, enabled: view.enabled }
    const failure = t('保存日志策略失败，部分设置可能已生效；请刷新确认后重试。')
    setSaving(true)
    setDraft((previous) => ({ ...previous, error: undefined }))
    try {
      const changes = [
        ...(requested.days !== view.original.days ? [{ key: LOG_POLICY_KEYS.days, value: String(requested.days) }] : []),
        ...(requested.enabled !== view.original.enabled ? [{ key: LOG_POLICY_KEYS.enabled, value: String(requested.enabled) }] : []),
      ]
      for (const change of changes) {
        if (!current()) return
        const { data } = await api.put<{ success: boolean }>('/api/option/', change, { ...requestOptions, signal: controller.signal })
        if (!current()) return
        if (!data?.success) throw new Error('Log policy save rejected')
      }
      const original = await readOptions(controller.signal)
      if (!current()) return
      const confirmed = original.days === requested.days && original.enabled === requested.enabled
      setDraft({ revision: account.revision, original, days: String(requested.days), enabled: requested.enabled, error: confirmed ? undefined : failure })
      if (confirmed) toast.success(t('日志策略已保存。'))
      else toast.error(failure)
    } catch {
      if (!current()) return
      let original: PolicyOptions | undefined
      try { original = await readOptions(controller.signal) } catch { /* Require a refresh if readback fails. */ }
      if (!current()) return
      setDraft({ revision: account.revision, original, days: String(requested.days), enabled: requested.enabled, error: failure })
      toast.error(failure)
    } finally {
      stop()
      if (active.current === controller) { active.current = null; if (account.isCurrent()) setSaving(false) }
    }
  }

  return (
    <SettingsControlGroup className='mt-6 space-y-4' aria-busy={loading || saving}>
      <div>
        <h4 className='text-sm font-medium'>{t('日志查询与导出策略')}</h4>
        <p className='text-muted-foreground text-sm'>{t('查询跨度默认 365 天；导出未指定时间时默认最近 31 天，并受查询跨度限制。')}</p>
      </div>
      {loading && <p className='text-muted-foreground text-sm' role='status'>{t('正在读取日志策略…')}</p>}
      {view?.error && <p className='text-destructive text-sm' role='alert'>{view.error}</p>}
      <div className='space-y-2'>
        <Label htmlFor={`${id}-days`}>{t('日志查询最大跨度（天）')}</Label>
        <Input id={`${id}-days`} type='number' min={1} max={MAX_LOG_RANGE_DAYS} step={1} inputMode='numeric'
          className='w-full sm:max-w-xs' value={view?.days || ''} disabled={loading || saving || !view?.original}
          aria-invalid={!!view?.original && !valid} aria-describedby={`${id}-help`}
          onChange={(event) => setDraft((previous) => ({ ...previous, days: event.target.value }))} />
        <p id={`${id}-help`} className={view?.original && !valid ? 'text-destructive text-sm' : 'text-muted-foreground text-sm'}>
          {t('请输入 1 至 {{max}} 之间的整数天数。', { max: MAX_LOG_RANGE_DAYS })}
        </p>
      </div>
      <SettingsSwitchRow>
        <SettingsSwitchContent>
          <Label htmlFor={`${id}-enabled`}>{t('允许普通用户导出日志')}</Label>
          <p className='text-muted-foreground text-sm'>{t('关闭后普通用户不能导出；管理员仍可导出，但同样受时间跨度和每账户单任务限制。')}</p>
        </SettingsSwitchContent>
        <Switch id={`${id}-enabled`} checked={view?.enabled || false} disabled={loading || saving || !view?.original}
          onCheckedChange={(enabled) => setDraft((previous) => ({ ...previous, enabled }))} />
      </SettingsSwitchRow>
      <div className='flex flex-wrap gap-2'>
        <Button type='button' disabled={loading || saving || !valid || !dirty || !view?.original} onClick={() => void save()}>
          {saving ? t('正在保存日志策略…') : t('保存日志策略')}
        </Button>
        <Button type='button' variant='outline' disabled={loading || saving || !account.isCurrent()} onClick={refresh}>{t('刷新日志策略')}</Button>
      </div>
    </SettingsControlGroup>
  )
}
