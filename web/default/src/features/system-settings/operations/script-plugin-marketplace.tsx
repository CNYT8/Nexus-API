import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/confirm-dialog'
import type { AccountSession } from '@/features/playground/hooks/use-account-session'
import { useScriptPluginRequest } from './use-script-plugin-request'
import {
  listMarketSources, loadMarketIndex, installMarketRevision, saveMarketSources, validateSources,
  type MarketIndex, type MarketplaceSource, type MarketEntry,
} from './script-plugin-management-api'

type InstallDraft = { source: MarketplaceSource; entry: MarketEntry }
export function ScriptPluginMarketplace({ session, disabled, onInstalled, onBusyChange }: {
  session: AccountSession; disabled: boolean; onInstalled: (id: number) => void; onBusyChange: (busy: boolean) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const dirty = useRef(false)
  const [sources, setSources] = useState<MarketplaceSource[]>([])
  const [draft, setDraft] = useState<MarketplaceSource[]>([])
  const [loaded, setLoaded] = useState(false)
  const [selected, setSelected] = useState('')
  const [index, setIndex] = useState<MarketIndex | null>(null)
  const [page, setPage] = useState(0)
  const [pendingSave, setPendingSave] = useState<MarketplaceSource[] | null>(null)
  const [pendingInstall, setPendingInstall] = useState<InstallDraft | null>(null)
  const [validation, setValidation] = useState('')
  const [notice, setNotice] = useState('')
  const { run: readSources, busy: loading, error: loadError, cancel: cancelSources } = useScriptPluginRequest(session)
  const catalog = useScriptPluginRequest(session)
  const save = useScriptPluginRequest(session, false)
  useEffect(() => { onBusyChange(save.busy); return () => onBusyChange(false) }, [save.busy, onBusyChange])
  const locked = disabled || save.busy
  const loadSources = useCallback(() => {
    void readSources((config) => listMarketSources(config, t), (rows) => {
      setSources(rows)
      if (!dirty.current) setDraft(rows)
      setLoaded(true)
      setSelected((current) => rows.some((row) => row.id === current && row.enabled) ? current : rows.find((row) => row.enabled)?.id ?? '')
    }, t('市场源读取失败'))
  }, [readSources, t])
  useEffect(() => { loadSources() }, [loadSources])
  const clearCatalog = () => {
    catalog.cancel(); setIndex(null); setPage(0); setPendingInstall(null)
  }
  const updateDraft = (value: MarketplaceSource[]) => { dirty.current = true; setDraft(value); setValidation(''); setNotice('') }
  const optionStyle = 'border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
  return (
    <section className='min-w-0 space-y-3 rounded-md border p-3' aria-labelledby={`${id}-title`}>
      <h5 id={`${id}-title`} className='font-semibold'>{t('插件市场')}</h5>
      <p className='text-muted-foreground text-sm'>{t('从可信市场选择固定版本和摘要，安装后先审阅能力与源码，再决定是否启用。新安装的修订默认禁用。')}</p>
      <details>
        <summary className='cursor-pointer text-sm font-medium'>{t('管理市场源')}</summary>
        <div className='mt-3 space-y-3'>
          {draft.map((row, position) => <div key={position} className='grid items-end gap-2 sm:grid-cols-2'>
            <div><Label htmlFor={`${id}-key-${position}`}>{t('市场源标识')}</Label><Input id={`${id}-key-${position}`} value={row.id} disabled={locked || loading} autoComplete='off' onChange={(event) => updateDraft(draft.map((item, i) => i === position ? { ...item, id: event.target.value } : item))} /></div>
            <div><Label htmlFor={`${id}-name-${position}`}>{t('市场源名称')}</Label><Input id={`${id}-name-${position}`} value={row.name} disabled={locked || loading} onChange={(event) => updateDraft(draft.map((item, i) => i === position ? { ...item, name: event.target.value } : item))} /></div>
            <div><Label htmlFor={`${id}-url-${position}`}>{t('市场索引地址')}</Label><Input id={`${id}-url-${position}`} value={row.url} disabled={locked || loading} autoComplete='off' onChange={(event) => updateDraft(draft.map((item, i) => i === position ? { ...item, url: event.target.value } : item))} /></div>
            <Label className='flex items-center gap-2'><input type='checkbox' checked={row.enabled} disabled={locked || loading} onChange={(event) => updateDraft(draft.map((item, i) => i === position ? { ...item, enabled: event.target.checked } : item))} />{t('启用市场源')}</Label>
            <Button variant='outline' disabled={locked || loading} aria-label={`${t('移除市场源')} ${row.name || position + 1}`} onClick={() => updateDraft(draft.filter((_, i) => i !== position))}>{t('移除市场源')}</Button>
          </div>)}
          <div className='flex flex-wrap gap-2'>
            <Button variant='outline' disabled={locked || loading || draft.length >= 32} onClick={() => updateDraft([...draft, { id: '', name: '', url: '', enabled: true }])}>{t('添加市场源')}</Button>
            <Button disabled={locked || loading || !loaded} onClick={() => {
              setValidation(''); save.clearError()
              try { setPendingSave(validateSources(draft, t)) }
              catch (cause) { setValidation(cause instanceof Error ? cause.message : t('市场源配置无效')) }
            }}>{t('保存市场源')}</Button>
          </div>
        </div>
      </details>
      <div className='flex flex-wrap items-end gap-2'>
        <div className='min-w-0 flex-1'><Label htmlFor={`${id}-source`}>{t('选择市场源')}</Label><select id={`${id}-source`} className={optionStyle} value={selected} disabled={locked || loading} onChange={(event) => { clearCatalog(); setSelected(event.target.value); setNotice('') }}>
          <option value=''>{t('请选择市场源')}</option>{sources.filter((row) => row.enabled).map((row) => <option key={row.id} value={row.id}>{row.name}</option>)}
        </select></div>
        <Button variant='outline' disabled={locked || loading} onClick={() => { clearCatalog(); loadSources() }}>{t('刷新市场源')}</Button>
        <Button variant='outline' disabled={locked || !selected || catalog.busy} onClick={() => {
          clearCatalog(); setNotice('')
          void catalog.run((config) => loadMarketIndex(selected, config, t), setIndex, t('市场索引读取失败'))
        }}>{t('浏览市场')}</Button>
      </div>
      {loaded && sources.length === 0 && <p>{t('尚未配置市场源')}</p>}
      {(loading || catalog.busy) && <p role='status'>{t('正在读取插件市场')}</p>}
      {[loadError, catalog.error, validation].filter(Boolean).map((message, position) => <p key={position} role='alert' className='text-destructive text-sm break-words'>{message}</p>)}
      {notice && <p role='status'>{notice}</p>}
      {index && <div className='space-y-3'>
        <h6 className='font-medium'>{index.source.name}</h6>
        {index.plugins.length === 0 && <p>{t('此市场暂无插件')}</p>}
        {index.plugins.slice(page * 20, (page + 1) * 20).map((entry) => <div key={JSON.stringify([entry.key, entry.version])} className='space-y-2 rounded-md border p-3'>
          <p className='break-all'>{entry.name} · {entry.key} · {entry.version}</p>
          {entry.description && <p className='text-sm break-words'>{entry.description}</p>}
          <p className='text-muted-foreground text-xs break-all'>{entry.source_url}<br />SHA-256: {entry.sha256}</p>
          <Button variant='outline' disabled={locked} onClick={() => { save.clearError(); setPendingInstall({ source: index.source, entry }) }}>{t('安装插件版本')}</Button>
        </div>)}
        {index.plugins.length > 20 && <div className='flex items-center gap-3'>
          <Button variant='outline' disabled={page === 0} onClick={() => setPage((value) => value - 1)}>{t('上一页')}</Button><span>{page + 1} / {Math.ceil(index.plugins.length / 20)}</span><Button variant='outline' disabled={(page + 1) * 20 >= index.plugins.length} onClick={() => setPage((value) => value + 1)}>{t('下一页')}</Button>
        </div>}
      </div>}
      <ConfirmDialog open={pendingSave !== null} onOpenChange={(open) => { if (!open && !save.busy) setPendingSave(null) }} title={t('保存市场源')} confirmText={t('确认')} isLoading={save.busy} disabled={disabled}
        desc={t('保存将替换当前市场源配置；移除市场源不会删除已经安装的插件。')}
        handleConfirm={() => {
          if (!pendingSave || locked) return
          cancelSources(); clearCatalog()
          const rows = pendingSave
          void save.run((config) => saveMarketSources(rows, config), () => {
            dirty.current = false; setSources(rows); setDraft(rows); setSelected(rows.find((row) => row.enabled)?.id ?? ''); setPendingSave(null); setNotice(t('市场源已保存'))
          }, t('插件操作失败，请检查后重试'))
        }}>
        <pre className='max-h-48 overflow-auto text-xs break-all whitespace-pre-wrap'>{JSON.stringify(pendingSave, null, 2)}</pre>
        {save.error && <p role='alert' className='text-destructive text-sm'>{save.error}</p>}
      </ConfirmDialog>
      <ConfirmDialog open={pendingInstall !== null} onOpenChange={(open) => { if (!open && !save.busy) setPendingInstall(null) }} title={t('安装插件版本')} confirmText={t('确认')} isLoading={save.busy} disabled={disabled}
        desc={t('将按选定版本与 SHA-256 摘要安装禁用修订。安装后请审阅完整能力和源码，启用与绑定需要单独确认。')}
        handleConfirm={() => {
          if (!pendingInstall || locked || !session.isCurrent()) return
          const target = pendingInstall
          void save.run((config) => installMarketRevision(target.source.id, target.entry, config, t), (revisionId) => {
            setPendingInstall(null); setNotice(t('插件已安装并禁用，请审阅能力和源码')); onInstalled(revisionId)
          }, t('插件操作失败，请检查后重试'))
        }}>
        {pendingInstall && <div className='max-h-60 space-y-2 overflow-auto text-sm break-all'>
          <p>{pendingInstall.source.name} · {pendingInstall.source.id}</p>
          <p>{pendingInstall.entry.key} · {pendingInstall.entry.version}</p>
          <p>{pendingInstall.entry.source_url}</p>
          <p>SHA-256: {pendingInstall.entry.sha256}</p>
        </div>}
        {save.error && <p role='alert' className='text-destructive text-sm'>{save.error}</p>}
      </ConfirmDialog>
    </section>
  )
}
