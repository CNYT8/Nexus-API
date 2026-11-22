import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ApiRequestConfig } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/confirm-dialog'
import type { AccountSession } from '@/features/playground/hooks/use-account-session'
import type { ScriptPlugin } from './script-plugins-api'
import { changeScriptRevision, listActivations, parseDryRun, runScriptRevision, scriptHooks, type DryRunInput } from './script-plugin-management-api'
import { useScriptPluginRequest } from './use-script-plugin-request'

type Action = { kind: 'activate'; expected: number } | { kind: 'delete' } | { kind: 'dryrun'; payload: DryRunInput }
export function ScriptPluginLifecycle({ plugin, session, busy, error, onChange }: {
  plugin: ScriptPlugin
  session: AccountSession
  busy: boolean
  error: string
  onChange: (request: (config: ApiRequestConfig) => Promise<void>, done: () => void) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [action, setAction] = useState<Action | null>(null)
  const [hook, setHook] = useState('buildSubmitRequest')
  const [args, setArgs] = useState('[{}]')
  const [fixtures, setFixtures] = useState('[]')
  const [activeId, setActiveId] = useState<number | null>(null)
  const { run: readActive, busy: activeBusy, error: activeError } = useScriptPluginRequest(session)
  const loadActive = useCallback(() => {
    setActiveId(null)
    void readActive((config) => listActivations(config, t), (rows) => {
      setActiveId(rows.find((row) => row.plugin_key === plugin.key)?.plugin_id ?? 0)
    }, t('插件激活状态读取失败'))
  }, [plugin.key, readActive, t])
  useEffect(() => { loadActive() }, [loadActive])
  const [output, setOutput] = useState('')
  const [validation, setValidation] = useState('')
  const request = useScriptPluginRequest(session, false)
  const locked = busy || request.busy
  const resetOutput = () => { setOutput(''); setValidation(''); request.clearError() }
  const title = action?.kind === 'delete' ? t('删除插件版本') : action?.kind === 'activate' ? t('激活插件版本') : t('试运行插件')
  return (
    <div className='space-y-3 border-t pt-3'>
      <div className='flex flex-wrap gap-2'>
        <Button variant='outline' disabled={locked || activeBusy || activeId === null || activeId === plugin.id || !plugin.enabled} onClick={() => { if (activeId !== null) setAction({ kind: 'activate', expected: activeId }) }}>{t('激活插件版本')}</Button>
        <Button variant='destructive' disabled={locked || activeBusy || activeId === null || activeId === plugin.id || plugin.enabled} onClick={() => setAction({ kind: 'delete' })}>{t('删除插件版本')}</Button>
        <Button variant='outline' disabled={locked || activeBusy} onClick={loadActive}>{t('刷新激活状态')}</Button>
      </div>
      <p className='text-muted-foreground text-sm'>{activeBusy ? t('正在读取激活状态') : activeId === null ? t('激活状态未知') : activeId === 0 ? t('此插件尚无激活版本') : `${t('当前激活版本')} #${activeId}`}</p>
      {activeError && <p role='alert' className='text-destructive text-sm'>{activeError}</p>}
      <p className='text-muted-foreground text-sm'>{t('激活前须先启用版本；删除前须先禁用，并解除渠道绑定和激活引用。历史任务仍可使用原修订。')}</p>
      <details>
        <summary className='cursor-pointer text-sm font-medium'>{t('试运行插件')}</summary>
        <div className='mt-3 space-y-3'>
          <p className='text-muted-foreground text-sm'>{t('试运行仅使用模拟网络响应，不会访问外部服务、创建任务或计费。请使用脱敏参数，不要填写真实密钥。')}</p>
          <Label htmlFor={`${id}-hook`}>{t('插件函数')}</Label>
          <Input id={`${id}-hook`} list={`${id}-hooks`} value={hook} disabled={locked} autoComplete='off' onChange={(event) => { setHook(event.target.value); resetOutput() }} />
          <datalist id={`${id}-hooks`}>{scriptHooks.map((value) => <option key={value} value={value} />)}</datalist>
          <Label htmlFor={`${id}-args`}>{t('试运行参数（JSON 数组）')}</Label>
          <Textarea id={`${id}-args`} value={args} rows={7} spellCheck={false} autoComplete='off' className='font-mono text-xs' disabled={locked} onChange={(event) => { setArgs(event.target.value); resetOutput() }} />
          <Label htmlFor={`${id}-fixtures`}>{t('模拟网络响应（JSON 数组）')}</Label>
          <Textarea id={`${id}-fixtures`} value={fixtures} rows={7} spellCheck={false} autoComplete='off' className='font-mono text-xs' disabled={locked} onChange={(event) => { setFixtures(event.target.value); resetOutput() }} />
          <p className='text-muted-foreground text-xs'>{t('每项填写 url、method、status、headers 和 body；按调用顺序匹配，缺少或多余的模拟响应都会失败。')}</p>
          <Button disabled={locked} onClick={() => {
            resetOutput()
            try { setAction({ kind: 'dryrun', payload: parseDryRun(hook, args, fixtures, t) }) }
            catch (cause) { setValidation(cause instanceof Error ? cause.message : t('试运行参数无效')) }
          }}>{t('试运行插件')}</Button>
          {validation && <p role='alert' className='text-destructive text-sm'>{validation}</p>}
          {output && <div role='status'><p>{t('试运行结果')}</p><pre className='max-h-80 overflow-auto text-xs break-all whitespace-pre-wrap'>{output}</pre></div>}
        </div>
      </details>
      <ConfirmDialog
        open={action !== null}
        onOpenChange={(open) => { if (!open && !locked) setAction(null) }}
        title={title}
        confirmText={t('确认')}
        destructive={action?.kind === 'delete'}
        isLoading={locked}
        desc={<div className='space-y-2 break-all'>
          <p>#{plugin.id} · {plugin.key} · {plugin.version}</p><p>SHA-256: {plugin.digest}</p>
          <p>{action?.kind === 'delete'
            ? t('删除后不能再用于新绑定；版本须已禁用且没有绑定或激活引用。历史任务需要的源码仍会保留。')
            : action?.kind === 'activate'
              ? t('激活将切换此插件的当前版本。已有绑定和历史任务仍固定到原修订，如需切换绑定请另行修改。')
              : t('试运行仅使用模拟网络响应，不会访问外部服务、创建任务或计费。请使用脱敏参数，不要填写真实密钥。')}</p>
        </div>}
        handleConfirm={() => {
          if (!action || locked || !session.isCurrent()) return
          if (action.kind === 'dryrun') {
            void request.run(
              (config) => runScriptRevision(plugin.id, action.payload, config),
              (result) => { setOutput(JSON.stringify(result ?? null, null, 2)); setAction(null) },
              t('插件操作失败，请检查后重试')
            )
          } else {
            const kind = action.kind
            const expected = action.kind === 'activate' ? action.expected : 0
            onChange((config) => changeScriptRevision(plugin.id, kind, expected, config), () => setAction(null))
          }
        }}
      >
        {action?.kind === 'dryrun' && <pre className='max-h-48 overflow-auto text-xs break-all whitespace-pre-wrap'>{JSON.stringify(action.payload, null, 2)}</pre>}
        {(action?.kind === 'dryrun' ? request.error : error) && <p role='alert' className='text-destructive text-sm'>{action?.kind === 'dryrun' ? request.error : error}</p>}
      </ConfirmDialog>
    </div>
  )
}
