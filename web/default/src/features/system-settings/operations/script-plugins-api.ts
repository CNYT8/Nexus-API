import { api, type ApiRequestConfig } from '@/lib/api'

export type ScriptPlugin = {
  id: number
  key: string
  version: string
  name: string
  meta: string
  digest: string
  enabled: boolean
  created_at: number
}
export type ScriptPluginDetail = ScriptPlugin & { source: string }
export type ScriptBinding = {
  id: number
  channel_id: number
  model: string
  plugin_id: number
}
export type BindingInput = Pick<
  ScriptBinding,
  'channel_id' | 'model' | 'plugin_id'
>

export const SCRIPT_PAGE_SIZE = 100
export const BINDING_LIMIT = 1000
export const SOURCE_LIMIT = 1 << 20
const endpoint = '/api/task/script_plugin'
const config = (request: ApiRequestConfig) => ({
  ...request,
  disableDuplicate: true,
  skipBusinessError: true,
  skipErrorHandler: true,
})
export const byteLength = (text: string) =>
  new TextEncoder().encode(text).byteLength

export function positiveId(text: string): number | null {
  if (!/^\d+$/.test(text)) return null
  const value = Number(text)
  return Number.isSafeInteger(value) && value > 0 ? value : null
}

export function exactModel(text: string): boolean {
  return (
    text.length > 0 &&
    text === text.trim() &&
    !/[\r\n\0]/.test(text) &&
    byteLength(text) <= 255
  )
}

export function sourceProblem(
  source: string
): 'required' | 'source-limit' | 'body-limit' | null {
  if (!source.trim()) return 'required'
  if (byteLength(source) > SOURCE_LIMIT) return 'source-limit'
  // The controller limits the encoded JSON body as well as the source itself.
  if (byteLength(JSON.stringify({ source, enabled: false })) > 2 << 20)
    return 'body-limit'
  return null
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Request failed')
  return value as Record<string, unknown>
}

function data(body: unknown): unknown {
  const envelope = record(body)
  if (envelope.success !== true) throw body
  return envelope.data
}

function revision(value: unknown): ScriptPlugin {
  const item = record(value)
  if (
    typeof item.id !== 'number' ||
    !Number.isSafeInteger(item.id) ||
    item.id <= 0 ||
    typeof item.key !== 'string' ||
    typeof item.version !== 'string' ||
    typeof item.name !== 'string' ||
    typeof item.meta !== 'string' ||
    typeof item.digest !== 'string' ||
    typeof item.enabled !== 'boolean' ||
    typeof item.created_at !== 'number' ||
    !Number.isFinite(item.created_at)
  )
    throw new Error('Request failed')
  // Pick fields explicitly: list state must never retain source text.
  return {
    id: item.id,
    key: item.key,
    version: item.version,
    name: item.name,
    meta: item.meta,
    digest: item.digest,
    enabled: item.enabled,
    created_at: item.created_at,
  }
}

export async function listScriptPlugins(
  before: number,
  request: ApiRequestConfig
): Promise<ScriptPlugin[]> {
  const response = await api.get(endpoint, {
    ...config(request),
    params: { before_id: before },
  })
  const items = data(response.data)
  if (!Array.isArray(items) || items.length > SCRIPT_PAGE_SIZE)
    throw new Error('Request failed')
  const rows = items.map(revision)
  if (
    rows.some(
      (row, index) =>
        (before > 0 && row.id >= before) ||
        (index > 0 && row.id >= rows[index - 1].id)
    )
  )
    throw new Error('Request failed')
  return rows
}

export async function getScriptPlugin(
  id: number,
  request: ApiRequestConfig
): Promise<ScriptPluginDetail> {
  const response = await api.get(`${endpoint}/${id}`, config(request))
  const item = record(data(response.data))
  const plugin = revision(item)
  if (plugin.id !== id || typeof item.source !== 'string')
    throw new Error('Request failed')
  return { ...plugin, source: item.source }
}

export async function createScriptPlugin(
  source: string,
  request: ApiRequestConfig
): Promise<ScriptPlugin> {
  const response = await api.post(
    endpoint,
    { source, enabled: false },
    config(request)
  )
  return revision(data(response.data))
}

export async function setScriptPluginEnabled(
  id: number,
  enabled: boolean,
  request: ApiRequestConfig
): Promise<void> {
  const response = await api.put(
    `${endpoint}/${id}/enabled`,
    { enabled },
    config(request)
  )
  data(response.data) // A successful enable/disable response has null data.
}

export async function listScriptBindings(
  channelId: number,
  request: ApiRequestConfig
): Promise<ScriptBinding[]> {
  const response = await api.get(`${endpoint}/bindings`, {
    ...config(request),
    params: { channel_id: channelId },
  })
  const items = data(response.data)
  if (!Array.isArray(items) || items.length > BINDING_LIMIT)
    throw new Error('Request failed')
  return items.map((value) => {
    const item = record(value)
    if (
      typeof item.id !== 'number' ||
      !Number.isSafeInteger(item.id) ||
      item.id <= 0 ||
      typeof item.channel_id !== 'number' ||
      !Number.isSafeInteger(item.channel_id) ||
      item.channel_id <= 0 ||
      typeof item.plugin_id !== 'number' ||
      !Number.isSafeInteger(item.plugin_id) ||
      item.plugin_id <= 0 ||
      typeof item.model !== 'string' ||
      !exactModel(item.model) ||
      (channelId > 0 && item.channel_id !== channelId)
    )
      throw new Error('Request failed')
    return {
      id: item.id,
      channel_id: item.channel_id,
      model: item.model,
      plugin_id: item.plugin_id,
    }
  })
}

export async function putScriptBinding(
  binding: BindingInput,
  request: ApiRequestConfig
): Promise<void> {
  const response = await api.put(
    `${endpoint}/bindings`,
    binding,
    config(request)
  )
  data(response.data)
}
