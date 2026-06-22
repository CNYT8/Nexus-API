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
import { useQuery } from '@tanstack/react-query'
import { Activity, Database } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'
import { SectionPageLayout } from '@/components/layout'
import { EmptyState } from '@/components/empty-state'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { getModelMonitor } from './api'
import type { ModelMonitorStatus, ModelMonitorVendor } from './types'

const statusClassName: Record<ModelMonitorStatus, string> = {
  excellent:
    'border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  good: 'border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  unstable:
    'border-rose-400/20 bg-rose-400/10 text-rose-600 dark:text-rose-300',
  poor: 'border-red-600/20 bg-red-600/10 text-red-700 dark:text-red-300',
}

const barClassName: Record<ModelMonitorStatus, string> = {
  excellent: 'bg-emerald-500',
  good: 'bg-amber-500',
  unstable: 'bg-rose-400',
  poor: 'bg-red-600',
}

function getScoreStatus(score: number): ModelMonitorStatus {
  if (score >= 85) return 'excellent'
  if (score >= 70) return 'good'
  if (score >= 45) return 'unstable'
  return 'poor'
}

function ScoreBadge(props: { score: number }) {
  const status = getScoreStatus(props.score)
  return (
    <Badge
      variant='outline'
      className={cn('h-6 min-w-12 tabular-nums', statusClassName[status])}
    >
      {props.score}
    </Badge>
  )
}

function ScoreBar(props: { score: number }) {
  const status = getScoreStatus(props.score)
  return (
    <div
      className='bg-muted h-1.5 overflow-hidden rounded-full'
      aria-label='model monitor score'
    >
      <div
        className={cn('h-full rounded-full', barClassName[status])}
        style={{ width: `${props.score}%` }}
      />
    </div>
  )
}

function VendorIcon(props: { vendor: ModelMonitorVendor }) {
  if (props.vendor.icon) {
    return (
      <span className='bg-muted/40 flex size-9 shrink-0 items-center justify-center rounded-lg'>
        {getLobeIcon(props.vendor.icon, 24)}
      </span>
    )
  }

  return (
    <span className='bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg text-sm font-semibold'>
      {(props.vendor.name || '?').slice(0, 1).toUpperCase()}
    </span>
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
              <Skeleton className='size-9 rounded-lg' />
              <div className='flex-1 space-y-2'>
                <Skeleton className='h-4 w-36' />
                <Skeleton className='h-3 w-20' />
              </div>
              <Skeleton className='h-6 w-12 rounded-full' />
            </div>
            <Skeleton className='h-20 w-full' />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

export function ModelMonitor() {
  const { t } = useTranslation()
  const monitorQuery = useQuery({
    queryKey: ['model-monitor'],
    queryFn: getModelMonitor,
    staleTime: 60 * 1000,
  })

  const summary = monitorQuery.data?.data

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Model Monitor')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {monitorQuery.isLoading ? (
          <LoadingSkeleton />
        ) : !summary || summary.vendors.length === 0 ? (
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
                  {t('Model Monitor')}
                </CardTitle>
                <CardDescription>
                  {t(
                    'Global model experience scores from the last {{window}} days, with recent {{hot}} days weighted higher.',
                    {
                      window: summary.window_days,
                      hot: summary.hot_days,
                    }
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className='flex flex-wrap gap-2 pt-0'>
                <Badge variant='secondary'>
                  {t('Models')} {summary.model_count}
                </Badge>
                <Badge variant='secondary'>
                  {t('Vendors')} {summary.vendor_count}
                </Badge>
                <Badge variant='secondary'>
                  {t('Best Score')} {summary.best_score}
                </Badge>
              </CardContent>
            </Card>

            <Accordion className='gap-3'>
              {summary.vendors.map((vendor) => (
                <AccordionItem
                  key={vendor.name}
                  value={vendor.name}
                  className='rounded-xl border px-4'
                >
                  <AccordionTrigger className='py-3 hover:no-underline'>
                    <div className='flex min-w-0 flex-1 items-center gap-3 pr-3'>
                      <VendorIcon vendor={vendor} />
                      <div className='min-w-0 text-left'>
                        <div className='truncate font-semibold'>
                          {vendor.name || t('Unknown Vendor')}
                        </div>
                        <div className='text-muted-foreground text-xs'>
                          {t('Models')} {vendor.models.length}
                        </div>
                      </div>
                    </div>
                    <ScoreBadge score={vendor.score} />
                  </AccordionTrigger>
                  <AccordionContent className='pb-4'>
                    <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'>
                      {vendor.models.map((model) => (
                        <div
                          key={model.model_name}
                          className='rounded-lg border p-3'
                        >
                          <div className='flex items-start justify-between gap-3'>
                            <div className='min-w-0'>
                              <div className='truncate font-mono text-sm font-semibold'>
                                {model.model_name}
                              </div>
                              <div className='text-muted-foreground mt-0.5 text-xs'>
                                {t('Score')} {model.score}
                              </div>
                            </div>
                            <ScoreBadge score={model.score} />
                          </div>
                          <div className='mt-3'>
                            <ScoreBar score={model.score} />
                          </div>
                        </div>
                      ))}
                    </div>
                  </AccordionContent>
                </AccordionItem>
              ))}
            </Accordion>
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
