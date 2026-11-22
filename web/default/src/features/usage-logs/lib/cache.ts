import type { LogOtherData } from '../types'

function toTokenCount(value: unknown): number {
  const count = Number(value)
  return Number.isFinite(count) && count > 0 ? count : 0
}

export function getCacheWriteBreakdown(other?: LogOtherData | null) {
  const fiveMinutes = toTokenCount(other?.cache_creation_tokens_5m)
  const oneHour = toTokenCount(other?.cache_creation_tokens_1h)
  const twelveHours = toTokenCount(other?.cache_creation_tokens_12h)
  const splitTotal = fiveMinutes + oneHour + twelveHours
  // The aggregate already includes TTL buckets; only its remainder is ordinary.
  const ordinary = Math.max(
    toTokenCount(other?.cache_creation_tokens) - splitTotal,
    0
  )

  return {
    ordinary,
    fiveMinutes,
    oneHour,
    twelveHours,
    total: ordinary + splitTotal,
  }
}
