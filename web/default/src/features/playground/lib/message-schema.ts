import type { Message } from '../types'
import {
  createTextTrimmer,
  hasTextAttachment,
  isStoredImageUrl,
  type HistoryAdapter,
} from '../../../../../shared/playground-history-storage'

export const messageHistoryAdapter: HistoryAdapter<Message> = {
  hasAttachments: (message) => message.versions.some((version) =>
    Boolean(version.images?.length) || hasTextAttachment(version.content)),
  trimText: (message, limit) => {
    const trim = createTextTrimmer(limit)
    return {
      ...message,
      versions: message.versions.map((version) => ({ ...version, content: trim(version.content) })),
      ...(message.reasoning ? { reasoning: { ...message.reasoning, content: trim(message.reasoning.content) } } : {}),
    }
  },
  textLength: (message) => message.versions.reduce((size, version) => size + version.content.length, 0) +
    (message.reasoning?.content.length ?? 0),
}

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)

// Validate rows independently: one corrupt item must not discard valid history.
export function restoreMessages(value: unknown): Message[] {
  const rows = Array.isArray(value)
    ? value
    : record(value) && Array.isArray(value.messages)
      ? value.messages
      : []
  const keys = new Set<string>()
  return rows.flatMap((row, index): Message[] => {
    if (!record(row)) return []
    const role = row.from ?? row.role
    if (role !== 'user' && role !== 'assistant' && role !== 'system') return []
    const versions = (Array.isArray(row.versions) ? row.versions : [row])
      .filter(record)
      .filter((version) => typeof version.content === 'string')
      .map((version, i) => ({
        id: typeof version.id === 'string' ? version.id : `restored-${i}`,
        content: version.content as string,
        images: Array.isArray(version.images)
          ? version.images.filter(isStoredImageUrl)
          : [],
      }))
    if (!versions.length) return []
    let key = typeof row.key === 'string' && row.key ? row.key : `restored-${index}`
    while (keys.has(key)) key += '-duplicate'
    keys.add(key)
    const reasoning = record(row.reasoning) ? row.reasoning.content : row.reasoningContent
    return [{
      key,
      from: role,
      versions,
      status: row.status === 'error' ? 'error' : row.status === 'loading' || row.status === 'streaming' || row.status === 'incomplete' ? 'streaming' : 'complete',
      isReasoningStreaming: false,
      isReasoningComplete: true,
      isContentComplete: true,
      errorCode: typeof row.errorCode === 'string' ? row.errorCode : null,
      ...(typeof reasoning === 'string' && reasoning
        ? { reasoning: { content: reasoning, duration: 0 } }
        : {}),
      sources: Array.isArray(row.sources) ? row.sources.filter(record)
        .filter((source) => typeof source.href === 'string' && /^https?:\/\//i.test(source.href) && typeof source.title === 'string')
        .map((source) => ({ href: source.href as string, title: source.title as string })) : [],
    }]
  })
}
