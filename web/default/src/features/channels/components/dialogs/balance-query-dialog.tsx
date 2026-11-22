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
import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, RefreshCw, DollarSign } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { getCodexUsage, updateChannelBalance } from '../../api'
import { channelsQueryKeys } from '../../lib'
import { useChannels } from '../channels-provider'
import {
  CodexUsageDialog,
  type CodexUsageDialogData,
} from './codex-usage-dialog'

type BalanceQueryDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function BalanceQueryDialog({
  open,
  onOpenChange,
}: BalanceQueryDialogProps) {
  const { t } = useTranslation()
  const { currentRow, setCurrentRow } = useChannels()
  const queryClient = useQueryClient()
  const [isQuerying, setIsQuerying] = useState(false)
  const [balance, setBalance] = useState<number | null>(null)
  const [balanceUpdatedTime, setBalanceUpdatedTime] = useState<number | null>(
    null
  )
  const [codexUsageResponse, setCodexUsageResponse] =
    useState<CodexUsageDialogData | null>(null)

  const [rawResponse, setRawResponse] = useState<string | null>(null)
  const requestId = useRef(0)
  const scope = useRef({ open, id: currentRow?.id })
  scope.current = { open, id: currentRow?.id }
  const isCurrent = (request: number, id: number) =>
    request === requestId.current && scope.current.open && scope.current.id === id

  useEffect(() => {
    requestId.current++
    setBalance(null)
    setBalanceUpdatedTime(null)
    setRawResponse(null)
    setCodexUsageResponse(null)
    setIsQuerying(false)
    return () => { requestId.current++ }
  }, [open, currentRow?.id])

  const isCodex = currentRow?.type === 57

  const handleQueryCodexUsage = async () => {
    const row = currentRow
    if (!row || !open) return
    const request = ++requestId.current
    setIsQuerying(true)
    try {
      const res = await getCodexUsage(row.id)
      if (!isCurrent(request, row.id)) return
      if (!res.success) {
        throw new Error(res.message || t('Failed to fetch usage'))
      }
      setCodexUsageResponse(res)
    } catch (error: unknown) {
      if (isCurrent(request, row.id)) toast.error(
        error instanceof Error ? error.message : t('Failed to fetch usage')
      )
    } finally {
      if (isCurrent(request, row.id)) setIsQuerying(false)
    }
  }

  useEffect(() => {
    if (!isCodex) return
    if (!open) return
    handleQueryCodexUsage()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, isCodex, currentRow?.id])

  if (!currentRow) return null

  const handleQueryBalance = async () => {
    if (!open || isQuerying) return
    const request = ++requestId.current
    setIsQuerying(true)
    setRawResponse(null)
    try {
      const response = await updateChannelBalance(currentRow.id)
      if (!isCurrent(request, currentRow.id)) return
      if (response.success && typeof response.balance === 'number' && Number.isFinite(response.balance)) {
        const newBalance = response.balance
        const now = Math.floor(Date.now() / 1000)

        setBalance(newBalance)
        setBalanceUpdatedTime(now)
        toast.success(t('Balance updated successfully'))

        // Update currentRow immediately with new balance and timestamp
        setCurrentRow({
          ...currentRow,
          balance: newBalance,
          balance_updated_time: now,
        })

        // Invalidate queries to refresh the table
        await queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
      } else if (response.success && Object.prototype.hasOwnProperty.call(response, 'raw_response')) {
        setRawResponse(JSON.stringify(response.raw_response, null, 2) ?? 'null')
      } else {
        toast.error(response.message || t('余额查询未返回有效余额或原始响应'))
      }
    } catch {
      if (isCurrent(request, currentRow.id)) toast.error(t('余额查询失败，请稍后重试'))
    } finally {
      if (isCurrent(request, currentRow.id)) setIsQuerying(false)
    }
  }

  const handleClose = () => {
    requestId.current++
    setRawResponse(null)
    setIsQuerying(false)
    setBalance(null)
    setBalanceUpdatedTime(null)
    setCodexUsageResponse(null)
    onOpenChange(false)
  }

  const formatBalance = (bal: number) =>
    formatCurrencyFromUSD(bal, {
      digitsLarge: 2,
      digitsSmall: 4,
      abbreviate: false,
    })

  const formatDate = (timestamp: number) => {
    if (!timestamp) return 'Never'
    return formatTimestampToDate(timestamp)
  }

  if (isCodex) {
    return (
      <CodexUsageDialog
        open={open}
        onOpenChange={(v) => {
          if (!v) handleClose()
        }}
        channelName={currentRow.name}
        channelId={currentRow.id}
        response={codexUsageResponse}
        onRefresh={handleQueryCodexUsage}
        isRefreshing={isQuerying}
      />
    )
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('Query Balance')}</DialogTitle>
          <DialogDescription>
            {t('Update balance for:')} <strong>{currentRow.name}</strong>
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-4 py-4'>
          {/* Current Balance Display */}
          <div className='bg-muted/50 rounded-lg border p-4'>
            <div className='text-muted-foreground mb-2 flex items-center gap-2 text-sm'>
              <DollarSign className='h-4 w-4' />
              <span>{t('Current Balance')}</span>
            </div>
            <div className='text-2xl font-bold'>
              {balance !== null
                ? formatBalance(balance)
                : formatBalance(currentRow.balance)}
            </div>
            <div className='text-muted-foreground mt-2 text-xs'>
              {t('Last updated:')}{' '}
              {formatDate(
                balanceUpdatedTime ?? currentRow.balance_updated_time
              )}
            </div>
          </div>

          {rawResponse !== null && (
            <section className='min-w-0 space-y-2' aria-live='polite'>
              <h3 className='text-sm font-medium'>{t('余额查询原始响应')}</h3>
              <p className='text-muted-foreground text-xs'>{t('接口未返回可用余额，以下为原始响应；余额和更新时间未修改')}</p>
              <pre className='bg-muted max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-md p-3 text-xs'>{rawResponse}</pre>
            </section>
          )}

          {/* Balance Update Button */}
          <Button
            className='w-full'
            onClick={handleQueryBalance}
            disabled={isQuerying}
          >
            {isQuerying && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {!isQuerying && <RefreshCw className='mr-2 h-4 w-4' />}
            {isQuerying ? t('Querying...') : t('Update Balance')}
          </Button>
        </div>

        <DialogFooter>
          <Button variant='outline' onClick={handleClose} disabled={isQuerying}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
