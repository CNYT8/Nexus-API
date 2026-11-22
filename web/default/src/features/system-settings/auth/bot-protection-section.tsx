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
import { useUpdateOption } from '../hooks/use-update-option'
import { useResetForm } from '../hooks/use-reset-form'

const botProtectionSchema = z.object({
  TurnstileCheckEnabled: z.boolean(),
  TurnstileSiteKey: z.string().optional(),
  TurnstileSecretKey: z.string().optional(),
})

type BotProtectionFormValues = z.infer<typeof botProtectionSchema>

type BotProtectionSectionProps = {
  defaultValues: BotProtectionFormValues
  secretKeyConfigured: boolean
}

export function BotProtectionSection({
  defaultValues,
  secretKeyConfigured,
}: BotProtectionSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const form = useForm<BotProtectionFormValues>({
    resolver: zodResolver(botProtectionSchema),
    defaultValues,
  })

  const { isSubmitting } = form.formState
  const submitForm = useResetForm(form, defaultValues, (value) =>
    typeof value === 'string' ? value.trim() : value
  )

  const onSubmit = async (data: BotProtectionFormValues) => {
    const values = {
      ...data,
      TurnstileSiteKey: data.TurnstileSiteKey?.trim() ?? '',
      TurnstileSecretKey: data.TurnstileSecretKey?.trim() ?? '',
    }
    if (values.TurnstileCheckEnabled) {
      let invalid = false
      if (!values.TurnstileSiteKey) {
        form.setError('TurnstileSiteKey', { message: t('Required') })
        invalid = true
      }
      if (
        !secretKeyConfigured &&
        !values.TurnstileSecretKey
      ) {
        form.setError('TurnstileSecretKey', { message: t('Required') })
        invalid = true
      }
      if (invalid) return
    }

    const updates = Object.entries(values).filter(
      ([key, value]) =>
        !(
          key === 'TurnstileSecretKey' &&
          value === ''
        ) && value !== defaultValues[key as keyof BotProtectionFormValues]
    )
    const turnstileEnableUpdate = updates.find(
      ([key, value]) => key === 'TurnstileCheckEnabled' && value === true
    )
    const orderedUpdates = turnstileEnableUpdate
      ? [
          ...updates.filter(([key]) => key !== 'TurnstileCheckEnabled'),
          turnstileEnableUpdate,
        ]
      : updates

    const refreshed = await updateOption.saveOptionsInOrder(
      orderedUpdates.map(([key, value]) => ({ key, value }))
    )
    // A write-only secret can be cleared only after the whole save and a fresh
    // configured flag confirm it. Never clear text entered during submission.
    if (
      refreshed?.data.some(
        ({ key, value }) => key === 'TurnstileSecretKeyConfigured' && value === 'true'
      ) &&
      orderedUpdates.some(([key]) => key === 'TurnstileSecretKey') &&
      form.getValues('TurnstileSecretKey') === data.TurnstileSecretKey
    ) {
      form.resetField('TurnstileSecretKey', { defaultValue: '' })
    }
  }

  return (
    <SettingsSection title={t('Bot Protection')}>
      <Form {...form}>
        <SettingsForm
          onSubmit={submitForm(onSubmit)}
          autoComplete='off'
        >
          <SettingsPageFormActions
            onSave={submitForm(onSubmit)}
            isSaving={isSubmitting}
          />
          <FormField
            control={form.control}
            name='TurnstileCheckEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Turnstile')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Protect login and registration with Cloudflare Turnstile'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    disabled={isSubmitting}
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='TurnstileSiteKey'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Site Key')}</FormLabel>
                <FormControl>
                  <Input
                    disabled={isSubmitting}
                    placeholder={t('Your Turnstile site key')}
                    autoComplete='off'
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='TurnstileSecretKey'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Secret Key')}</FormLabel>
                <FormControl>
                  <Input
                    disabled={isSubmitting}
                    type='password'
                    placeholder={t('Your Turnstile secret key')}
                    autoComplete='new-password'
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
