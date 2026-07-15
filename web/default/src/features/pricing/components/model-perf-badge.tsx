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
import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

export type ModelPerfBadgeData = {
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
  weighted_avg_latency_ms?: number
  weighted_success_rate?: number
  weighted_avg_tps?: number
}

export interface ModelPerfBadgeProps extends React.HTMLAttributes<HTMLDivElement> {
  perf: ModelPerfBadgeData | undefined
}

function formatCompactNumber(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '—'
  return value > 1 ? String(Math.round(value)) : value.toFixed(1)
}

function formatCompactLatency(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—'
  if (ms >= 1_000) return `${formatCompactNumber(ms / 1_000)}s`
  return `${formatCompactNumber(ms)}ms`
}

function formatCompactThroughput(tps: number): string {
  if (!Number.isFinite(tps) || tps <= 0) return '—'
  if (tps >= 1_000) return `${formatCompactNumber(tps / 1_000)}Ktps`
  return `${formatCompactNumber(tps)}tps`
}

function pickWeightedValue(
  source: ModelPerfBadgeData,
  weightedKey: keyof Pick<
    ModelPerfBadgeData,
    'weighted_avg_latency_ms' | 'weighted_success_rate' | 'weighted_avg_tps'
  >,
  fallbackKey: keyof Pick<
    ModelPerfBadgeData,
    'avg_latency_ms' | 'success_rate' | 'avg_tps'
  >
): number {
  const weightedValue = Number(source[weightedKey])
  if (Number.isFinite(weightedValue)) return weightedValue
  return Number(source[fallbackKey])
}

export const ModelPerfBadge = memo(function ModelPerfBadge(
  props: ModelPerfBadgeProps
) {
  const { t } = useTranslation()

  if (!props.perf) {
    return null
  }

  const avg_latency_ms = pickWeightedValue(
    props.perf,
    'weighted_avg_latency_ms',
    'avg_latency_ms'
  )
  const avg_tps = pickWeightedValue(
    props.perf,
    'weighted_avg_tps',
    'avg_tps'
  )
  const success_rate = pickWeightedValue(
    props.perf,
    'weighted_success_rate',
    'success_rate'
  )

  let statusColor = 'bg-emerald-500'
  if (success_rate < 99) {
    statusColor = 'bg-red-500'
  } else if (success_rate < 99.9) {
    statusColor = 'bg-amber-500'
  }

  return (
    <div
      className={cn(
        'hidden w-[132px] grid-cols-[38px_48px_30px] gap-x-2 text-right tabular-nums min-[460px]:grid',
        props.className
      )}
    >
      <div title={t('Average latency')} className='min-w-0'>
        <div className='text-muted-foreground/55 text-[10px] leading-4'>
          {t('Latency short')}
        </div>
        <div className='text-muted-foreground/80 font-mono text-xs leading-4 whitespace-nowrap'>
          {formatCompactLatency(avg_latency_ms)}
        </div>
      </div>
      <div title={t('Throughput')} className='min-w-0'>
        <div className='text-muted-foreground/55 truncate text-[10px] leading-4'>
          {t('Throughput short')}
        </div>
        <div className='text-muted-foreground/80 font-mono text-xs leading-4 whitespace-nowrap'>
          {formatCompactThroughput(avg_tps)}
        </div>
      </div>
      <div
        title={`${t('Success rate')}: ${success_rate.toFixed(1)}%`}
        className='min-w-0'
      >
        <div className='text-muted-foreground/55 truncate text-[10px] leading-4'>
          {t('Status short')}
        </div>
        <div className='flex h-4 items-center justify-end gap-0.5'>
          <span className='bg-muted-foreground/10 h-2 w-1 rounded-full' />
          <span className='bg-muted-foreground/15 h-2.5 w-1 rounded-full' />
          <span className={cn('h-3 w-1 rounded-full', statusColor)} />
        </div>
      </div>
    </div>
  )
})
