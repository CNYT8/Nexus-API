import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ConfirmDialog } from '@/components/confirm-dialog'
import type { AccountSession } from '@/features/playground/hooks/use-account-session'
import {
  BINDING_LIMIT,
  exactModel,
  listScriptBindings,
  positiveId,
  putScriptBinding,
  type BindingInput,
  type ScriptBinding,
} from './script-plugins-api'
import { useScriptPluginRequest } from './use-script-plugin-request'

type BindingPage = { rows: ScriptBinding[]; channelId: number; loaded: boolean }
type PendingBinding = { input: BindingInput; previousId?: number }

export function ScriptPluginBindings({ session }: { session: AccountSession }) {
  const { t } = useTranslation()
  const fieldId = useId()
  const [page, setPage] = useState<BindingPage>({
    rows: [],
    channelId: 0,
    loaded: false,
  })
  const [filter, setFilter] = useState('')
  const [filterError, setFilterError] = useState('')
  const [draft, setDraft] = useState({ channel: '', model: '', plugin: '' })
  const [draftError, setDraftError] = useState('')
  const [pending, setPending] = useState<PendingBinding | null>(null)
  const [notice, setNotice] = useState('')
  const {
    run: read,
    cancel,
    busy: loading,
    error: readError,
  } = useScriptPluginRequest(session)
  const {
    run: mutate,
    clearError,
    busy: saving,
    error: saveError,
  } = useScriptPluginRequest(session, false)

  const load = useCallback(
    (channelId: number) => {
      void read(
        (config) => listScriptBindings(channelId, config),
        (rows) => setPage({ rows, channelId, loaded: true }),
        t('Failed to load task plugins')
      )
    },
    [read, t]
  )
  useEffect(() => {
    load(0)
  }, [load])

  const search = () => {
    const channelId = filter === '' || filter === '0' ? 0 : positiveId(filter)
    if (channelId === null) {
      setFilterError(t('Please enter a valid number'))
      return
    }
    setFilterError('')
    load(channelId)
  }
  const updateDraft = (key: keyof typeof draft, value: string) => {
    setDraft((current) => ({ ...current, [key]: value }))
    setDraftError('')
    setNotice('')
  }
  const ask = (value: PendingBinding) => {
    clearError()
    setNotice('')
    setPending(value)
  }
  const prepare = (unbind: boolean) => {
    const channelId = positiveId(draft.channel)
    const pluginId = unbind ? 0 : positiveId(draft.plugin)
    if (channelId === null || pluginId === null) {
      setDraftError(
        `${channelId === null ? t('Channel ID') : `${t('Immutable revisions')} · ${t('ID')}`}: ${t('Please enter a valid number')}`
      )
      return
    }
    if (!exactModel(draft.model)) {
      setDraftError(
        t(
          'Bindings require exact model names and non-negative integer revision IDs'
        )
      )
      return
    }
    setDraftError('')
    const previous = page.rows.find(
      (row) => row.channel_id === channelId && row.model === draft.model
    )
    ask({
      input: { channel_id: channelId, model: draft.model, plugin_id: pluginId },
      previousId: previous?.plugin_id,
    })
  }
  const confirm = () => {
    if (!session.isCurrent() || !pending || saving) return
    cancel()
    void mutate(
      (config) => putScriptBinding(pending.input, config),
      () => {
        setPage((current) => ({
          ...current,
          rows: current.rows.flatMap((row) => {
            if (
              row.channel_id !== pending.input.channel_id ||
              row.model !== pending.input.model
            )
              return [row]
            return pending.input.plugin_id === 0
              ? []
              : [{ ...row, plugin_id: pending.input.plugin_id }]
          }),
        }))
        setPending(null)
        setNotice(t('Operation successful'))
        // The same draft stays available after either an error or a successful write.
        load(page.channelId)
      },
      t('Task plugin request failed; draft retained')
    )
  }

  return (
    <section
      aria-labelledby={`${fieldId}-title`}
      className='min-w-0 space-y-4 border-t pt-5'
    >
      <h5 id={`${fieldId}-title`} className='text-sm font-semibold'>
        {t('Bindings')}
      </h5>
      {notice && (
        <p role='status' className='text-sm'>
          {notice}
        </p>
      )}
      <div className='flex flex-wrap items-end gap-3'>
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-filter`}>{t('Channel ID')}</Label>
          <Input
            id={`${fieldId}-filter`}
            inputMode='numeric'
            value={filter}
            placeholder={t('All')}
            disabled={saving}
            aria-invalid={Boolean(filterError)}
            aria-describedby={
              filterError ? `${fieldId}-filter-error` : undefined
            }
            onChange={(event) => {
              setFilter(event.target.value)
              setFilterError('')
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                search()
              }
            }}
          />
        </div>
        <Button variant='outline' disabled={saving} onClick={search}>
          {t('Search')}
        </Button>
        <Button
          variant='outline'
          disabled={saving || loading}
          onClick={() => load(page.channelId)}
        >
          {t('Refresh')}
        </Button>
      </div>
      {filterError && (
        <p
          id={`${fieldId}-filter-error`}
          role='alert'
          className='text-destructive text-sm'
        >
          {filterError}
        </p>
      )}
      <p className='text-muted-foreground text-xs'>
        {t('Channel ID')}: {page.channelId || t('All')} ·{' '}
        {t('最多显示最近 {{limit}} 条记录', { limit: BINDING_LIMIT })}
      </p>
      {readError && (
        <p role='alert' className='text-destructive text-sm break-words'>
          {readError}
        </p>
      )}
      {loading && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t('Loading...')}
        </p>
      )}
      <div
        className='max-h-80 overflow-auto rounded-md border'
        aria-busy={loading}
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead scope='col'>{t('Channel ID')}</TableHead>
              <TableHead scope='col'>{t('Model Name')}</TableHead>
              <TableHead scope='col'>
                {t('Immutable revisions')} · {t('ID')}
              </TableHead>
              <TableHead scope='col'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {page.rows.map((binding) => (
              <TableRow key={binding.id}>
                <TableCell>{binding.channel_id}</TableCell>
                <TableCell className='max-w-64 break-words whitespace-normal'>
                  {binding.model}
                </TableCell>
                <TableCell>#{binding.plugin_id}</TableCell>
                <TableCell>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={saving || loading}
                    aria-label={`${t('Unbind')} ${binding.channel_id} / ${binding.model}`}
                    onClick={() =>
                      ask({
                        input: {
                          channel_id: binding.channel_id,
                          model: binding.model,
                          plugin_id: 0,
                        },
                        previousId: binding.plugin_id,
                      })
                    }
                  >
                    {t('Unbind')}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {page.loaded && !loading && page.rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={4}>{t('No data')}</TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <fieldset disabled={saving} className='min-w-0 space-y-3'>
        <legend className='mb-3 text-sm font-medium'>
          {t('Bind')} / {t('Unbind')}
        </legend>
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='space-y-2'>
            <Label htmlFor={`${fieldId}-channel`}>{t('Channel ID')}</Label>
            <Input
              id={`${fieldId}-channel`}
              inputMode='numeric'
              value={draft.channel}
              onChange={(event) => updateDraft('channel', event.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor={`${fieldId}-model`}>{t('Model Name')}</Label>
            <Input
              id={`${fieldId}-model`}
              value={draft.model}
              autoComplete='off'
              spellCheck={false}
              onChange={(event) => updateDraft('model', event.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor={`${fieldId}-plugin`}>
              {t('Immutable revisions')} · {t('ID')}
            </Label>
            <Input
              id={`${fieldId}-plugin`}
              inputMode='numeric'
              value={draft.plugin}
              onChange={(event) => updateDraft('plugin', event.target.value)}
            />
          </div>
        </div>
        {draftError && (
          <p role='alert' className='text-destructive text-sm break-words'>
            {draftError}
          </p>
        )}
        <div className='flex flex-wrap gap-2'>
          <Button onClick={() => prepare(false)}>{t('Bind')}</Button>
          <Button variant='outline' onClick={() => prepare(true)}>
            {t('Unbind')}
          </Button>
        </div>
      </fieldset>
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open && !saving) setPending(null)
        }}
        title={
          pending?.input.plugin_id === 0
            ? t('Confirm Unbind')
            : t('Confirm Changes')
        }
        confirmText={pending?.input.plugin_id === 0 ? t('Unbind') : t('Bind')}
        destructive={pending?.input.plugin_id === 0}
        isLoading={saving}
        handleConfirm={confirm}
        desc={
          <div className='space-y-2 text-sm break-words'>
            <p>
              {t('Channel ID')}: {pending?.input.channel_id}
            </p>
            <p>
              {t('Model Name')}:{' '}
              <span className='whitespace-pre-wrap'>
                {pending?.input.model}
              </span>
            </p>
            <p>
              {t('Immutable revisions')} · {t('ID')}:{' '}
              {pending?.previousId ? `#${pending.previousId} → ` : ''}
              {pending?.input.plugin_id === 0
                ? t('Unbind')
                : `#${pending?.input.plugin_id ?? ''}`}
            </p>
          </div>
        }
      >
        {saveError && (
          <p role='alert' className='text-destructive text-sm break-words'>
            {saveError}
          </p>
        )}
        {saving && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('Loading...')}
          </p>
        )}
      </ConfirmDialog>
    </section>
  )
}
