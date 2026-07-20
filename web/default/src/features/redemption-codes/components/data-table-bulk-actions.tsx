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
import { useState, useMemo } from 'react'
import { type Table } from '@tanstack/react-table'
import { Eraser, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { batchDeleteRedemptions, deleteInvalidRedemptions } from '../api'
import { type Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

type DataTableBulkActionsProps<TData> = {
  table: Table<TData>
}

export function DataTableBulkActions<TData>({
  table,
}: DataTableBulkActionsProps<TData>) {
  const { t } = useTranslation()
  const { triggerRefresh } = useRedemptions()
  const [showDeleteSelectedConfirm, setShowDeleteSelectedConfirm] =
    useState(false)
  const [showDeleteInvalidConfirm, setShowDeleteInvalidConfirm] =
    useState(false)
  const [isDeletingSelected, setIsDeletingSelected] = useState(false)
  const [isDeletingInvalid, setIsDeletingInvalid] = useState(false)
  const selectedRows = table.getFilteredSelectedRowModel().rows

  const contentToCopy = useMemo(() => {
    const selectedCodes = selectedRows.map((row) => {
      const redemption = row.original as Redemption
      return `${redemption.name}\t${redemption.key}`
    })
    return selectedCodes.join('\n')
  }, [selectedRows])

  const handleDeleteSelected = async () => {
    const ids = selectedRows.map((row) => (row.original as Redemption).id)
    if (ids.length === 0) return

    setIsDeletingSelected(true)
    try {
      const result = await batchDeleteRedemptions(ids)
      if (result.success) {
        toast.success(
          t('Successfully deleted {{count}} redemption code(s)', {
            count: result.data ?? ids.length,
          })
        )
        table.resetRowSelection()
        triggerRefresh()
        setShowDeleteSelectedConfirm(false)
      } else {
        toast.error(result.message || t('Batch delete failed'))
      }
    } catch {
      toast.error(t('Batch delete failed'))
    } finally {
      setIsDeletingSelected(false)
    }
  }

  const handleDeleteInvalid = async () => {
    setIsDeletingInvalid(true)
    try {
      const result = await deleteInvalidRedemptions()

      if (result.success) {
        const count = result.data || 0
        toast.success(
          t('Successfully deleted {{count}} invalid redemption codes', {
            count,
          })
        )
        table.resetRowSelection()
        triggerRefresh()
        setShowDeleteInvalidConfirm(false)
      } else {
        toast.error(result.message || t('Batch delete failed'))
      }
    } catch {
      toast.error(t('Batch delete failed'))
    } finally {
      setIsDeletingInvalid(false)
    }
  }

  return (
    <>
      <BulkActionsToolbar table={table} entityName={t('redemption code')}>
        <CopyButton
          value={contentToCopy}
          variant='outline'
          size='icon'
          className='size-8'
          tooltip={t('Copy selected codes')}
          successTooltip={t('Codes copied!')}
          aria-label={t('Copy selected codes')}
        />

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='destructive'
                size='icon'
                onClick={() => setShowDeleteSelectedConfirm(true)}
                className='size-8'
                aria-label={t('Delete selected redemption codes')}
                title={t('Delete selected redemption codes')}
              />
            }
          >
            <Trash2 />
            <span className='sr-only'>
              {t('Delete selected redemption codes')}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('Delete selected redemption codes')}</p>
          </TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='outline'
                size='icon'
                onClick={() => setShowDeleteInvalidConfirm(true)}
                className='size-8'
                aria-label={t('Delete invalid redemption codes')}
                title={t('Delete invalid redemption codes')}
              />
            }
          >
            <Eraser />
            <span className='sr-only'>{t('Delete invalid codes')}</span>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('Delete invalid codes (used/disabled/expired)')}</p>
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>

      <ConfirmDialog
        destructive
        open={showDeleteSelectedConfirm}
        onOpenChange={setShowDeleteSelectedConfirm}
        handleConfirm={handleDeleteSelected}
        isLoading={isDeletingSelected}
        className='max-w-md'
        title={t('Delete {{count}} redemption code(s)?', {
          count: selectedRows.length,
        })}
        desc={
          <>
            {t('You are about to delete {{count}} redemption code(s).', {
              count: selectedRows.length,
            })}
            <br />
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={t('Delete')}
      />

      <ConfirmDialog
        destructive
        open={showDeleteInvalidConfirm}
        onOpenChange={setShowDeleteInvalidConfirm}
        handleConfirm={handleDeleteInvalid}
        isLoading={isDeletingInvalid}
        className='max-w-md'
        title={t('Delete Invalid Redemption Codes?')}
        desc={
          <>
            {t('This will delete all')} <strong>{t('used')}</strong>,{' '}
            <strong>{t('disabled')}</strong>
            {t(', and')} <strong>{t('expired')}</strong>{' '}
            {t('redemption codes.')}
            <br />
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={t('Delete Invalid')}
      />
    </>
  )
}
