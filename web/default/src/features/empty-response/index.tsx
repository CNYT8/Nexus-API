import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { RefreshCcw, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { api } from '@/lib/api'
import { formatQuota } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

type EmptyResponseStatus = {
  enabled: boolean
  period_days: number
  refund_percent: number
  pending_count: number
  pending_quota: number
  refunded_count: number
}

type EmptyResponseRecord = {
  id: number
  request_id: string
  log_created_at: number
  model_name: string
  prompt_tokens: number
  quota: number
  refund_percent: number
  refund_quota: number
  status: 'pending' | 'refunded'
}

type EmptyResponseList = {
  items: EmptyResponseRecord[]
  total: number
}

function formatTimestamp(value: number): string {
  if (!value) return '-'
  return new Date(value * 1000).toLocaleString()
}

export function EmptyResponsePage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const statusQuery = useQuery({
    queryKey: ['empty-response-status'],
    queryFn: async () =>
      api.get<ApiResponse<EmptyResponseStatus>>('/api/empty-responses/self'),
  })
  const listQuery = useQuery({
    queryKey: ['empty-response-list'],
    queryFn: async () =>
      api.get<ApiResponse<EmptyResponseList>>('/api/empty-responses', {
        params: { p: 1, page_size: 50 },
      }),
  })

  const claimMutation = useMutation({
    mutationFn: async () =>
      api.post<ApiResponse<{ created: number; refund_quota: number }>>(
        '/api/empty-responses/claim'
      ),
    onSuccess: async (result) => {
      if (!result.data.success) {
        toast.error(result.data.message || t('Empty response claim failed'))
        return
      }
      toast.success(
        t('Claimed {{count}} empty responses for {{quota}}', {
          count: result.data.data?.created ?? 0,
          quota: formatQuota(result.data.data?.refund_quota ?? 0),
        })
      )
      await queryClient.invalidateQueries({ queryKey: ['empty-response-status'] })
      await queryClient.invalidateQueries({ queryKey: ['empty-response-list'] })
    },
    onError: () => toast.error(t('Empty response claim failed')),
  })

  const status = statusQuery.data?.data.data
  const records = listQuery.data?.data.data?.items ?? []
  const enabled = status?.enabled === true

  return (
    <div className='space-y-4 p-4 md:p-6'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div>
          <h1 className='text-2xl font-semibold tracking-tight'>
            {t('Empty Response Detection')}
          </h1>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'Records paid requests that received input but produced no output within the current rolling window.'
            )}
          </p>
        </div>
        <div className='flex gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={statusQuery.isFetching || listQuery.isFetching}
            onClick={() => {
              void statusQuery.refetch()
              void listQuery.refetch()
            }}
          >
            <RefreshCcw data-icon='inline-start' />
            {t('Refresh')}
          </Button>
          <Button
            type='button'
            disabled={!enabled || (status?.pending_count ?? 0) <= 0}
            onClick={() => claimMutation.mutate()}
          >
            <ShieldCheck data-icon='inline-start' />
            {t('Detect and claim compensation')}
          </Button>
        </div>
      </div>

      {!enabled && (
        <Card className='border-warning/50'>
          <CardContent className='py-4 text-sm'>
            {t(
              'Empty response compensation is currently disabled. Contact an administrator.'
            )}
          </CardContent>
        </Card>
      )}

      <div className='grid gap-4 md:grid-cols-3'>
        <Card>
          <CardHeader className='pb-2'>
            <CardDescription>{t('Current empty responses')}</CardDescription>
            <CardTitle>{status?.pending_count ?? 0}</CardTitle>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className='pb-2'>
            <CardDescription>{t('Estimated compensation')}</CardDescription>
            <CardTitle>{formatQuota(status?.pending_quota ?? 0)}</CardTitle>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className='pb-2'>
            <CardDescription>{t('Detection window')}</CardDescription>
            <CardTitle>
              {t('{{days}} days', { days: status?.period_days ?? 3 })}
            </CardTitle>
          </CardHeader>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t('Empty response records')}</CardTitle>
          <CardDescription>
            {t(
              'Compensation is calculated from the charged quota recorded at the time of each request.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Time')}</TableHead>
                <TableHead>{t('Model')}</TableHead>
                <TableHead className='text-right'>{t('Input Tokens')}</TableHead>
                <TableHead className='text-right'>{t('Original Charge')}</TableHead>
                <TableHead className='text-right'>{t('Rate')}</TableHead>
                <TableHead className='text-right'>{t('Compensation')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {records.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className='text-muted-foreground py-8 text-center'>
                    {t('No empty response records')}
                  </TableCell>
                </TableRow>
              ) : (
                records.map((record) => (
                  <TableRow key={record.id}>
                    <TableCell>{formatTimestamp(record.log_created_at)}</TableCell>
                    <TableCell>{record.model_name || '-'}</TableCell>
                    <TableCell className='text-right'>{record.prompt_tokens}</TableCell>
                    <TableCell className='text-right'>
                      {formatQuota(record.quota)}
                    </TableCell>
                    <TableCell className='text-right'>
                      {record.refund_percent}%
                    </TableCell>
                    <TableCell className='text-right'>
                      {formatQuota(record.refund_quota)}
                    </TableCell>
                    <TableCell>
                      <Badge variant={record.status === 'refunded' ? 'default' : 'secondary'}>
                        {record.status === 'refunded' ? t('Claimed') : t('Pending')}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  )
}
