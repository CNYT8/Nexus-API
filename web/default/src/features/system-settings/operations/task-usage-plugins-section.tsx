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
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { api, type ApiRequestConfig } from '@/lib/api'
import { useAccountSession, type AccountSession } from '@/features/playground/hooks/use-account-session'
import { useScriptPluginRequest } from './use-script-plugin-request'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { ScriptPluginsPanel } from './script-plugins-panel'

type Plugin = {
  id: number
  name: string
  version: string
  schema: string
  expression: string
}
type Draft = Omit<Plugin, 'id'>
type Contract = {
  kind: string
  api_version: number
  example_create: Draft
  example_response: Record<string, unknown>
  example_group_ratio: number
  example_other_ratios: Record<string, number>
  capabilities: { preview: boolean; usage_billing: boolean }
  limits: Record<string, number>
  usage_schema: unknown
  expression_operators: string[]
}
type Preview = {
  dry_run: boolean
  quota: number
  quota_per_unit: number
  currency: string
}
const format = (value: unknown) => JSON.stringify(value, null, 2)
const blankDraft: Draft = { name: '', version: '', schema: '', expression: '' }

function unwrap<T>(body: { success: boolean; data: T }): T {
  if (body?.success !== true || body.data === undefined) throw body
  return body.data
}
function parseObject(value: string): Record<string, unknown> {
  const parsed: unknown = JSON.parse(value)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('Expected a JSON object')
  }
  return parsed as Record<string, unknown>
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
const validDraft = (value: unknown): value is Draft =>
  isRecord(value) && ['name', 'version', 'schema', 'expression'].every((key) => typeof value[key] === 'string')
const validPlugin = (value: unknown): value is Plugin =>
  isRecord(value) && validDraft(value) && Number.isSafeInteger(value.id) && Number(value.id) > 0
const validBindings = (value: unknown): value is Record<string, number> =>
  isRecord(value) && Object.entries(value).every(([model, id]) =>
    model.trim() !== '' && model === model.trim() && !/[\r\n\t\0]/.test(model) &&
    typeof id === 'number' && Number.isSafeInteger(id) && id >= 0)

function readPlugins(body: { success: boolean; data: unknown }, before = Infinity): Plugin[] {
  const value = unwrap(body) ?? []
  if (!Array.isArray(value) || value.length > 100 || value.some((item, index) =>
    !validPlugin(item) || item.id >= (index ? value[index - 1].id : before))) throw null
  return value
}
function readBindings(body: { success: boolean; data: unknown }) {
  const value = unwrap(body)
  if (!isRecord(value) || !Object.hasOwn(value, 'bindings')) throw null
  const mapping = value.bindings ?? {}
  if (!validBindings(mapping)) throw null
  return mapping
}
function readContract(body: { success: boolean; data: Contract }) {
  const value = unwrap(body)
  if (!isRecord(value) || value.kind !== 'declarative_usage_billing' || value.api_version !== 1 ||
    !validDraft(value.example_create) || !isRecord(value.example_response) ||
    !Number.isFinite(value.example_group_ratio) || value.example_group_ratio < 0 ||
    !isRecord(value.example_other_ratios) || Object.values(value.example_other_ratios).some((ratio) =>
      typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio < 0) ||
    !isRecord(value.capabilities) || typeof value.capabilities.preview !== 'boolean' ||
    typeof value.capabilities.usage_billing !== 'boolean' || !isRecord(value.limits) ||
    !Array.isArray(value.expression_operators) || value.expression_operators.some((item) => typeof item !== 'string')) throw null
  return value
}

export function TaskUsagePluginsSection() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const session = useAccountSession()
  if (user?.role !== 100 || !session.userId || String(user.id) !== session.userId) {
    return <p>{t('Root access is required to manage task usage plugins')}</p>
  }
  return <TaskUsagePluginsContent key={session.revision} session={session} />
}

function TaskUsagePluginsContent({ session }: { session: AccountSession }) {
  const { t } = useTranslation()
  const fieldId = useId()
  const [contract, setContract] = useState<Contract | null>(null)
  const [plugins, setPlugins] = useState<Plugin[]>([])
  const [more, setMore] = useState(false)
  const [draft, setDraft] = useState<Draft>(blankDraft)
  const [response, setResponse] = useState('{}')
  const [groupRatio, setGroupRatio] = useState('1')
  const [otherRatios, setOtherRatios] = useState('{}')
  const [bindings, setBindings] = useState<string | null>(null)
  const [preview, setPreview] = useState<Preview | null>(null)
  const [notice, setNotice] = useState('')
  const { run: read, cancel, busy: reading, error: readError, clearError } = useScriptPluginRequest(session)
  const { run: write, busy: writing, error: writeError, clearError: clearWriteError } = useScriptPluginRequest(session, false)
  const busy = reading || writing
  const error = writeError || readError

  const load = useCallback(() => {
    if (!session.isCurrent()) return
    setNotice('')
    clearWriteError()
    return read(async (config) => {
      const [schemaResult, listResult, bindingsResult] = await Promise.all([
        api.get('/api/task/plugin/schema', config),
        api.get('/api/task/plugin', config),
        api.get('/api/task/plugin/bindings', config),
      ])
      return { schema: readContract(schemaResult.data), list: readPlugins(listResult.data), saved: readBindings(bindingsResult.data) }
    }, ({ schema, list, saved }) => {
      setContract(schema)
      setPlugins(list)
      setMore(list.length === 100)
      setBindings((current) => current ?? format(saved))
    }, t('Failed to load task plugins'))
  }, [read, session, clearWriteError, t])

  useEffect(() => {
    void load()
  }, [load])

  const run = <T,>(action: (config: ApiRequestConfig) => Promise<T>, apply: (value: T) => void) => {
    if (!session.isCurrent() || busy || !contract) return
    cancel()
    clearError()
    setNotice('')
    setPreview(null)
    return write(action, apply, t('Task plugin request failed; draft retained'))
  }
  const updateDraft = (key: keyof Draft, value: string) => {
    setDraft((current) => ({ ...current, [key]: value }))
    setPreview(null)
    setNotice('')
  }
  const validatedDraft = () => {
    if (
      !draft.name.trim() ||
      !draft.version.trim() ||
      !draft.expression.trim()
    ) {
      throw new Error(t('Plugin name, version and expression are required'))
    }
    parseObject(draft.schema)
    return { ...draft, name: draft.name.trim(), version: draft.version.trim() }
  }
  const previewDraft = () =>
    run(async (config) => {
      const ratios = parseObject(otherRatios)
      const ratio = Number(groupRatio)
      if (
        !groupRatio.trim() ||
        !Number.isFinite(ratio) ||
        ratio < 0 ||
        Object.values(ratios).some(
          (value) =>
            typeof value !== 'number' || !Number.isFinite(value) || value < 0
        )
      ) {
        throw new Error(t('Multipliers must be finite non-negative numbers'))
      }
      const result = await api.post(
        '/api/task/plugin/preview',
        {
          plugin: validatedDraft(),
          response: parseObject(response),
          group_ratio: ratio,
          other_ratios: ratios,
        },
        config
      )
      const value = unwrap<Preview>(result.data)
      if (!value || value.dry_run !== true || value.currency !== 'USD' ||
        !Number.isFinite(value.quota) || value.quota < 0 ||
        !Number.isFinite(value.quota_per_unit) || value.quota_per_unit <= 0) {
        throw new Error(t('Invalid preview response'))
      }
      return value
    }, setPreview)
  const create = () =>
    run(async (config) => {
      const result = await api.post(
        '/api/task/plugin',
        validatedDraft(),
        config
      )
      const saved = unwrap<Plugin>(result.data)
      if (!validPlugin(saved)) throw null
      return saved
    }, (saved) => {
      setPlugins((current) => [
        saved,
        ...current.filter((item) => item.id !== saved.id),
      ])
      setPreview(null)
      setNotice(
        t('Created immutable revision #{{id}}. Bind a model to enable it.', {
          id: saved.id,
        })
      )
    })
  const saveBindings = () =>
    run(async (config) => {
      const mapping = parseObject(bindings ?? '{}')
      if (!validBindings(mapping)) {
        throw new Error(
          t(
            'Bindings require exact model names and non-negative integer revision IDs'
          )
        )
      }
      const result = await api.put(
        '/api/task/plugin/bindings',
        { bindings: mapping },
        config
      )
      return readBindings(result.data)
    }, (saved) => {
      setBindings(format(saved))
      setNotice(t('Model bindings saved'))
    })
  const loadMore = () =>
    run(async (config) => {
      const before = plugins[plugins.length - 1]?.id
      if (!before) throw null
      const result = await api.get('/api/task/plugin', {
        ...config,
        params: { before_id: before },
      })
      return readPlugins(result.data, before)
    }, (list) => {
      setPlugins((current) => [
        ...current,
        ...list.filter((item) => !current.some((old) => old.id === item.id)),
      ])
      setMore(list.length === 100)
    })

  return (
    <SettingsSection title={t('Task usage plugins')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Immutable declarative usage expressions in USD. Invalid usage keeps the reserved quota.'
        )}
      </p>
      <div>
        <Button
          variant='outline'
          disabled={busy}
          onClick={() => void load()}
        >
          {t('Refresh')}
        </Button>
      </div>
      {error && (
        <p role='alert' className='text-destructive text-sm break-words'>
          {error}
        </p>
      )}
      {notice && (
        <p role='status' className='text-sm'>
          {notice}
        </p>
      )}
      {busy && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t('Loading...')}
        </p>
      )}
      <fieldset
        disabled={busy || !contract}
        className='flex min-w-0 flex-col gap-4'
      >
        <details className='rounded-md border p-3'>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('Usage schema and expression contract')}
          </summary>
          <pre className='mt-2 max-h-64 overflow-auto text-xs break-all whitespace-pre-wrap'>
            {format({
              usage_schema: contract?.usage_schema,
              expression_operators: contract?.expression_operators,
              limits: contract?.limits,
            })}
          </pre>
        </details>
        <div className='flex flex-wrap items-center gap-2'>
          <h4 className='text-sm font-semibold'>
            {t('Create immutable revision')}
          </h4>
          <Button
            variant='outline'
            size='sm'
            onClick={() => {
              if (!contract) return
              const example = contract.example_create
              setDraft({
                name: example.name,
                version: example.version,
                schema: example.schema,
                expression: example.expression,
              })
              setResponse(format(contract.example_response))
              setGroupRatio(String(contract.example_group_ratio))
              setOtherRatios(format(contract.example_other_ratios))
              setPreview(null)
              setNotice('')
            }}
          >
            {t('Load example into draft')}
          </Button>
        </div>
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor={`${fieldId}-name`}>{t('Name')}</Label>
            <Input
              id={`${fieldId}-name`}
              value={draft.name}
              onChange={(event) => updateDraft('name', event.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor={`${fieldId}-version`}>{t('Version')}</Label>
            <Input
              id={`${fieldId}-version`}
              value={draft.version}
              onChange={(event) => updateDraft('version', event.target.value)}
            />
          </div>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-schema`}>
            {t('Usage schema (JSON)')}
          </Label>
          <Textarea
            id={`${fieldId}-schema`}
            className='font-mono'
            rows={6}
            value={draft.schema}
            onChange={(event) => updateDraft('schema', event.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-expression`}>
            {t('Usage expression (USD)')}
          </Label>
          <Textarea
            id={`${fieldId}-expression`}
            className='font-mono'
            value={draft.expression}
            onChange={(event) => updateDraft('expression', event.target.value)}
          />
        </div>
        <div>
          <Button
            disabled={!contract?.capabilities.usage_billing}
            onClick={() => void create()}
          >
            {t('Create immutable revision')}
          </Button>
        </div>
        <h4 className='text-sm font-semibold'>{t('Preview draft usage')}</h4>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Preview only: no task dispatch or balance changes. Quota is calculated by the server.'
          )}
        </p>
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-response`}>
            {t('Provider response (JSON)')}
          </Label>
          <Textarea
            id={`${fieldId}-response`}
            className='font-mono'
            rows={4}
            value={response}
            onChange={(event) => {
              setResponse(event.target.value)
              setPreview(null)
            }}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-group`}>{t('Group multiplier')}</Label>
          <Input
            id={`${fieldId}-group`}
            inputMode='decimal'
            value={groupRatio}
            onChange={(event) => {
              setGroupRatio(event.target.value)
              setPreview(null)
            }}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-ratios`}>
            {t('Other multipliers (JSON)')}
          </Label>
          <Textarea
            id={`${fieldId}-ratios`}
            className='font-mono'
            value={otherRatios}
            onChange={(event) => {
              setOtherRatios(event.target.value)
              setPreview(null)
            }}
          />
        </div>
        <div>
          <Button
            variant='outline'
            disabled={!contract?.capabilities.preview}
            onClick={() => void previewDraft()}
          >
            {t('Preview draft usage')}
          </Button>
        </div>
        {preview && (
          <div role='status' className='rounded-md border p-3 text-sm'>
            {t('Server quota')}: <code>{String(preview.quota)}</code>
            <br />
            {t('Quota per USD')}: <code>{String(preview.quota_per_unit)}</code>{' '}
            ({preview.currency})
          </div>
        )}
        <h4 className='text-sm font-semibold'>{t('Immutable revisions')}</h4>
        <div className='max-h-72 space-y-2 overflow-auto'>
          {plugins.length === 0 && (
            <p className='text-muted-foreground text-sm'>
              {t('No immutable revisions')}
            </p>
          )}
          {plugins.map((plugin) => (
            <details key={plugin.id} className='rounded-md border p-3'>
              <summary className='cursor-pointer text-sm break-words'>
                #{plugin.id} · {plugin.name} · {plugin.version}
              </summary>
              <pre className='my-2 overflow-auto text-xs break-all whitespace-pre-wrap'>
                {format(plugin)}
              </pre>
              <Button
                variant='outline'
                size='sm'
                onClick={() => {
                  setDraft({
                    name: plugin.name,
                    version: '',
                    schema: plugin.schema,
                    expression: plugin.expression,
                  })
                  setPreview(null)
                  setNotice('')
                }}
              >
                {t('Copy to new revision draft')}
              </Button>
            </details>
          ))}
        </div>
        {more && (
          <div>
            <Button variant='outline' onClick={() => void loadMore()}>
              {t('Load more')}
            </Button>
          </div>
        )}
        <div className='space-y-2'>
          <Label htmlFor={`${fieldId}-bindings`}>
            {t('Exact model bindings (JSON)')}
          </Label>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Saving replaces the entire mapping. Use exact model names and revision IDs; remove a key or use 0 to unbind. Existing task snapshots stay unchanged.'
            )}
          </p>
          <Textarea
            id={`${fieldId}-bindings`}
            className='font-mono'
            rows={6}
            value={bindings ?? ''}
            placeholder={'{"model-name": 1}'}
            onChange={(event) => {
              setBindings(event.target.value)
              setNotice('')
            }}
          />
        </div>
        <div>
          <Button
            disabled={bindings === null}
            onClick={() => void saveBindings()}
          >
            {t('Save model bindings')}
          </Button>
        </div>
      </fieldset>
      <ScriptPluginsPanel />
    </SettingsSection>
  )
}
