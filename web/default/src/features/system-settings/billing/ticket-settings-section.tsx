/*
Copyright (C) 2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import {
  SettingsControlGroup,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { getTicketSettings, updateTicketSettings } from '@/features/tickets/api'
import type { TicketSettings } from '@/features/tickets/types'

const defaultSettings: TicketSettings = {
  enabled: true,
  admin_manage_enabled: true,
  admin_can_close: true,
  max_content_length: 4000,
}

export function TicketSettingsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [settings, setSettings] = useState<TicketSettings>(defaultSettings)
  const settingsQuery = useQuery({
    queryKey: ['ticket-settings'],
    queryFn: getTicketSettings,
  })

  useEffect(() => {
    if (settingsQuery.data?.success && settingsQuery.data.data) {
      setSettings(settingsQuery.data.data)
    }
  }, [settingsQuery.data])

  const saveMutation = useMutation({
    mutationFn: () => updateTicketSettings(settings),
    onSuccess: async (result) => {
      if (!result.success || !result.data) {
        toast.error(result.message || t('Failed to save settings'))
        return
      }
      setSettings(result.data)
      await queryClient.invalidateQueries({ queryKey: ['ticket-settings'] })
      await queryClient.invalidateQueries({ queryKey: ['status'] })
      toast.success(t('Settings saved'))
    },
  })

  const update = <K extends keyof TicketSettings>(
    key: K,
    value: TicketSettings[K]
  ) => setSettings((current) => ({ ...current, [key]: value }))

  return (
    <SettingsSection title={t('Ticket Settings')}>
      <SettingsPageFormActions
        onSave={() => saveMutation.mutate()}
        isSaving={saveMutation.isPending || settingsQuery.isLoading}
        saveLabel='Save Settings'
      />
      <SettingsControlGroup>
        <SettingsSwitchField
          checked={settings.enabled}
          disabled={settingsQuery.isLoading}
          onCheckedChange={(value) => update('enabled', value)}
          label={t('Enable Ticket Center')}
          description={t('When disabled, users cannot create, view, or reply to tickets.')}
        />
        <SettingsSwitchField
          checked={settings.admin_manage_enabled}
          disabled={settingsQuery.isLoading}
          onCheckedChange={(value) => update('admin_manage_enabled', value)}
          label={t('Allow administrators to manage tickets')}
          description={t('Administrators still need the Ticket Management permission.')}
        />
        <SettingsSwitchField
          checked={settings.admin_can_close}
          disabled={settingsQuery.isLoading || !settings.admin_manage_enabled}
          onCheckedChange={(value) => update('admin_can_close', value)}
          label={t('Allow administrators to close tickets')}
          description={t('Super administrators can always close and reopen tickets.')}
        />
        <div className='flex items-center justify-between gap-4 border-b py-2.5 last:border-b-0'>
          <div className='min-w-0 space-y-0.5'>
            <Label className='text-sm font-medium'>{t('Maximum content length')}</Label>
            <p className='text-muted-foreground text-xs'>
              {t('Applies to new tickets and follow-up replies.')}
            </p>
          </div>
          <Input
            className='w-32 text-right'
            type='number'
            min={100}
            max={20000}
            step={100}
            value={settings.max_content_length}
            disabled={settingsQuery.isLoading}
            onChange={(event) => {
              const value = Number(event.target.value)
              update(
                'max_content_length',
                Number.isFinite(value) ? Math.min(20000, Math.max(100, value)) : 4000
              )
            }}
          />
        </div>
      </SettingsControlGroup>
    </SettingsSection>
  )
}
