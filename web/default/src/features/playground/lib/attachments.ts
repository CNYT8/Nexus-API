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

/**
 * Playground attachments (client-side only, never uploaded to the server).
 *
 * Why client-side:
 * - Text files are decoded with FileReader and inlined into the message body,
 *   so the upstream model really receives the file content.
 * - Images are converted to data URLs and sent through the existing
 *   OpenAI-compatible `image_url` content part, which the relay already
 *   supports natively for `data:image/...;base64,`.
 * - Nothing is stored server-side, so there is no storage cost and no upload
 *   surface for malicious files. Files never leave the browser.
 */

/** Text extensions the UI accepts. */
export const TEXT_FILE_EXTENSIONS = [
  '.txt',
  '.md',
  '.markdown',
  '.csv',
  '.json',
  '.log',
]

/** Text attachment limit (bytes). */
export const MAX_TEXT_FILE_BYTES = 2 * 1024 * 1024 // 2MB

/** Image attachment limit (bytes). base64 inflates ~4/3, so keep it bounded. */
export const MAX_IMAGE_FILE_BYTES = 5 * 1024 * 1024 // 5MB

const ALLOWED_IMAGE_MIMES = new Set([
  'image/png',
  'image/jpeg',
  'image/jpg',
  'image/webp',
  'image/gif',
  'image/bmp',
])

const ALLOWED_TEXT_MIMES = new Set([
  'text/plain',
  'text/markdown',
  'text/x-markdown',
  'text/csv',
  'application/json',
  'text/json',
  'application/octet-stream', // some systems report this for .md/.log
  '',
])

export const TEXT_FILE_ACCEPT = TEXT_FILE_EXTENSIONS.join(',')
export const IMAGE_FILE_ACCEPT =
  'image/png,image/jpeg,image/webp,image/gif,image/bmp'

function getExtension(fileName: string): string {
  const name = typeof fileName === 'string' ? fileName : ''
  const dotIndex = name.lastIndexOf('.')
  if (dotIndex <= 0) return ''
  return name.slice(dotIndex).toLowerCase()
}

export function formatFileSize(bytes: number): string {
  const size = Number(bytes)
  if (!Number.isFinite(size) || size <= 0) return '0 B'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(2)} MB`
}

export function isAllowedTextFile(file: File | null | undefined): boolean {
  if (!file) return false
  const ext = getExtension(file.name)
  if (!TEXT_FILE_EXTENSIONS.includes(ext)) return false

  const mime = String(file.type || '').toLowerCase()
  return ALLOWED_TEXT_MIMES.has(mime)
}

export function isAllowedImageFile(file: File | null | undefined): boolean {
  if (!file) return false
  const mime = String(file.type || '').toLowerCase()
  if (!mime.startsWith('image/')) return false
  return ALLOWED_IMAGE_MIMES.has(mime)
}

function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsText(file, 'utf-8')
  })
}

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsDataURL(file)
  })
}

export type TextAttachmentResult =
  | { ok: true; name: string; size: number; text: string }
  | {
      ok: false
      name: string
      reason: 'unsupported_type' | 'too_large' | 'read_failed'
    }

export type ImageAttachmentResult =
  | { ok: true; name: string; size: number; dataUrl: string }
  | {
      ok: false
      name: string
      reason: 'unsupported_type' | 'too_large' | 'read_failed'
    }

export async function parseTextAttachment(
  file: File
): Promise<TextAttachmentResult> {
  const name = file?.name || ''
  if (!isAllowedTextFile(file)) {
    return { ok: false, name, reason: 'unsupported_type' }
  }
  if (file.size > MAX_TEXT_FILE_BYTES) {
    return { ok: false, name, reason: 'too_large' }
  }

  try {
    const text = await readFileAsText(file)
    return { ok: true, name, size: file.size, text }
  } catch {
    return { ok: false, name, reason: 'read_failed' }
  }
}

export async function parseImageAttachment(
  file: File
): Promise<ImageAttachmentResult> {
  const name = file?.name || ''
  if (!isAllowedImageFile(file)) {
    return { ok: false, name, reason: 'unsupported_type' }
  }
  if (file.size > MAX_IMAGE_FILE_BYTES) {
    return { ok: false, name, reason: 'too_large' }
  }

  try {
    const dataUrl = await readFileAsDataURL(file)
    if (!dataUrl.startsWith('data:image/')) {
      return { ok: false, name, reason: 'unsupported_type' }
    }
    return { ok: true, name, size: file.size, dataUrl }
  } catch {
    return { ok: false, name, reason: 'read_failed' }
  }
}

/**
 * Merge text attachments into a single block that is prepended to the prompt,
 * so the model can tell the user question apart from file contents.
 */
export function buildAttachmentTextBlock(
  attachments: Array<{ name: string; size: number; text: string }>
): string {
  const list = Array.isArray(attachments) ? attachments : []
  const valid = list.filter((item) => item && typeof item.text === 'string')
  if (valid.length === 0) return ''

  return valid
    .map(
      (item) =>
        `--- 附件: ${item.name} (${formatFileSize(item.size)}) ---\n${item.text}\n--- 附件结束: ${item.name} ---`
    )
    .join('\n\n')
}

/**
 * Strip inline base64 image data before writing messages to localStorage.
 * A single inline image can be several MB and would blow the storage quota,
 * which previously caused the whole history to silently fail to save.
 */
export function stripInlineImageDataForStorage<T extends { content: unknown }>(
  messages: T[]
): T[] {
  if (!Array.isArray(messages)) return messages

  return messages.map((message) => {
    const content = message?.content
    if (!Array.isArray(content)) return message

    const nextContent = content.map((part: unknown) => {
      const candidate = part as {
        type?: string
        image_url?: { url?: string }
      }
      if (
        candidate &&
        candidate.type === 'image_url' &&
        candidate.image_url &&
        typeof candidate.image_url.url === 'string' &&
        candidate.image_url.url.startsWith('data:')
      ) {
        return {
          ...candidate,
          image_url: { url: '[inline-image-omitted]' },
        }
      }
      return part
    })

    return { ...message, content: nextContent }
  })
}
