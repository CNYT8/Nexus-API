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
import { z } from 'zod'
import { useForm, type Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { Switch } from '@/components/ui/switch'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const schema = z.object({
  enabled: z.boolean(),
  conditionEnabled: z.boolean(),
  requestThreshold: z.coerce.number().int().min(0),
  tokenThreshold: z.coerce.number().int().min(0),
  amountThreshold: z.coerce.number().int().min(0),
  minQuota: z.coerce.number().int().min(0),
  maxQuota: z.coerce.number().int().min(0),
})

type Values = z.infer<typeof schema>

export function CheckinSettingsSection({
  defaultValues,
}: {
  defaultValues: {
    enabled: boolean
    conditionEnabled: boolean
    requestThreshold: number
    tokenThreshold: number
    amountThreshold: number
    minQuota: number
    maxQuota: number
  }
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: {
      enabled: defaultValues.enabled,
      conditionEnabled: defaultValues.conditionEnabled,
      requestThreshold: defaultValues.requestThreshold,
      tokenThreshold: defaultValues.tokenThreshold,
      amountThreshold: defaultValues.amountThreshold,
      minQuota: defaultValues.minQuota,
      maxQuota: defaultValues.maxQuota,
    },
  })

  const { isDirty, isSubmitting } = form.formState
  const enabled = form.watch('enabled')
  const conditionEnabled = form.watch('conditionEnabled')

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []

    if (values.enabled !== defaultValues.enabled) {
      updates.push({
        key: 'checkin_setting.enabled',
        value: String(values.enabled),
      })
    }

    if (values.conditionEnabled !== defaultValues.conditionEnabled) {
      updates.push({
        key: 'checkin_setting.condition_enabled',
        value: String(values.conditionEnabled),
      })
    }

    if (values.requestThreshold !== defaultValues.requestThreshold) {
      updates.push({
        key: 'checkin_setting.request_threshold',
        value: String(values.requestThreshold),
      })
    }

    if (values.tokenThreshold !== defaultValues.tokenThreshold) {
      updates.push({
        key: 'checkin_setting.token_threshold',
        value: String(values.tokenThreshold),
      })
    }

    if (values.amountThreshold !== defaultValues.amountThreshold) {
      updates.push({
        key: 'checkin_setting.amount_threshold',
        value: String(values.amountThreshold),
      })
    }

    if (values.minQuota !== defaultValues.minQuota) {
      updates.push({
        key: 'checkin_setting.min_quota',
        value: String(values.minQuota),
      })
    }

    if (values.maxQuota !== defaultValues.maxQuota) {
      updates.push({
        key: 'checkin_setting.max_quota',
        value: String(values.maxQuota),
      })
    }

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }

    form.reset(values)
  }

  return (
    <SettingsSection title={t('Check-in Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel='Save check-in settings'
          />
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable check-in feature')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Allow users to check in daily for random quota rewards'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={updateOption.isPending || isSubmitting}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          {enabled && (
            <div className='grid gap-6 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='conditionEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem className='sm:col-span-2'>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Enable conditional check-in')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Require previous-day usage before users can check in'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={updateOption.isPending || isSubmitting}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />

              <FormField
                control={form.control}
                name='requestThreshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Previous-day request threshold')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        placeholder={t('0 means no request limit')}
                        disabled={!conditionEnabled}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Users whose previous-day requests do not exceed this value cannot check in'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='tokenThreshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Previous-day usage threshold')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        placeholder={t('0 means no usage limit')}
                        disabled={!conditionEnabled}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Users whose previous-day token usage does not exceed this value cannot check in'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='amountThreshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Previous-day spent quota threshold')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        placeholder={t('0 means no amount limit')}
                        disabled={!conditionEnabled}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Users whose previous-day spent quota does not exceed this value cannot check in'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='minQuota'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Minimum check-in quota')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        placeholder={t('1000')}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Default minimum quota used when no stage rule overrides it'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='maxQuota'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Maximum check-in quota')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        placeholder={t('10000')}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Default maximum quota used when no stage rule overrides it'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          )}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
