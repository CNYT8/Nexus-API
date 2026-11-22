import { isCancelledError } from '@tanstack/react-query'
import { isCancel } from 'axios'
import i18next from 'i18next'
import { toast } from 'sonner'

export const safeServerErrorMessage = Symbol('safeServerErrorMessage')
const notifiedErrors = new WeakSet<object>()
type ErrorSource = Record<string | symbol, unknown>

function isRecord(value: unknown): value is ErrorSource {
  return value !== null && typeof value === 'object'
}

/** Visit error origins only; a safe message is a boundary hiding its cause. */
export function getServerErrorSources(value: unknown): ErrorSource[] {
  const sources: ErrorSource[] = []
  const pending = [value]
  const seen = new Set<object>()
  for (let index = 0; index < pending.length; index++) {
    const source = pending[index]
    if (!isRecord(source) || seen.has(source)) continue
    seen.add(source)
    sources.push(source)
    if (source[safeServerErrorMessage]) continue
    if (isRecord(source.response)) pending.push(source.response.data)
    pending.push(source.cause, source.error)
  }
  return sources
}

function messageText(value: unknown): string | undefined {
  if (typeof value !== 'string' || !value.trim() || value.trim() === 'success') {
    return undefined
  }
  if (/^\s*(?:<!doctype|<html[\s>])/i.test(value)) return undefined
  return value
}

export function translateServerText(
  t: (key: string, options?: Record<string, unknown>) => string,
  text: string
): string {
  return t(text, { skipInterpolation: true, keySeparator: false, nsSeparator: false })
}

/** Params are replacement data, never i18next options such as lng or ns. */
export function getServerMessage(value: unknown): string | undefined {
  if (!isRecord(value)) return messageText(value)
  if (value[safeServerErrorMessage]) return messageText(value.message)
  const key = messageText(value.message_key)
  if (key && i18next.exists(key, { keySeparator: false, nsSeparator: false })) {
    const params = isRecord(value.message_params) ? value.message_params : {}
    const result = i18next.t(key, {
      replace: params,
      count: typeof params.count === 'number' ? params.count : undefined,
      keySeparator: false,
      nsSeparator: false,
      returnObjects: false,
      interpolation: { escapeValue: false, skipOnVariables: true, nestingPrefix: '\u0000' },
    })
    if (typeof result === 'string') return messageText(result)
  }
  const message = messageText(value.message)
  if (message) return translateServerText(i18next.t.bind(i18next), message)
  const detail = messageText(value.error) || messageText(value.title)
  if (detail) return translateServerText(i18next.t.bind(i18next), detail)
  return key
}

export function getServerErrorStatus(value: unknown): number | undefined {
  for (const source of getServerErrorSources(value)) {
    const status = isRecord(source.response) ? source.response.status : source.status
    if (typeof status === 'number') return status
  }
  return undefined
}

export function isServerErrorCancelled(value: unknown): boolean {
  return getServerErrorSources(value).some((source) =>
    isCancel(source) || isCancelledError(source) ||
    source.code === 'ERR_CANCELED' || source.name === 'AbortError' ||
    source.name === 'CancelledError'
  )
}

export function getServerErrorMessage(value: unknown, fallback?: string): string {
  const sources = getServerErrorSources(value)
  // A masked error stays authoritative when wrapped by another error.
  const safe = sources.find((source) => source[safeServerErrorMessage])
  if (safe) return getServerMessage(safe) || fallback || i18next.t('Request failed')
  for (const source of sources) {
    if (isRecord(source.response)) {
      const message = getServerMessage(source.response.data)
      if (message) return message
    }
    // A cause or payload is more useful than Axios's generated HTTP message.
    if (!(source instanceof Error) && !source.isAxiosError && !isRecord(source.response)) {
      const message = getServerMessage(source)
      if (message) return message
    }
  }
  const status = getServerErrorStatus(value)
  if (status === 304) return i18next.t('Content not modified!')
  if (status === 204) return i18next.t('Content not found.')
  // Inspect the origin before a wrapper's generic message.
  for (const source of [...sources].reverse()) {
    const message = getServerMessage(source)
    if (message) return message
  }
  const text = messageText(value)
  return (text && translateServerText(i18next.t.bind(i18next), text)) || fallback || i18next.t('Request failed')
}

export function isServerErrorNotified(value: unknown): boolean {
  return getServerErrorSources(value).some((source) => notifiedErrors.has(source))
}

export function markServerErrorNotified(value: unknown): void {
  getServerErrorSources(value).forEach((source) => notifiedErrors.add(source))
}

export function notifyServerError(
  value: unknown,
  fallback?: string,
  presentation?: { title: string; description?: string }
): void {
  if (isServerErrorCancelled(value) || isServerErrorNotified(value)) return
  markServerErrorNotified(value)
  const message = presentation?.title || getServerErrorMessage(value, fallback)
  if (presentation?.description) {
    toast.error(message, { description: presentation.description })
  } else {
    toast.error(message)
  }
}

export function createServerError(value: unknown, fallback?: string): Error {
  const error = new Error(getServerErrorMessage(value, fallback), { cause: value })
  if (isServerErrorNotified(value)) notifiedErrors.add(error)
  return error
}

export function requireServerSuccess<T>(response: T): T {
  if (isRecord(response) && response.success === false) throw createServerError(response)
  return response
}
