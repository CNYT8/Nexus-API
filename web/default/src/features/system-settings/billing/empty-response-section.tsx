import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { api } from '@/lib/api'
import { SettingsSection } from '../components/settings-section'
import { SettingsPageFormActions } from '../components/settings-page-context'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

type EmptyResponseSettings = {
  enabled: boolean
  period_days: number
  refund_percent: number
}

const defaultSettings: EmptyResponseSettings = {
  enabled: false,
  period_days: 3,
  refund_percent: 70,
}

export function EmptyResponseSettingsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [settings, setSettings] = useState(defaultSettings)

  const settingsQuery = useQuery({
    queryKey: ['empty-response-settings'],
    queryFn: async () =>
      api.get<ApiResponse<EmptyResponseSettings>>('/api/empty-responses/settings'),
  })

  useEffect(() => {
    const data = settingsQuery.data?.data.data
    if (!data) return
    setSettings({
      enabled: Boolean(data.enabled),
      period_days: Number(data.period_days) || 3,
      refund_percent: Number(data.refund_percent) || 70,
    })
  }, [settingsQuery.data])

  const saveMutation = useMutation({
    mutationFn: async () =>
      api.put<ApiResponse<EmptyResponseSettings>>(
        '/api/empty-responses/settings',
        settings
      ),
    onSuccess: async (result) => {
      if (!result.data.success) {
        toast.error(result.data.message || t('Failed to save settings'))
        return
      }
      await queryClient.invalidateQueries({ queryKey: ['empty-response-settings'] })
      toast.success(t('Setting updated successfully'))
    },
    onError: () => toast.error(t('Failed to save settings')),
  })

  const busy = settingsQuery.isLoading || saveMutation.isPending

  return (
    <SettingsSection title={t('Empty Response Compensation')}>
      <SettingsForm
        onSubmit={(event) => {
          event.preventDefault()
          saveMutation.mutate()
        }}
      >
        <SettingsPageFormActions
          onSave={() => saveMutation.mutate()}
          isSaving={busy}
          saveLabel='Save empty response settings'
        />
        <SettingsSwitchItem>
          <SettingsSwitchContent>
            <Label>{t('Enable empty response compensation')}</Label>
            <p className='text-muted-foreground text-sm'>
              {t(
                'When enabled, paid requests with input tokens and zero output tokens become eligible for compensation.'
              )}
            </p>
          </SettingsSwitchContent>
          <Switch
            checked={settings.enabled}
            disabled={busy}
            onCheckedChange={(enabled) =>
              setSettings((current) => ({ ...current, enabled }))
            }
          />
        </SettingsSwitchItem>
        <div className='grid gap-4 md:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor='empty-response-period-days'>{t('Detection period')}</Label>
            <Input
              id='empty-response-period-days'
              type='number'
              min={1}
              max={30}
              disabled={busy}
              value={settings.period_days}
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  period_days: Number(event.target.value),
                }))
              }
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='empty-response-refund-percent'>
              {t('Compensation percentage')}
            </Label>
            <Input
              id='empty-response-refund-percent'
              type='number'
              min={1}
              max={100}
              disabled={busy}
              value={settings.refund_percent}
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  refund_percent: Number(event.target.value),
                }))
              }
            />
          </div>
        </div>
      </SettingsForm>
    </SettingsSection>
  )
}
