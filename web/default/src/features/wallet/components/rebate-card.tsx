/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useCallback, useEffect, useState } from 'react'
import { Coins } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatQuota } from '@/lib/format'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ErrorState } from '@/components/error-state'
import {
  getUserRebate,
  getUserRebateRecords,
  type RebateOverview,
  type RebateRecord,
} from '../api'

const PAGE_SIZE = 20
const MICROS = 1_000_000

function money(value: number | undefined) {
  const amount = Number(value ?? 0) / MICROS
  return Number.isFinite(amount) ? amount.toFixed(2) : '-'
}

function percent(value: number | undefined) {
  const amount = Number(value ?? 0) / 100
  return Number.isFinite(amount) ? `${amount.toFixed(2)}%` : '-'
}

function displayDate(value: string | number | undefined) {
  if (value == null) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString()
}

const statusLabels = {
  pending: 'pending',
  held: 'held',
  paid: 'paid',
  discarded: 'discarded',
  voided: 'voided',
  failed: 'failed',
} as const
const payoutLabels = {
  immediate: 'Immediate',
  scheduled: 'Scheduled',
  half_day: 'Twice daily',
  custom: 'Custom interval',
} as const

export function RebateCard() {
  const { t } = useTranslation()
  const [overview, setOverview] = useState<RebateOverview | null>(null)
  const [records, setRecords] = useState<RebateRecord[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [recordsLoading, setRecordsLoading] = useState(false)
  const [error, setError] = useState(false)
  const [recordsError, setRecordsError] = useState(false)

  const loadOverview = useCallback(async () => {
    setLoading(true)
    setError(false)
    try {
      const response = await getUserRebate()
      if (
        !(response.success === true || response.message === 'success') ||
        !response.data
      )
        throw new Error(response.message)
      setOverview(response.data)
    } catch {
      setError(true)
    } finally {
      setLoading(false)
    }
  }, [])

  const loadRecords = useCallback(async (nextPage: number) => {
    setRecordsLoading(true)
    setRecordsError(false)
    try {
      const response = await getUserRebateRecords(nextPage, PAGE_SIZE)
      if (
        !(response.success === true || response.message === 'success') ||
        !response.data
      )
        throw new Error(response.message)
      setRecords(response.data.items ?? [])
      setTotal(Number(response.data.total) || 0)
      setPage(Number(response.data.page) || nextPage)
    } catch {
      setRecordsError(true)
    } finally {
      setRecordsLoading(false)
    }
  }, [])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadOverview()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [loadOverview])

  useEffect(() => {
    if (!overview?.enabled) return undefined
    const timer = window.setTimeout(() => {
      void loadRecords(page)
    }, 0)
    return () => window.clearTimeout(timer)
  }, [overview?.enabled, loadRecords, page])

  if (loading) {
    return <Skeleton className='h-56 rounded-xl' />
  }
  if (error) {
    return (
      <ErrorState
        title={t('Unable to load recharge rebate')}
        onRetry={loadOverview}
      />
    )
  }
  if (!overview?.enabled) return null

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const stats = [
    [t('Rebate Rate'), percent(overview.rate_basis_points)],
    [t('Pending Rebate'), formatQuota(overview.pending_quota)],
    [t('Paid Rebate'), formatQuota(overview.paid_quota)],
    [t('Paid Amount'), money(overview.paid_money_micros)],
  ]

  return (
    <Card className='bg-muted/20 py-0'>
      <CardHeader className='px-3 pt-4 sm:px-4'>
        <CardTitle className='flex items-center gap-2 text-sm font-semibold'>
          <Coins className='size-4' />
          {t('Recharge Rebate')}
        </CardTitle>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Only direct referrals generate recharge rebates. Rebate rewards do not generate further rebates.'
          )}
        </p>
      </CardHeader>
      <CardContent className='space-y-4 px-3 pb-4 sm:px-4'>
        <div className='grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4'>
          {stats.map(([label, value]) => (
            <div key={label} className='bg-background/60 rounded-lg border p-3'>
              <div className='text-muted-foreground text-xs'>{label}</div>
              <div className='mt-1 text-base font-semibold tabular-nums'>
                {value}
              </div>
            </div>
          ))}
        </div>
        <div className='text-muted-foreground grid gap-1 text-xs sm:grid-cols-2'>
          <span>
            {t('Invitee lifetime eligible spending cap')}:{' '}
            {Number(overview.invitee_eligible_money_limit_micros ?? 0) === 0
              ? t('Unlimited')
              : money(overview.invitee_eligible_money_limit_micros)}
          </span>
          <span>
            {t('Direct referrals eligible spending cap')}:{' '}
            {Number(overview.inviter_eligible_money_limit_micros ?? 0) === 0
              ? t('Unlimited')
              : money(overview.inviter_eligible_money_limit_micros)}
          </span>
          <span>
            {t('Current period eligible spending')}:{' '}
            {money(overview.inviter_period_eligible_money_micros)}
          </span>
          <span>
            {t('Payout schedule')}:{' '}
            {t(
              payoutLabels[overview.payout_mode as keyof typeof payoutLabels] ??
                overview.payout_mode
            )}
            {overview.payout_mode === 'custom'
              ? ` (${overview.payout_interval_hours}h)`
              : ''}
          </span>
        </div>
        {recordsError ? (
          <ErrorState
            className='min-h-32'
            title={t('Unable to load rebate records')}
            onRetry={() => loadRecords(page)}
          />
        ) : recordsLoading ? (
          <Skeleton className='h-36 rounded-lg' />
        ) : records.length === 0 ? (
          <div className='text-muted-foreground border-border/70 rounded-lg border border-dashed p-6 text-center text-sm'>
            {t('No recharge rebate records yet')}
          </div>
        ) : (
          <>
            <div className='overflow-x-auto rounded-lg border'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Created At')}</TableHead>
                    <TableHead>{t('Invitee')}</TableHead>
                    <TableHead>{t('Eligible spending')}</TableHead>
                    <TableHead>{t('Reward')}</TableHead>
                    <TableHead>{t('Available at')}</TableHead>
                    <TableHead>{t('Status')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {records.map((record) => (
                    <TableRow key={record.id}>
                      <TableCell>{displayDate(record.created_at)}</TableCell>
                      <TableCell>
                        {record.invitee_username || record.invitee_id || '-'}
                      </TableCell>
                      <TableCell>
                        {money(record.eligible_money_micros)}
                      </TableCell>
                      <TableCell>{money(record.reward_money_micros)}</TableCell>
                      <TableCell>{displayDate(record.available_at)}</TableCell>
                      <TableCell>
                        {t(
                          statusLabels[
                            record.status as keyof typeof statusLabels
                          ] ?? record.status
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            {totalPages > 1 && (
              <div className='flex items-center justify-end gap-2 text-xs'>
                <button
                  className='border-border rounded-md border px-2 py-1 disabled:opacity-50'
                  disabled={page <= 1 || recordsLoading}
                  onClick={() => setPage((value) => value - 1)}
                >
                  {t('Previous')}
                </button>
                <span className='text-muted-foreground'>
                  {page} / {totalPages}
                </span>
                <button
                  className='border-border rounded-md border px-2 py-1 disabled:opacity-50'
                  disabled={page >= totalPages || recordsLoading}
                  onClick={() => setPage((value) => value + 1)}
                >
                  {t('Next')}
                </button>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
