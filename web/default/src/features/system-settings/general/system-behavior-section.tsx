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
import { useMemo } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
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
import { useResetForm } from '../hooks/use-reset-form'
import { useSystemOptions } from '../hooks/use-system-options'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const behaviorSchema = z.object({
  RetryTimes: z.coerce.number().min(0).max(10),
  MaxUserTokens: z.string(),
  DefaultCollapseSidebar: z.boolean(),
  DefaultRecordIpLogEnabled: z.boolean(),
  DefaultRecordIpLogForced: z.boolean(),
  DemoSiteEnabled: z.boolean(),
  SelfUseModeEnabled: z.boolean(),
})

type BehaviorFormValues = z.infer<typeof behaviorSchema>

type SystemBehaviorSectionProps = {
  defaultValues: Omit<BehaviorFormValues, 'MaxUserTokens'>
}

export function SystemBehaviorSection({
  defaultValues,
}: SystemBehaviorSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const { data: options } = useSystemOptions()
  const normalizedDefaults = useMemo(() => {
    const value = options?.data?.find(
      (option) => option.key === 'token_setting.max_user_tokens'
    )?.value
    return {
      ...defaultValues,
      MaxUserTokens: value == null || String(value).trim() === ''
        ? '1000'
        : String(value).trim(),
    }
  }, [defaultValues, options?.data])
  const localizedSchema = behaviorSchema.extend({
    MaxUserTokens: z.string().refine(
      (value) => value.trim() !== '' && Number.isSafeInteger(Number(value)) && Number(value) > 0,
      t('用户最大令牌数量必须为正整数')
    ),
  })

  const form = useForm({
    resolver: zodResolver(localizedSchema),
    defaultValues: normalizedDefaults,
  })

  const submitForm = useResetForm(form, normalizedDefaults)

  const onSubmit = async (data: BehaviorFormValues) => {
    const updates = Object.entries(data).filter(
      ([key, value]) => value !== normalizedDefaults[key as keyof BehaviorFormValues]
    )

    await updateOption.saveOptions(updates.map(([key, value]) => ({
      key: key === 'MaxUserTokens' ? 'token_setting.max_user_tokens' : key,
      value: key === 'MaxUserTokens' ? Number(value) : value,
    })))
  }

  return (
    <SettingsSection title={t('System Behavior')}>
      <Form {...form}>
        <SettingsForm onSubmit={submitForm(onSubmit)}>
          <SettingsPageFormActions
            onSave={submitForm(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='RetryTimes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Retry Times')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min='0'
                    max='10'
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t('Number of times to retry failed requests (0-10)')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='MaxUserTokens'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('用户最大令牌数量')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min='1'
                    step='1'
                    max={Number.MAX_SAFE_INTEGER}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t('每个用户最多可创建的令牌数量，默认 1000，设置过大可能会影响性能')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='DefaultCollapseSidebar'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Default Collapse Sidebar')}</FormLabel>
                  <FormDescription>
                    {t('Sidebar collapsed by default for new users')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='DefaultRecordIpLogEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Default Record Request and Error Log IP')}
                  </FormLabel>
                  <FormDescription>
                    {t('Use this setting as the default for all users')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='DefaultRecordIpLogForced'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t(
                      'Force Users to Use Default Record Request and Error Log IP Setting'
                    )}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Users cannot change the request and error log IP switch when enabled'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='DemoSiteEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Demo Site Mode')}</FormLabel>
                  <FormDescription>
                    {t('Enable demo mode with limited functionality')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='SelfUseModeEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Self-Use Mode')}</FormLabel>
                  <FormDescription>
                    {t('Optimize system for self-hosted single-user usage')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
