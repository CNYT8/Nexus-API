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
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { addTimeToDate } from '@/lib/time'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { createRedemption, updateRedemption, getRedemption } from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import {
  getRedemptionFormSchema,
  type RedemptionFormValues,
  REDEMPTION_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
} from '../lib'
import { type Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

type RedemptionsMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Redemption
}

export function RedemptionsMutateDrawer(props: RedemptionsMutateDrawerProps) {
  return (
    <RedemptionsMutateDrawerForm
      key={`${props.open}-${props.currentRow?.id ?? 'create'}`}
      {...props}
    />
  )
}

function RedemptionsMutateDrawerForm({
  open,
  onOpenChange,
  currentRow,
}: RedemptionsMutateDrawerProps) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const { triggerRefresh } = useRedemptions()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [loadedRowId, setLoadedRowId] = useState<number | null>(null)
  const [detailError, setDetailError] = useState(false)
  const [detailRetry, setDetailRetry] = useState(0)
  const detailRequest = useRef(0)
  const readyRowId = useRef<number | null>(null)
  const submitPending = useRef(false)
  const rowId = currentRow?.id
  const isDetailReady = !isUpdate || loadedRowId === rowId

  const form = useForm<RedemptionFormValues>({
    resolver: zodResolver(getRedemptionFormSchema(t)),
    defaultValues: REDEMPTION_FORM_DEFAULT_VALUES,
  })

  useEffect(() => {
    const request = ++detailRequest.current
    readyRowId.current = null
    form.reset(REDEMPTION_FORM_DEFAULT_VALUES)

    if (open && rowId !== undefined) {
      getRedemption(rowId)
        .then((result) => {
          if (request !== detailRequest.current) return
          if (!result.success || !result.data || result.data.id !== rowId) {
            setDetailError(true)
            return
          }
          form.reset(transformRedemptionToFormDefaults(result.data))
          readyRowId.current = rowId
          setLoadedRowId(rowId)
        })
        .catch(() => {
          if (request === detailRequest.current) setDetailError(true)
        })
    }

    return () => {
      detailRequest.current = request + 1
      readyRowId.current = null
    }
  }, [open, rowId, form, detailRetry])

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      detailRequest.current++
      readyRowId.current = null
      setLoadedRowId(null)
    }
    onOpenChange(nextOpen)
  }

  const onSubmit = async (data: RedemptionFormValues) => {
    if (
      !open ||
      submitPending.current ||
      !isDetailReady ||
      (isUpdate && readyRowId.current !== rowId)
    )
      return
    const request = detailRequest.current
    submitPending.current = true
    setIsSubmitting(true)
    try {
      const basePayload = transformFormDataToPayload(data)

      if (isUpdate && currentRow) {
        const result = await updateRedemption({
          ...basePayload,
          id: currentRow.id,
        })
        if (result.success) triggerRefresh()
        if (request !== detailRequest.current) return
        if (result.success) {
          toast.success(t(SUCCESS_MESSAGES.REDEMPTION_UPDATED))
          handleOpenChange(false)
        } else {
          toast.error(result.message || t(ERROR_MESSAGES.UPDATE_FAILED))
        }
      } else {
        // Create mode
        const result = await createRedemption(basePayload)
        if (result.success) triggerRefresh()
        if (request !== detailRequest.current) return
        if (result.success) {
          const count = result.data?.length || 0
          toast.success(
            count > 1
              ? t('Successfully created {{count}} redemption codes', {
                  count,
                })
              : t(SUCCESS_MESSAGES.REDEMPTION_CREATED)
          )
          handleOpenChange(false)
        } else {
          toast.error(result.message || t(ERROR_MESSAGES.CREATE_FAILED))
        }
      }
    } catch (_error) {
      if (request === detailRequest.current) {
        toast.error(t(ERROR_MESSAGES.UNEXPECTED))
      }
    } finally {
      submitPending.current = false
      setIsSubmitting(false)
    }
  }

  const handleSetExpiry = (months: number, days: number, hours: number) => {
    const newDate = addTimeToDate(months, days, hours)
    form.setValue('expired_time', newDate ?? null)
  }

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const quotaLabel = t('Quota ({{currency}})', { currency: currencyLabel })
  const quotaPlaceholder = tokensOnly
    ? t('Enter quota in tokens')
    : t('Enter quota in {{currency}}', { currency: currencyLabel })

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[600px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate
              ? t('Update Redemption Code')
              : t('Create Redemption Code')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate
              ? t('Update the redemption code by providing necessary info.')
              : t(
                  'Add new redemption code(s) by providing necessary info.'
                )}{' '}
            {t('Click save when you&apos;re done.')}
          </SheetDescription>
        </SheetHeader>
        {isUpdate && !isDetailReady && (
          <div className='space-y-2 px-6 py-4 text-sm' role='status'>
            <p>{t(detailError ? ERROR_MESSAGES.LOAD_FAILED : 'Loading...')}</p>
            {detailError && (
              <Button
                type='button'
                variant='outline'
                onClick={() => {
                  setDetailError(false)
                  setDetailRetry((retry) => retry + 1)
                }}
              >
                {t('Retry')}
              </Button>
            )}
          </div>
        )}
        <Form {...form}>
          <form
            id='redemption-form'
            onSubmit={(event) => {
              const request = detailRequest.current
              void form.handleSubmit((data) => {
                if (request === detailRequest.current) return onSubmit(data)
              })(event)
            }}
            inert={!isDetailReady || isSubmitting}
            aria-busy={!isDetailReady || isSubmitting}
            className={sideDrawerFormClassName()}
          >
            <SideDrawerSection>
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Name')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('Enter a name')} />
                    </FormControl>
                    <FormDescription>
                      {t('Name for this redemption code (1-20 characters)')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='quota_dollars'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{quotaLabel}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='number'
                        step={tokensOnly ? 1 : 0.01}
                        placeholder={quotaPlaceholder}
                        onChange={(e) =>
                          field.onChange(parseFloat(e.target.value) || 0)
                        }
                      />
                    </FormControl>
                    <FormDescription>
                      {tokensOnly
                        ? t('Enter the quota amount in tokens')
                        : t('Enter the quota amount in {{currency}}', {
                            currency: currencyLabel,
                          })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='expired_time'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Expiration Time')}</FormLabel>
                    <div className='flex flex-col gap-2'>
                      <FormControl>
                        <DateTimePicker
                          value={field.value ?? undefined}
                          onChange={(date) => field.onChange(date ?? null)}
                          placeholder={t('Never expires')}
                        />
                      </FormControl>
                      <div className='grid grid-cols-4 gap-1.5 sm:flex sm:gap-2'>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 0, 0)}
                        >
                          {t('Never')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(1, 0, 0)}
                        >
                          {t('1M')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 7, 0)}
                        >
                          {t('1W')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 1, 0)}
                        >
                          {t('1 Day')}
                        </Button>
                      </div>
                    </div>
                    <FormDescription>
                      {t('Leave empty for never expires')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {!isUpdate && (
                <FormField
                  control={form.control}
                  name='count'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Quantity')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min='1'
                          max='100'
                          placeholder={t('Number of codes to create')}
                          onChange={(e) =>
                            field.onChange(parseInt(e.target.value, 10) || 1)
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Create multiple redemption codes at once (1-100)')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='redemption-form'
            type='submit'
            disabled={isSubmitting || !isDetailReady}
          >
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
