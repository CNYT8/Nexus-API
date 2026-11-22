const MAX_MICROS = '9223372036854775807'

// Accept at most six decimal places; reject excess precision, never round.
export function moneyToMicros(value: string): string | null {
  const match = /^(\d+)(?:\.(\d{0,6}))?$/.exec(value.trim())
  if (!match) return null
  const micros = `${match[1]}${(match[2] ?? '').padEnd(6, '0')}`.replace(
    /^0+(?=\d)/,
    ''
  )
  if (
    micros.length > MAX_MICROS.length ||
    (micros.length === MAX_MICROS.length && micros > MAX_MICROS)
  ) {
    return null
  }
  return micros
}

export function microsToMoney(value: string): string {
  const digits = value.replace(/^0+(?=\d)/, '').padStart(7, '0')
  const fraction = digits.slice(-6).replace(/0+$/, '')
  return `${digits.slice(0, -6)}${fraction ? `.${fraction}` : ''}`
}
