// Keep validation aligned with the classic theme's task-public-address.js.
export function isValidTaskPublicAddress(value: string): boolean {
  if (typeof value !== 'string') return false
  if (value === '') return true
  if (value !== value.trim() || !/^https?:\/\//i.test(value)) return false
  if (/[\s\\?#]/u.test(value)) return false
  for (const character of value) {
    const code = character.codePointAt(0) ?? 0
    if (code <= 0x1f || code === 0x7f) return false
  }
  try {
    const url = new URL(value)
    const authority = value.slice(value.indexOf('://') + 3).split('/')[0]
    return Boolean(url.hostname) && !authority.includes('@') &&
      !url.username && !url.password && !url.search && !url.hash
  } catch {
    return false
  }
}
