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
import { useMemo, useState } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'
import { DateTimePicker } from '@/components/datetime-picker'
import { useLogCleanupTask } from '../hooks/use-log-cleanup-task'
import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { useResetForm } from '../hooks/use-reset-form'
import { LogPolicySettings } from './log-policy-settings'

const logSettingsSchema = z.object({
  LogConsumeEnabled: z.boolean(),
})

type LogSettingsFormValues = z.infer<typeof logSettingsSchema>

type LogSettingsSectionProps = {
  defaultEnabled: boolean
}

const HOURS_IN_DAY = 24

const getDateHoursAgo = (hours: number) => {
  const date = new Date()
  date.setHours(date.getHours() - hours)
  return date
}

const getDateDaysAgo = (days: number) => getDateHoursAgo(days * HOURS_IN_DAY)

const quickSelectOptions = [
  {
    label: '24 hours ago',
    getValue: () => getDateHoursAgo(24),
  },
  {
    label: '7 days ago',
    getValue: () => getDateDaysAgo(7),
  },
  {
    label: '30 days ago',
    getValue: () => getDateDaysAgo(30),
  },
]

export function LogSettingsSection({
  defaultEnabled,
}: LogSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const form = useForm<LogSettingsFormValues>({
    resolver: zodResolver(logSettingsSchema),
    defaultValues: {
      LogConsumeEnabled: defaultEnabled,
    },
  })

  const [purgeDate, setPurgeDate] = useState<Date | undefined>(() =>
    getDateDaysAgo(30)
  )
  const cleanup = useLogCleanupTask()
  const isCleaning = cleanup.create.isPending || cleanup.active
  const [showConfirmDialog, setShowConfirmDialog] = useState(false)
  const taskStatus = {
    queued: t('Cleanup queued'),
    running: t('Cleanup running'),
    succeeded: t('Cleanup completed'),
    failed: t('Cleanup failed'),
  }

  const submitForm = useResetForm(form, { LogConsumeEnabled: defaultEnabled })

  const purgeTimestamp = useMemo(() => {
    if (!purgeDate) return null
    const timestamp = Math.floor(purgeDate.getTime() / 1000)
    return Number.isFinite(timestamp) && timestamp > 0 ? timestamp : null
  }, [purgeDate])

  const formattedPurgeDate = useMemo(() => {
    if (!purgeDate) return ''
    return formatTimestampToDate(purgeDate.getTime(), 'milliseconds')
  }, [purgeDate])

  const onSubmit = async (values: LogSettingsFormValues) => {
    if (values.LogConsumeEnabled === defaultEnabled) return
    await updateOption.saveOptions([
      { key: 'LogConsumeEnabled', value: values.LogConsumeEnabled },
    ])
  }

  const handleRequestCleanLogs = () => {
    if (!purgeTimestamp || purgeTimestamp > Math.floor(Date.now() / 1000)) {
      toast.error(t('Select a past timestamp before clearing logs.'))
      return
    }

    setShowConfirmDialog(true)
  }

  const handleCleanLogs = async () => {
    if (isCleaning) return
    if (!purgeTimestamp || purgeTimestamp > Math.floor(Date.now() / 1000)) {
      toast.error(t('Select a past timestamp before clearing logs.'))
      return
    }

    try {
      await cleanup.create.mutateAsync(purgeTimestamp)
      setShowConfirmDialog(false)
    } catch (error) {
      const message =
        error instanceof Error ? error.message : t('Failed to clean logs')
      toast.error(message)
    }
  }

  return (
    <SettingsSection title={t('Log Maintenance')}>
      <Form {...form}>
        <SettingsForm onSubmit={submitForm(onSubmit)}>
          <SettingsPageFormActions
            onSave={submitForm(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save log settings'
          />
          <FormField
            control={form.control}
            name='LogConsumeEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Record quota usage')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Track per-request consumption to power usage analytics. Keeping this on increases database writes.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />

          <SettingsControlGroup className='space-y-3'>
            <div>
              <h4 className='text-sm font-medium'>{t('Clean history logs')}</h4>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Remove all log entries created before the selected timestamp.'
                )}
              </p>
            </div>
            <DateTimePicker value={purgeDate} onChange={setPurgeDate} />
            <div className='flex flex-wrap gap-3'>
              {quickSelectOptions.map((option) => (
                <Button
                  key={option.label}
                  type='button'
                  variant='outline'
                  onClick={() => setPurgeDate(option.getValue())}
                >
                  {t(option.label)}
                </Button>
              ))}
              <Button
                type='button'
                variant='destructive'
                onClick={handleRequestCleanLogs}
                disabled={isCleaning}
              >
                {isCleaning ? t('Cleaning...') : t('Clean logs')}
              </Button>
            </div>
            {cleanup.taskId > 0 && (
              <div className='space-y-2 rounded-md border p-3 text-sm' role='status' aria-live='polite'>
                <p>{t('Cleanup task #{{id}}', { id: cleanup.taskId })}</p>
                {cleanup.task && (
                  <>
                    <p>{taskStatus[cleanup.task.status]}</p>
                    <p>{t('Processed {{count}} log(s).', { count: cleanup.task.processed })}</p>
                    <p>{t('Logs before {{date}}', { date: formatTimestampToDate(cleanup.task.target, 'seconds') })}</p>
                    {cleanup.task.error && <p className='text-destructive' role='alert'>{cleanup.task.error}</p>}
                  </>
                )}
                {cleanup.error && <p className='text-destructive' role='alert'>{cleanup.error.message}</p>}
                {cleanup.active && <p className='text-muted-foreground'>{t('Automatic checks pause after five minutes. Refresh status to continue.')}</p>}
                <Button type='button' variant='outline' size='sm' disabled={cleanup.fetching} onClick={cleanup.refresh}>
                  {t('Refresh status')}
                </Button>
              </div>
            )}
          </SettingsControlGroup>
        </SettingsForm>
      </Form>
      <LogPolicySettings />
      <AlertDialog open={showConfirmDialog} onOpenChange={setShowConfirmDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Confirm log cleanup')}</AlertDialogTitle>
            <AlertDialogDescription>
              {formattedPurgeDate
                ? t(
                    'This will permanently remove all log entries created before {{date}}.',
                    { date: formattedPurgeDate }
                  )
                : t(
                    'This will permanently remove log entries before the selected timestamp.'
                  )}{' '}
              {t('This action cannot be undone.')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isCleaning}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleCleanLogs} disabled={isCleaning}>
              {isCleaning ? t('Cleaning...') : t('Delete logs')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsSection>
  )
}
