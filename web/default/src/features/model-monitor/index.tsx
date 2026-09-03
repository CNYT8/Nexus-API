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
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, ChevronDown, ChevronRight, Database } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { SectionPageLayout } from '@/components/layout'
import { EmptyState } from '@/components/empty-state'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { getModelMonitorAvailability } from './api'
import type {
  ModelMonitorAvailabilityBucket,
  ModelMonitorStatus,
} from './types'

// Three availability colors driven by the scheduling-score thresholds:
// green >= 70, orange 45-70, red < 45, gray means no traffic at all.
const barClassName: Record<ModelMonitorStatus, string> = {
  excellent: 'bg-emerald-500 dark:bg-emerald-500',
  good: 'bg-emerald-500 dark:bg-emerald-500',
  unstable: 'bg-amber-500 dark:bg-amber-500',
  poor: 'bg-red-600 dark:bg-red-500',
  unknown: 'bg-muted-foreground/30 dark:bg-muted-foreground/30',
}

const barTextKey: Record<ModelMonitorStatus, string> = {
  excellent: 'Available',
  good: 'Available',
  unstable: 'Unstable',
  poor: 'Poor',
  unknown: 'No calls',
}

const legendItems: { className: string; key: string }[] = [
  { className: 'bg-emerald-500', key: 'Available' },
  { className: 'bg-amber-500', key: 'Unstable' },
  { className: 'bg-red-600', key: 'Poor' },
  { className: 'bg-muted-foreground/30', key: 'No calls' },
]

const inProgressStripe =
  'repeating-linear-gradient(45deg, rgba(255,255,255,0.25) 0 3px, transparent 3px 6px)'

function padTimePart(value: number): string {
  return String(value).padStart(2, '0')
}

function formatBucketTime(timestamp: number): string {
  if (!timestamp) return '--'
  const date = new Date(timestamp * 1000)
  return `${padTimePart(date.getMonth() + 1)}-${padTimePart(date.getDate())} ${padTimePart(date.getHours())}:${padTimePart(date.getMinutes())}`
}

function formatRefreshClock(timestamp: number): string {
  if (!timestamp) return '--:--'
  const date = new Date(timestamp)
  return `${padTimePart(date.getHours())}:${padTimePart(date.getMinutes())}`
}

function getBarStatus(bucket: ModelMonitorAvailabilityBucket): ModelMonitorStatus {
  return bucket.has_data ? bucket.status || 'unknown' : 'unknown'
}

function AvailabilityStrip(props: {
  buckets: ModelMonitorAvailabilityBucket[]
  windowEnd: number
  height?: number
}) {
  const { t } = useTranslation()
  const height = props.height ?? 26
  return (
    <div
      className='flex w-full items-stretch gap-[3px]'
      style={{ height }}
      role='img'
      aria-label={t(
        'Rolling 24-hour group availability in 2-hour segments, scored by the scheduling algorithm.'
      )}
    >
      {props.buckets.map((bucket, index) => {
        const status = getBarStatus(bucket)
        const isInProgress =
          index === props.buckets.length - 1 && bucket.end > props.windowEnd
        const displayEnd = isInProgress ? props.windowEnd : bucket.end
        return (
          <Tooltip key={bucket.start}>
            <TooltipTrigger
              render={
                <div
                  className={cn(
                    'h-full min-w-0 flex-1 cursor-default rounded-[3px]',
                    barClassName[status]
                  )}
                  style={{
                    backgroundImage:
                      isInProgress && bucket.has_data
                        ? inProgressStripe
                        : undefined,
                  }}
                />
              }
            />
            <TooltipContent>
              <div className='text-center'>
                <div>
                  {formatBucketTime(bucket.start)} –{' '}
                  {formatBucketTime(displayEnd)}
                </div>
                <div>
                  {t(barTextKey[status])}
                  {isInProgress ? ` · ${t('In progress')}` : ''}
                </div>
              </div>
            </TooltipContent>
          </Tooltip>
        )
      })}
    </div>
  )
}

function ModelRow(props: {
  model: { model_name: string; buckets: ModelMonitorAvailabilityBucket[] }
  windowEnd: number
}) {
  return (
    <div className='border-border flex flex-col gap-1.5 rounded-lg border p-3'>
      <span className='truncate font-mono text-[13px]'>
        {props.model.model_name}
      </span>
      <AvailabilityStrip
        buckets={props.model.buckets}
        windowEnd={props.windowEnd}
        height={16}
      />
    </div>
  )
}

function GroupBlock(props: {
  group: {
    group: string
    buckets: ModelMonitorAvailabilityBucket[]
    models: { model_name: string; buckets: ModelMonitorAvailabilityBucket[] }[]
  }
  windowEnd: number
  open: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  return (
    <div className='border-border border-b last:border-b-0'>
      <button
        type='button'
        className='bg-transparent flex w-full flex-col gap-2 py-3 text-left md:flex-row md:items-center md:gap-4'
        onClick={props.onToggle}
      >
        <div className='text-muted-foreground flex min-w-0 items-center gap-2 md:w-44 md:shrink-0'>
          {props.open ? (
            <ChevronDown className='size-4 shrink-0' />
          ) : (
            <ChevronRight className='size-4 shrink-0' />
          )}
          <span className='truncate font-semibold'>{props.group.group}</span>
        </div>
        <div className='min-w-0 flex-1'>
          <AvailabilityStrip
            buckets={props.group.buckets}
            windowEnd={props.windowEnd}
          />
        </div>
      </button>
      {props.open && (
        <div className='grid gap-1.5 pb-3 pl-6 md:grid-cols-2 md:gap-x-6 md:pl-14'>
          {props.group.models.map((model) => (
            <ModelRow
              key={model.model_name}
              model={model}
              windowEnd={props.windowEnd}
            />
          ))}
          {props.group.models.length === 0 && (
            <span className='text-muted-foreground px-1 py-2 text-sm'>
              {t('No models in this group')}
            </span>
          )}
        </div>
      )}
    </div>
  )
}

function LoadingSkeleton() {
  return (
    <div className='space-y-3'>
      <Card>
        <CardHeader>
          <Skeleton className='h-5 w-40' />
          <Skeleton className='h-4 w-72' />
        </CardHeader>
      </Card>
      {Array.from({ length: 4 }).map((_, index) => (
        <Card key={index}>
          <CardContent className='space-y-4 pt-0'>
            <div className='flex items-center gap-3'>
              <Skeleton className='size-4' />
              <Skeleton className='h-4 w-24' />
              <Skeleton className='h-6 flex-1' />
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

export function ModelMonitor() {
  const { t } = useTranslation()
  const monitorQuery = useQuery({
    queryKey: ['model-monitor-availability'],
    queryFn: getModelMonitorAvailability,
    staleTime: 60 * 1000,
    refetchInterval: 60 * 1000,
  })

  const availability = monitorQuery.data?.data
  const windowEnd = availability?.window_end ?? 0
  const [expandedGroups, setExpandedGroups] = useState(() => new Set<string>())
  const nextRefreshAt =
    monitorQuery.dataUpdatedAt && availability?.refresh_seconds
      ? monitorQuery.dataUpdatedAt + availability.refresh_seconds * 1000
      : Date.now() + 60 * 1000

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Model Monitor')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {monitorQuery.isLoading ? (
          <LoadingSkeleton />
        ) : !availability || availability.groups.length === 0 ? (
          <EmptyState
            icon={Database}
            title={t('No model monitor data')}
            description={t('Model scores will appear after recent requests.')}
            bordered
          />
        ) : (
          <div className='space-y-3 sm:space-y-4'>
            <Card>
              <CardHeader>
                <CardTitle className='flex items-center gap-2'>
                  <Activity className='text-muted-foreground size-4' />
                  {t('Group availability')}
                </CardTitle>
                <CardDescription>
                  {t(
                    'Rolling 24-hour group availability in 2-hour segments, scored by the scheduling algorithm.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className='flex flex-wrap items-center gap-x-4 gap-y-1.5 pt-0'>
                {legendItems.map((item) => (
                  <span
                    key={item.key}
                    className='flex items-center gap-1.5'
                  >
                    <span
                      className={cn(
                        'inline-block h-2.5 w-2.5 shrink-0 rounded-[3px]',
                        item.className
                      )}
                    />
                    <span className='text-muted-foreground text-xs'>
                      {t(item.key)}
                    </span>
                  </span>
                ))}
                <span className='text-muted-foreground ml-auto text-xs'>
                  {t('每1分钟动态更新数据')} ·{' '}
                  {t('下次更新时间:')} {formatRefreshClock(nextRefreshAt)}
                </span>
              </CardContent>
            </Card>

            <Card>
              <CardContent className='flex flex-col pt-0'>
                {availability.groups.map((group) => {
                  const key = String(group.group)
                  return (
                    <GroupBlock
                      key={key}
                      group={group}
                      windowEnd={windowEnd}
                      open={expandedGroups.has(key)}
                      onToggle={() =>
                        setExpandedGroups((prev) => {
                          const next = new Set(prev)
                          if (next.has(key)) {
                            next.delete(key)
                          } else {
                            next.add(key)
                          }
                          return next
                        })
                      }
                    />
                  )
                })}
              </CardContent>
            </Card>
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
