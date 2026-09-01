import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { CircleDollarSign, FileWarning } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { api } from '@/lib/api'
import { formatQuota } from '@/lib/format'
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
  expired_count: number
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
  status: 'pending' | 'refunded' | 'expired'
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
    enabled: statusQuery.data?.data.data?.enabled === true,
    queryFn: async () => {
      const pageSize = 100
      const first = await api.get<ApiResponse<EmptyResponseList>>(
        '/api/empty-responses',
        { params: { p: 1, page_size: pageSize } }
      )
      const firstData = first.data.data
      if (!firstData || firstData.items.length >= firstData.total) {
        return first
      }
      const items = [...firstData.items]
      const pageCount = Math.ceil(firstData.total / pageSize)
      for (let page = 2; page <= pageCount; page += 1) {
        const next = await api.get<ApiResponse<EmptyResponseList>>(
          '/api/empty-responses',
          { params: { p: page, page_size: pageSize } }
        )
        items.push(...(next.data.data?.items ?? []))
      }
      return {
        ...first,
        data: {
          ...first.data,
          data: { items, total: firstData.total },
        },
      }
    },
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
      await queryClient.invalidateQueries({
        queryKey: ['empty-response-status'],
      })
      await queryClient.invalidateQueries({
        queryKey: ['empty-response-list'],
      })
    },
    onError: () => toast.error(t('Empty response claim failed')),
  })

  const status = statusQuery.data?.data.data
  const enabled = status?.enabled === true
  const records = enabled ? (listQuery.data?.data.data?.items ?? []) : []

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Empty Response Detection')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          type='button'
          disabled={!enabled || claimMutation.isPending}
          onClick={() => claimMutation.mutate()}
        >
          <CircleDollarSign data-icon='inline-start' />
          {t('Claim compensation')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='min-w-0 space-y-4'>
          {!enabled && (
            <Card className='border-warning/50'>
              <CardContent className='py-4 text-sm'>
                {t(
                  'Empty response compensation is currently disabled. Contact an administrator.'
                )}
              </CardContent>
            </Card>
          )}

          <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
            <Card>
              <CardHeader className='pb-2'>
                <CardDescription>
                  {t('Current empty responses')}
                </CardDescription>
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
                <CardDescription>{t('Expired records')}</CardDescription>
                <CardTitle>{status?.expired_count ?? 0}</CardTitle>
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

          <Card className='overflow-hidden'>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'>
                <FileWarning className='text-primary h-5 w-5' />
                {t('Empty response records')}
              </CardTitle>
              <CardDescription>
                {t(
                  'All detected records are kept here. Pending records can be claimed; older records remain visible as expired.'
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className='p-0 sm:p-6'>
              <div className='hidden overflow-x-auto sm:block'>
              <Table className='min-w-[720px]'>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Time')}</TableHead>
                    <TableHead>{t('Model')}</TableHead>
                    <TableHead className='text-right'>
                      {t('Input Tokens')}
                    </TableHead>
                    <TableHead className='text-right'>
                      {t('Original Charge')}
                    </TableHead>
                    <TableHead className='text-right'>{t('Rate')}</TableHead>
                    <TableHead className='text-right'>
                      {t('Compensation')}
                    </TableHead>
                    <TableHead>{t('Status')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {records.length === 0 ? (
                    <TableRow>
                      <TableCell
                        colSpan={7}
                        className='text-muted-foreground py-8 text-center'
                      >
                        {t('No empty response records')}
                      </TableCell>
                    </TableRow>
                  ) : (
                    records.map((record) => (
                      <TableRow key={record.id}>
                        <TableCell>
                          {formatTimestamp(record.log_created_at)}
                        </TableCell>
                        <TableCell>{record.model_name || '-'}</TableCell>
                        <TableCell className='text-right'>
                          {record.prompt_tokens}
                        </TableCell>
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
                          <Badge
                            variant={
                              record.status === 'refunded'
                                ? 'default'
                                : record.status === 'expired'
                                  ? 'outline'
                                  : 'secondary'
                            }
                          >
                            {record.status === 'refunded'
                              ? t('Claimed')
                              : record.status === 'expired'
                                ? t('Expired')
                                : t('Pending')}
                          </Badge>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
              </div>
              <div className='divide-border divide-y sm:hidden'>
                {records.length === 0 ? (
                  <div className='text-muted-foreground px-4 py-8 text-center text-sm'>
                    {t('No empty response records')}
                  </div>
                ) : (
                  records.map((record) => (
                    <div key={record.id} className='space-y-3 px-4 py-4'>
                      <div className='flex items-start justify-between gap-3'>
                        <div className='min-w-0'>
                          <p className='truncate font-medium'>{record.model_name || '-'}</p>
                          <p className='text-muted-foreground text-xs'>
                            {formatTimestamp(record.log_created_at)}
                          </p>
                        </div>
                        <Badge
                          variant={
                            record.status === 'refunded'
                              ? 'default'
                              : record.status === 'expired'
                                ? 'outline'
                                : 'secondary'
                          }
                        >
                          {record.status === 'refunded'
                            ? t('Claimed')
                            : record.status === 'expired'
                              ? t('Expired')
                              : t('Pending')}
                        </Badge>
                      </div>
                      <div className='grid grid-cols-2 gap-3 text-sm'>
                        <div>
                          <p className='text-muted-foreground text-xs'>{t('Input Tokens')}</p>
                          <p>{record.prompt_tokens}</p>
                        </div>
                        <div>
                          <p className='text-muted-foreground text-xs'>{t('Original Charge')}</p>
                          <p>{formatQuota(record.quota)}</p>
                        </div>
                        <div>
                          <p className='text-muted-foreground text-xs'>{t('Compensation')}</p>
                          <p>{formatQuota(record.refund_quota)}</p>
                        </div>
                        <div>
                          <p className='text-muted-foreground text-xs'>{t('Rate')}</p>
                          <p>{record.refund_percent}%</p>
                        </div>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
