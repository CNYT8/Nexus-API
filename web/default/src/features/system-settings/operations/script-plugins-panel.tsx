import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  useAccountSession,
  type AccountSession,
} from '@/features/playground/hooks/use-account-session'
import { ScriptPluginBindings } from './script-plugin-bindings'
import { ScriptPluginMarketplace } from './script-plugin-marketplace'
import { ScriptPluginCapabilities } from './script-plugin-capabilities'
import { ScriptPluginLifecycle } from './script-plugin-lifecycle'
import {
  byteLength,
  createScriptPlugin,
  getScriptPlugin,
  listScriptPlugins,
  SCRIPT_PAGE_SIZE,
  setScriptPluginEnabled,
  SOURCE_LIMIT,
  sourceProblem,
  type ScriptPlugin,
  type ScriptPluginDetail,
} from './script-plugins-api'
import { useScriptPluginRequest } from './use-script-plugin-request'

type PendingAction =
  | { kind: 'create'; source: string }
  | { kind: 'enabled'; plugin: ScriptPlugin; enabled: boolean }
  | { kind: 'copy'; plugin: ScriptPluginDetail }
type RevisionPage = { rows: ScriptPlugin[]; cursors: number[]; loaded: boolean }

export function ScriptPluginsPanel() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const session = useAccountSession()
  if (user?.role !== 100 || String(user.id) !== session.userId)
    return <p>{t('Root access is required to manage task usage plugins')}</p>
  // Session boundaries also discard drafts/dialogs on logout and same-user login.
  return <ScriptPluginsContent key={session.revision} session={session} />
}

function ScriptPluginsContent({ session }: { session: AccountSession }) {
  const { t } = useTranslation()
  const fieldId = useId()
  const [page, setPage] = useState<RevisionPage>({
    rows: [],
    cursors: [0],
    loaded: false,
  })
  const [source, setSource] = useState('')
  const [draftError, setDraftError] = useState('')
  const [detailId, setDetailId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ScriptPluginDetail | null>(null)
  const [pending, setPending] = useState<PendingAction | null>(null)
  const [notice, setNotice] = useState('')
  const [marketBusy, setMarketBusy] = useState(false)
  const {
    run: readList,
    cancel: cancelList,
    busy: listBusy,
    error: listError,
  } = useScriptPluginRequest(session)
  const {
    run: readDetail,
    cancel: cancelDetail,
    busy: detailBusy,
    error: detailError,
  } = useScriptPluginRequest(session)
  const {
    run: mutate,
    clearError,
    busy: mutationBusy,
    error: saveError,
  } = useScriptPluginRequest(session, false)
  const saving = mutationBusy || marketBusy

  const loadPage = useCallback(
    (cursors: number[]) => {
      void readList(
        (config) => listScriptPlugins(cursors[cursors.length - 1], config),
        (rows) => setPage({ rows, cursors, loaded: true }),
        t('Failed to load task plugins')
      )
    },
    [readList, t]
  )
  useEffect(() => {
    loadPage([0])
  }, [loadPage])

  const showDetail = (id: number) => {
    setDetailId(id)
    setDetail(null)
    void readDetail(
      (config) => getScriptPlugin(id, config),
      setDetail,
      t('Failed to load task plugins')
    )
  }
  const closeDetail = () => {
    cancelDetail()
    setDetailId(null)
    setDetail(null)
  }
  const ask = (action: PendingAction) => {
    clearError()
    setNotice('')
    setPending(action)
  }
  const validateSource = (value: string) => {
    const problem = sourceProblem(value)
    const message =
      problem === 'required'
        ? t('Required')
        : problem === 'source-limit'
          ? `${t('Source')}: ${t('Limit')} 1 MiB`
          : problem === 'body-limit'
            ? `JSON: ${t('Limit')} 2 MiB`
            : ''
    setDraftError(message)
    return !message
  }
  const confirmAction = () => {
    if (!session.isCurrent() || !pending || saving) return
    if (pending.kind === 'copy') {
      setSource(pending.plugin.source)
      setDraftError('')
      setPending(null)
      return
    }
    if (pending.kind === 'create' && !validateSource(pending.source)) {
      setPending(null)
      return
    }
    cancelList()
    closeDetail()
    void mutate(
      async (config) => {
        if (pending.kind === 'create')
          return createScriptPlugin(pending.source, config)
        await setScriptPluginEnabled(pending.plugin.id, pending.enabled, config)
        return null
      },
      (created) => {
        if (pending.kind === 'enabled') {
          setPage((current) => ({
            ...current,
            rows: current.rows.map((row) =>
              row.id === pending.plugin.id
                ? { ...row, enabled: pending.enabled }
                : row
            ),
          }))
        }
        setPending(null)
        setNotice(
          created
            ? `${t('Create succeeded')} #${created.id} · ${t('Disabled')}`
            : t('Operation successful')
        )
        // Keep the draft even on success; a refresh never edits it.
        loadPage(created ? [0] : page.cursors)
      },
      t('Task plugin request failed; draft retained')
    )
  }
  const pendingTitle =
    pending?.kind === 'create'
      ? t('Create immutable revision')
      : pending?.kind === 'copy'
        ? t('Copy to new revision draft')
        : pending?.enabled
          ? t('Confirm enable')
          : t('Confirm disable')

  return (
    <section
      aria-labelledby={`${fieldId}-title`}
      className='min-w-0 space-y-6 border-t pt-6'
    >
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h4 id={`${fieldId}-title`} className='text-base font-semibold'>
          JavaScript · {t('Task usage plugins')}
        </h4>
        <Button
          variant='outline'
          disabled={listBusy || saving}
          onClick={() => loadPage(page.cursors)}
        >
          {t('Refresh')}
        </Button>
      </div>
      {notice && (
        <p role='status' className='text-sm'>
          {notice}
        </p>
      )}
      <ScriptPluginMarketplace
        session={session}
        disabled={mutationBusy || pending !== null}
        onBusyChange={setMarketBusy}
        onInstalled={(id) => { cancelList(); cancelDetail(); loadPage([0]); showDetail(id) }}
      />
      <section
        aria-labelledby={`${fieldId}-revisions`}
        className='min-w-0 space-y-3'
        aria-busy={listBusy}
      >
        <h5 id={`${fieldId}-revisions`} className='text-sm font-semibold'>
          {t('Immutable revisions')}
        </h5>
        {listError && (
          <p role='alert' className='text-destructive text-sm break-words'>
            {listError}
          </p>
        )}
        {listBusy && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('Loading...')}
          </p>
        )}
        <div className='rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead scope='col'>{t('ID')}</TableHead>
                <TableHead scope='col'>
                  {t('Name')} / {t('Key')}
                </TableHead>
                <TableHead scope='col'>{t('Version')}</TableHead>
                <TableHead scope='col'>{t('Status')}</TableHead>
                <TableHead scope='col'>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {page.rows.map((plugin) => (
                <TableRow key={plugin.id}>
                  <TableCell>#{plugin.id}</TableCell>
                  <TableCell className='max-w-64 break-words whitespace-normal'>
                    <div className='font-medium'>{plugin.name}</div>
                    <div className='text-muted-foreground'>{plugin.key}</div>
                  </TableCell>
                  <TableCell>{plugin.version}</TableCell>
                  <TableCell>
                    <Badge variant={plugin.enabled ? 'default' : 'secondary'}>
                      {plugin.enabled ? t('Enabled') : t('Disabled')}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div className='flex flex-wrap gap-2'>
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={saving}
                        onClick={() => showDetail(plugin.id)}
                        aria-label={`${t('Details')} #${plugin.id}`}
                      >
                        {t('Details')}
                      </Button>
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={saving || listBusy}
                        onClick={() =>
                          ask({
                            kind: 'enabled',
                            plugin,
                            enabled: !plugin.enabled,
                          })
                        }
                        aria-label={`${plugin.enabled ? t('Disable') : t('Enable')} #${plugin.id}`}
                      >
                        {plugin.enabled ? t('Disable') : t('Enable')}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {page.loaded && !listBusy && page.rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5}>
                    {t('No immutable revisions')}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div className='flex flex-wrap items-center gap-3'>
          <Button
            variant='outline'
            size='sm'
            disabled={listBusy || saving || page.cursors.length <= 1}
            onClick={() => loadPage(page.cursors.slice(0, -1))}
          >
            {t('Previous page')}
          </Button>
          <span className='text-muted-foreground text-sm'>
            {t('Page')} {page.cursors.length}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={
              listBusy || saving || page.rows.length !== SCRIPT_PAGE_SIZE
            }
            onClick={() => {
              const last = page.rows[page.rows.length - 1]
              if (last) loadPage([...page.cursors, last.id])
            }}
          >
            {t('Next page')}
          </Button>
        </div>
      </section>

      {detailId !== null && (
        <section
          aria-labelledby={`${fieldId}-detail-title`}
          className='space-y-3 rounded-md border p-3'
          aria-busy={detailBusy}
        >
          <div className='flex items-center justify-between gap-3'>
            <h5
              id={`${fieldId}-detail-title`}
              className='text-sm font-semibold'
            >
              {t('Details')} #{detailId}
            </h5>
            <div className='flex gap-2'>
              <Button
                variant='outline'
                size='sm'
                disabled={detailBusy || saving}
                onClick={() => showDetail(detailId)}
              >
                {t('Refresh')}
              </Button>
              <Button variant='ghost' size='sm' disabled={saving} onClick={closeDetail}>
                {t('Close')}
              </Button>
            </div>
          </div>
          {detailBusy && (
            <p role='status' className='text-sm'>
              {t('Loading...')}
            </p>
          )}
          {detailError && (
            <p role='alert' className='text-destructive text-sm break-words'>
              {detailError}
            </p>
          )}
          {detail && (
            <>
              <p className='text-sm break-words'>
                {detail.name} · {detail.key} · {detail.version}
              </p>
              <ScriptPluginCapabilities meta={detail.meta} />
              <ScriptPluginLifecycle
                key={`${detail.id}:${detail.digest}`}
                plugin={detail}
                session={session}
                busy={saving}
                error={saveError}
                onChange={(request, done) => {
                  if (saving || !session.isCurrent()) return
                  cancelList()
                  cancelDetail()
                  void mutate(request, () => {
                    cancelList()
                    closeDetail()
                    done()
                    setNotice(t('插件操作成功'))
                    loadPage([0])
                  }, t('插件操作失败，请检查后重试'))
                }}
              />
              <div className='space-y-2'>
                <Label htmlFor={`${fieldId}-source-view`}>{t('Source')}</Label>
                <Textarea
                  id={`${fieldId}-source-view`}
                  value={detail.source}
                  readOnly
                  spellCheck={false}
                  autoComplete='off'
                  className='max-h-96 font-mono text-xs'
                  rows={10}
                />
              </div>
              <details>
                <summary className='cursor-pointer text-sm'>
                  {t('Metadata')} / SHA-256
                </summary>
                <pre className='mt-2 max-h-48 overflow-auto text-xs break-all whitespace-pre-wrap'>
                  {detail.meta}
                  {'\n'}
                  {detail.digest}
                </pre>
              </details>
              <Button
                variant='outline'
                size='sm'
                disabled={saving}
                onClick={() => ask({ kind: 'copy', plugin: detail })}
              >
                {t('Copy to new revision draft')}
              </Button>
            </>
          )}
        </section>
      )}

      <section aria-labelledby={`${fieldId}-create`} className='space-y-3'>
        <div className='flex flex-wrap items-center gap-3'>
          <h5 id={`${fieldId}-create`} className='text-sm font-semibold'>
            {t('Create immutable revision')}
          </h5>
          <Badge variant='secondary'>{t('Disabled')}</Badge>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-source`}>
            JavaScript · {t('Source')}
          </Label>
          <Textarea
            id={`${fieldId}-source`}
            value={source}
            rows={12}
            spellCheck={false}
            autoComplete='off'
            autoCapitalize='off'
            autoCorrect='off'
            className='max-h-[32rem] font-mono text-xs'
            disabled={saving}
            aria-invalid={Boolean(draftError)}
            aria-describedby={`${fieldId}-source-limit${draftError ? ` ${fieldId}-source-error` : ''}`}
            onChange={(event) => {
              setSource(event.target.value)
              setDraftError('')
              setNotice('')
            }}
          />
          <p
            id={`${fieldId}-source-limit`}
            className='text-muted-foreground text-xs'
          >
            {byteLength(source).toLocaleString()} /{' '}
            {SOURCE_LIMIT.toLocaleString()} B
          </p>
          {draftError && (
            <p
              id={`${fieldId}-source-error`}
              role='alert'
              className='text-destructive text-sm'
            >
              {draftError}
            </p>
          )}
        </div>
        <Button
          disabled={saving || !source.trim()}
          onClick={() => {
            if (validateSource(source)) ask({ kind: 'create', source })
          }}
        >
          {t('Create immutable revision')}
        </Button>
      </section>

      <ScriptPluginBindings session={session} />

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open && !saving) setPending(null)
        }}
        title={pendingTitle}
        confirmText={t('Confirm')}
        isLoading={saving}
        destructive={pending?.kind === 'enabled' && !pending.enabled}
        handleConfirm={confirmAction}
        desc={
          <div className='space-y-2 text-sm break-words'>
            {pending?.kind === 'create' ? (
              <>
                <p>
                  {t('Status')}: {t('Disabled')}
                </p>
                <p>
                  {t('Source')}: {byteLength(pending.source).toLocaleString()} B
                </p>
              </>
            ) : pending ? (
              <>
                <p>
                  #{pending.plugin.id} · {pending.plugin.name} ·{' '}
                  {pending.plugin.version}
                </p>
                {pending.kind === 'copy' ? (
                  <p>
                    {t('Source')}
                    {source ? ` · ${t('Unsaved changes')}` : ''}
                  </p>
                ) : (
                  <p>
                    {t('Status')}:{' '}
                    {pending.enabled ? t('Enabled') : t('Disabled')}
                  </p>
                )}
              </>
            ) : (
              ''
            )}
          </div>
        }
      >
        {(pending?.kind === 'copy' || pending?.kind === 'create') && (
          <pre className='max-h-48 overflow-auto text-xs break-all whitespace-pre-wrap'>
            {pending.kind === 'create' ? pending.source : pending.plugin.source}
          </pre>
        )}
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
