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
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, HeartPulse, Timer } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { GroupBadge } from '@/components/group-badge'
import {
  getPerfMetrics,
  getPerfMetricsSummary,
} from '@/features/performance-metrics/api'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
} from '@/features/performance-metrics/lib/format'
import {
  groupWeight,
  pointWeight,
  weightedAverage,
  weightedMetric,
  weightedSummaryMetric,
} from '@/features/performance-metrics/lib/weighted'
import type {
  PerformanceGroup,
  PerfModelSummary,
} from '@/features/performance-metrics/types'
import { type UptimeDayPoint } from '../lib/mock-stats'
import type { PricingModel } from '../types'
import { LatencyTrendChart, UptimeTrendChart } from './model-details-charts'
import { UptimeSparkline } from './model-details-uptime-sparkline'

function StatCard(props: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: React.ReactNode
  hint?: string
  intent?: 'default' | 'warning' | 'success'
}) {
  const Icon = props.icon
  const intent = props.intent ?? 'default'
  return (
    <div className='bg-background flex flex-col gap-1 rounded-lg border p-3'>
      <span className='text-muted-foreground inline-flex items-center gap-1.5 text-[10px] font-medium tracking-wider uppercase'>
        <Icon className='size-3' />
        {props.label}
      </span>
      <span
        className={cn(
          'text-foreground font-mono text-lg font-semibold tabular-nums',
          intent === 'warning' && 'text-amber-600 dark:text-amber-400',
          intent === 'success' && 'text-emerald-600 dark:text-emerald-400'
        )}
      >
        {props.value}
      </span>
      {props.hint && (
        <span className='text-muted-foreground/70 text-[11px]'>
          {props.hint}
        </span>
      )}
    </div>
  )
}

type PerformanceRow = {
  group: string
  avg_ttft_ms: number
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
}

function toUptimePct(value: number): number {
  if (!Number.isFinite(value)) return 0
  const clamped = Math.min(100, Math.max(0, value))
  return Math.round(clamped * 100) / 100
}

function pointTtft(point: PerformanceGroup['series'][number]): number {
  const adjusted = Number(point.adjusted_avg_ttft_ms)
  return Number.isFinite(adjusted) && adjusted > 0
    ? adjusted
    : point.avg_ttft_ms
}

function toLatencySeries(groups: PerformanceGroup[]) {
  const byTs = new Map<number, { sum: number; weight: number }>()
  for (const group of groups) {
    for (const point of group.series) {
      const ttft = pointTtft(point)
      if (ttft <= 0) continue
      const weight = pointWeight(point)
      const current = byTs.get(point.ts) ?? { sum: 0, weight: 0 }
      current.sum += ttft * weight
      current.weight += weight
      byTs.set(point.ts, current)
    }
  }

  return Array.from(byTs.entries())
    .sort(([a], [b]) => a - b)
    .map(([ts, value]) => ({
      timestamp: new Date(ts * 1000).toISOString(),
      group: 'latency',
      ttft_ms:
        value.weight > 0 ? Math.round(value.sum / value.weight) : 0,
    }))
}

function toUptimeSeries(groups: PerformanceGroup[]): UptimeDayPoint[] {
  const byTs = new Map<
    number,
    { weightedRate: number; weight: number; incidents: number }
  >()
  for (const group of groups) {
    for (const point of group.series) {
      const current = byTs.get(point.ts) ?? {
        weightedRate: 0,
        weight: 0,
        incidents: 0,
      }
      if (Number.isFinite(point.success_rate)) {
        const successRate = toUptimePct(point.success_rate)
        const weight = pointWeight(point)
        current.weightedRate += successRate * weight
        current.weight += weight
        if (successRate < 100) current.incidents += 1
      }
      byTs.set(point.ts, current)
    }
  }
  return Array.from(byTs.entries())
    .sort(([a], [b]) => a - b)
    .map(([ts, value]) => {
      const uptime = value.weight > 0 ? value.weightedRate / value.weight : 0
      return {
        date: new Date(ts * 1000).toISOString(),
        uptime_pct: toUptimePct(uptime),
        incidents: value.incidents,
        outage_minutes: 0,
      }
    })
}

function toGroupUptimeSeries(group: PerformanceGroup): UptimeDayPoint[] {
  return group.series.map((point) => {
    const successRate = toUptimePct(point.success_rate)
    return {
      date: new Date(point.ts * 1000).toISOString(),
      uptime_pct: successRate,
      incidents: successRate < 100 ? 1 : 0,
      outage_minutes: 0,
    }
  })
}

function PerformanceDetailsSkeleton() {
  return (
    <div className='space-y-4' aria-hidden='true'>
      <div className='rounded-lg border p-3'>
        <div className='mb-3 flex items-center gap-2'>
          <div className='bg-muted h-3.5 w-3.5 rounded-full' />
          <div className='bg-muted h-3 w-36 rounded' />
        </div>
        <div className='space-y-2'>
          {Array.from({ length: 3 }).map((_, index) => (
            <div
              key={index}
              className='grid grid-cols-[96px_1fr_88px] items-center gap-3'
            >
              <div className='bg-muted h-6 rounded' />
              <div className='bg-muted h-2 rounded' />
              <div className='bg-muted h-4 rounded' />
            </div>
          ))}
        </div>
      </div>
      <div className='bg-muted/60 h-44 rounded-lg border' />
    </div>
  )
}

export function ModelDetailsPerformance(props: {
  model: PricingModel
  perfSummary?: PerfModelSummary
}) {
  const { t } = useTranslation()
  const metricsQuery = useQuery({
    queryKey: ['perf-metrics', props.model.model_name],
    queryFn: () => getPerfMetrics(props.model.model_name, 24),
    staleTime: 60 * 1000,
  })
  const summaryQuery = useQuery({
    queryKey: ['perf-metrics-summary', 24],
    queryFn: () => getPerfMetricsSummary(24),
    staleTime: 60 * 1000,
    retry: false,
    enabled: !props.perfSummary,
  })
  const groups = useMemo(
    () => metricsQuery.data?.data.groups ?? [],
    [metricsQuery.data]
  )
  const summary = useMemo(
    () =>
      props.perfSummary ??
      summaryQuery.data?.data.models.find(
        (model) => model.model_name === props.model.model_name
      ),
    [props.model.model_name, props.perfSummary, summaryQuery.data]
  )
  const performances = useMemo<PerformanceRow[]>(
    () =>
      groups.map((group) => ({
        group: group.group,
        avg_ttft_ms: weightedMetric(group, 'weighted_avg_ttft_ms'),
        avg_latency_ms: weightedMetric(group, 'weighted_avg_latency_ms'),
        success_rate: weightedMetric(group, 'weighted_success_rate'),
        avg_tps: weightedMetric(group, 'weighted_avg_tps'),
      })),
    [groups]
  )
  const latencySeries = useMemo(() => toLatencySeries(groups), [groups])
  const uptimeSeries = useMemo(() => toUptimeSeries(groups), [groups])
  const uptimeByGroup = useMemo<Record<string, UptimeDayPoint[]>>(() => {
    const map: Record<string, UptimeDayPoint[]> = {}
    for (const group of groups) {
      map[group.group] = toGroupUptimeSeries(group)
    }
    return map
  }, [groups])

  const hasDetailedData = performances.length > 0
  const hasSummaryData = Boolean(summary)

  if (!hasDetailedData && !hasSummaryData) {
    if (metricsQuery.isFetching || summaryQuery.isFetching) {
      return <PerformanceDetailsSkeleton />
    }
    return (
      <div className='text-muted-foreground rounded-lg border p-6 text-center text-sm'>
        {t('Performance data is not yet available for this model.')}
      </div>
    )
  }

  const avgTps = hasDetailedData
    ? weightedAverage(
        groups,
        (group) => weightedMetric(group, 'weighted_avg_tps'),
        groupWeight,
        (value) => value > 0
      )
    : weightedSummaryMetric(summary, 'weighted_avg_tps')
  const avgLatency = hasDetailedData
    ? Math.round(
        weightedAverage(
          groups,
          (group) => weightedMetric(group, 'weighted_avg_latency_ms'),
          groupWeight,
          (value) => value > 0
        )
      )
    : Math.round(weightedSummaryMetric(summary, 'weighted_avg_latency_ms'))
  const successRate = hasDetailedData
    ? weightedAverage(
        groups,
        (group) => weightedMetric(group, 'weighted_success_rate'),
        groupWeight,
        Number.isFinite
      )
    : weightedSummaryMetric(summary, 'weighted_success_rate')
  const incidentCount = uptimeSeries.reduce((s, p) => s + p.incidents, 0)
  let intent: 'default' | 'warning' | 'success' = 'warning'
  if (successRate >= 99.9) {
    intent = 'success'
  } else if (successRate >= 99) {
    intent = 'default'
  }

  const headerCellClass =
    'text-muted-foreground py-2 text-[10px] font-medium tracking-wider uppercase'

  return (
    <div className='flex flex-col gap-4'>
      <div className='grid grid-cols-1 gap-2 sm:grid-cols-3'>
        <StatCard
          icon={Timer}
          label='TPS'
          value={formatThroughput(avgTps)}
          hint={t('Sustained tokens per second')}
        />
        <StatCard
          icon={Timer}
          label={t('Average latency')}
          value={formatLatency(avgLatency)}
        />
        <StatCard
          icon={HeartPulse}
          label={t('Success rate')}
          value={formatUptimePct(successRate)}
          hint={
            hasDetailedData
              ? incidentCount > 0
                ? t('{{count}} incidents in the last 24 hours', {
                    count: incidentCount,
                  })
                : t('No incidents in the last 24 hours')
              : undefined
          }
          intent={intent}
        />
      </div>

      {hasDetailedData ? (
        <>
          <section>
            <SectionHeader
              icon={HeartPulse}
              title={t('Per-group performance')}
              description={t('Average latency, TTFT, TPS, and success rate')}
            />
            <div className='overflow-x-auto rounded-lg border'>
              <Table className='text-sm'>
                <TableHeader>
                  <TableRow className='hover:bg-transparent'>
                    <TableHead className={headerCellClass}>
                      {t('Group')}
                    </TableHead>
                    <TableHead className={`${headerCellClass} text-right`}>
                      TPS
                    </TableHead>
                    <TableHead className={`${headerCellClass} text-right`}>
                      {t('Average TTFT')}
                    </TableHead>
                    <TableHead className={`${headerCellClass} text-right`}>
                      {t('Average latency')}
                    </TableHead>
                    <TableHead
                      className={`${headerCellClass} min-w-[180px] text-left`}
                    >
                      {t('Success rate')}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {performances.map((perf) => (
                    <TableRow key={perf.group}>
                      <TableCell className='py-2.5'>
                        <GroupBadge group={perf.group} size='sm' />
                      </TableCell>
                      <TableCell className='py-2.5 text-right font-mono'>
                        {formatThroughput(perf.avg_tps)}
                      </TableCell>
                      <TableCell className='py-2.5 text-right font-mono'>
                        {formatLatency(perf.avg_ttft_ms)}
                      </TableCell>
                      <TableCell className='text-muted-foreground py-2.5 text-right font-mono'>
                        {formatLatency(perf.avg_latency_ms)}
                      </TableCell>
                      <TableCell className='py-2.5'>
                        <UptimeSparkline
                          size='sm'
                          series={uptimeByGroup[perf.group] ?? []}
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </section>

          <section>
            <SectionHeader
              icon={Timer}
              title={t('Latency trend (last 24h)')}
              description={t('Average TTFT')}
            />
            <LatencyTrendChart series={latencySeries} />
          </section>

          <section>
            <SectionHeader
              icon={HeartPulse}
              title={t('Availability (last 24h)')}
              description={
                incidentCount > 0
                  ? t(
                      'Request success rate; {{incidents}} incident buckets in the last 24 hours',
                      {
                        incidents: incidentCount,
                      }
                    )
                  : t('Request success rate sampled over the last 24 hours')
              }
              accent={
                incidentCount > 0 ? (
                  <span className='inline-flex items-center gap-1 text-amber-600 dark:text-amber-400'>
                    <AlertTriangle className='size-3.5' />
                    {t('{{count}} incidents', {
                      count: incidentCount,
                    })}
                  </span>
                ) : null
              }
            />
            <UptimeTrendChart series={uptimeSeries} />
          </section>
        </>
      ) : metricsQuery.isFetching ? (
        <PerformanceDetailsSkeleton />
      ) : (
        <div className='text-muted-foreground rounded-lg border p-6 text-center text-sm'>
          {t('Performance data is not yet available for this model.')}
        </div>
      )}
    </div>
  )
}

function SectionHeader(props: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  description?: string
  accent?: React.ReactNode
}) {
  const Icon = props.icon
  return (
    <div className='mb-2 flex flex-wrap items-center justify-between gap-2'>
      <div className='flex min-w-0 items-center gap-2'>
        <Icon className='text-muted-foreground/70 size-3.5 shrink-0' />
        <div className='min-w-0'>
          <div className='text-foreground text-sm font-semibold'>
            {props.title}
          </div>
          {props.description && (
            <p className='text-muted-foreground/80 text-xs'>
              {props.description}
            </p>
          )}
        </div>
      </div>
      {props.accent && (
        <div className='shrink-0 text-xs font-medium'>{props.accent}</div>
      )}
    </div>
  )
}
