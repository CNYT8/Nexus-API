// Limits apply to persisted snapshots, never to the live conversation/editor.
export const MAX_STORED_MESSAGES = 100
export const MAX_STORED_MESSAGES_BYTES = 1024 * 1024
export const MAX_STORED_TEXT_CHARS = 120_000
export const MAX_MESSAGE_TEXT_CHARS = 40_000
// Text/image attachments are atomic. One supported 5 MiB image needs about
// 6.7 MiB as base64; browser quota may still be lower than this application cap.
export const MAX_ATTACHMENT_HISTORY_BYTES = 8 * 1024 * 1024

export type HistoryAdapter<T> = {
  hasAttachments: (message: T) => boolean
  trimText: (message: T, limit: number) => T
  textLength: (message: T) => number
}

const encoder = new TextEncoder()
const byteLength = (value: unknown) => encoder.encode(JSON.stringify(value)).length

export function isStoredImageUrl(value: unknown): value is string {
  return typeof value === 'string' && (
    /^https?:\/\//i.test(value) ||
    /^data:image\/(?:png|jpe?g|webp|gif|bmp);base64,[a-z\d+/=\s]+$/i.test(value)
  )
}

export function hasTextAttachment(text: string): boolean {
  // Preserve even an old/incomplete attachment block; never cut its payload.
  return /(?:^|\n)--- 附件: /.test(text)
}

// One budget across every version, reasoning and text part of a message.
export function createTextTrimmer(limit: number): (text: string) => string {
  let remaining = limit
  return (text) => {
    let end = Math.min(text.length, remaining)
    if (end < text.length && end > 0 && /[\uD800-\uDBFF]/.test(text[end - 1])) end--
    const result = text.slice(0, end)
    remaining -= result.length
    return result
  }
}

function measureHistory<T>(messages: T[], adapter: HistoryAdapter<T>) {
  const entries = messages.map((message) => {
    const attached = adapter.hasAttachments(message)
    return { message, attached, bytes: byteLength(message), chars: attached ? 0 : adapter.textLength(message) }
  })
  let count = entries.length
  let plainCount = entries.filter((entry) => !entry.attached).length
  let bytes = entries.reduce((total, entry) => total + entry.bytes, 0)
  let plainBytes = entries.reduce((total, entry) => total + (entry.attached ? 0 : entry.bytes), 0)
  let chars = entries.reduce((total, entry) => total + entry.chars, 0)
  return {
    fits: () => chars <= MAX_STORED_TEXT_CHARS &&
      plainBytes + Math.max(0, plainCount - 1) + 2 <= MAX_STORED_MESSAGES_BYTES &&
      bytes + Math.max(0, count - 1) + 2 <=
        (plainCount === count ? MAX_STORED_MESSAGES_BYTES : MAX_ATTACHMENT_HISTORY_BYTES),
    remove: (index: number) => {
      const entry = entries[index]
      count--
      bytes -= entry.bytes
      chars -= entry.chars
      if (!entry.attached) {
        plainCount--
        plainBytes -= entry.bytes
      }
    },
  }
}

export function trimHistory<T>(messages: T[], adapter: HistoryAdapter<T>): T[] {
  const selected = messages.slice(-MAX_STORED_MESSAGES).map((message) =>
    adapter.hasAttachments(message) ? message : adapter.trimText(message, MAX_MESSAGE_TEXT_CHARS))
  const size = measureHistory(selected, adapter)
  let start = 0
  while (!size.fits() && start < selected.length - 1) {
    // Count retention expires whole old messages. Inside that retained window,
    // never discard an attachment to satisfy a byte/text limit. An oversized
    // legacy snapshot remains recoverable; writeHistory will decline the write.
    if (adapter.hasAttachments(selected[start])) break
    size.remove(start++)
  }
  return selected.slice(start)
}

export function writeHistory<T>(
  storage: Pick<Storage, 'setItem'>,
  key: string,
  messages: T[],
  adapter: HistoryAdapter<T>
): boolean {
  const selected = trimHistory(messages, adapter)
  if (!measureHistory(selected, adapter).fits()) return false
  while (true) {
    const serialized = JSON.stringify(selected)
    try {
      // setItem is atomic: never remove the previous snapshot before success.
      storage.setItem(key, serialized)
      return true
    } catch (error) {
      const name = error && typeof error === 'object' && 'name' in error ? error.name : ''
      if ((name !== 'QuotaExceededError' && name !== 'NS_ERROR_DOM_QUOTA_REACHED') ||
        selected.length <= 1 || adapter.hasAttachments(selected[0])) return false
      // Quota is shared with the other UI/accounts. Only evict our own oldest
      // ordinary message; leave the previous snapshot intact if an attachment
      // cannot fit, and let the caller retain its dirty flag for a later retry.
      selected.shift()
    }
  }
}
