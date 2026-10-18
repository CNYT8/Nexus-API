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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const schema = z.object({
  enabled: z.boolean(),
  countDuringClosure: z.boolean(),
  ratePercent: z.coerce.number().finite().min(0).max(100),
  payoutMode: z.enum(['immediate', 'scheduled', 'half_day', 'custom']),
  payoutHour: z.coerce.number().int().min(0).max(23),
  payoutMinute: z.coerce.number().int().min(0).max(59),
  payoutIntervalHours: z.coerce.number().int().min(1).max(720),
  siteTimezone: z.string().min(1),
  inviterLimitPeriod: z.enum(['daily', 'monthly', 'permanent']),
  inviteeCap: z.coerce
    .number()
    .finite()
    .min(0)
    .max(Number.MAX_SAFE_INTEGER / 1_000_000),
  inviterEligibleCap: z.coerce
    .number()
    .finite()
    .min(0)
    .max(Number.MAX_SAFE_INTEGER / 1_000_000),
  inviterMinPaid: z.coerce
    .number()
    .finite()
    .min(0)
    .max(Number.MAX_SAFE_INTEGER / 1_000_000),
  payoutHoldHours: z.coerce.number().int().min(0).max(720),
})

type Values = z.infer<typeof schema>
const MICROS = 1_000_000

function toMicros(value: number) {
  const result = Math.round(value * MICROS)
  return Number.isSafeInteger(result) ? String(result) : ''
}

type FieldProps = {
  form: ReturnType<typeof useForm<Values>>
  name:
    | 'ratePercent'
    | 'payoutIntervalHours'
    | 'payoutHour'
    | 'payoutMinute'
    | 'inviteeCap'
    | 'inviterEligibleCap'
    | 'inviterMinPaid'
    | 'payoutHoldHours'
  label: string
  min: number
  max?: number
  step: string
}

function NumberField({ form, name, label, min, max, step }: FieldProps) {
  return (
    <FormField
      control={form.control}
      name={name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <Input
              type='number'
              min={min}
              max={max}
              step={step}
              {...field}
              onChange={(event) => field.onChange(event.target.value)}
            />
          </FormControl>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

export function RebateSettingsSection({
  defaultValues,
}: {
  defaultValues: {
    enabled: boolean
    countDuringClosure: boolean
    rateBasisPoints: number
    payoutMode: string
    payoutHour: number
    payoutMinute: number
    payoutIntervalHours: number
    siteTimezone: string
    inviterLimitPeriod: string
    inviteeCap: number
    inviterEligibleCap: number
    inviterMinPaid: number
    payoutHoldHours: number
  }
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: {
      enabled: defaultValues.enabled,
      countDuringClosure: defaultValues.countDuringClosure,
      ratePercent: defaultValues.rateBasisPoints / 100,
      payoutMode: defaultValues.payoutMode as Values['payoutMode'],
      payoutHour: defaultValues.payoutHour,
      payoutMinute: defaultValues.payoutMinute,
      payoutIntervalHours: defaultValues.payoutIntervalHours,
      siteTimezone: defaultValues.siteTimezone,
      inviterLimitPeriod:
        defaultValues.inviterLimitPeriod as Values['inviterLimitPeriod'],
      inviteeCap: defaultValues.inviteeCap / MICROS,
      inviterEligibleCap: defaultValues.inviterEligibleCap / MICROS,
      inviterMinPaid: defaultValues.inviterMinPaid / MICROS,
      payoutHoldHours: defaultValues.payoutHoldHours,
    },
  })
  const { isDirty, isSubmitting } = form.formState
  const enabled = form.watch('enabled')
  const payoutMode = form.watch('payoutMode')

  async function onSubmit(values: Values) {
    const updates: Array<[string, string]> = [
      ['enabled', String(values.enabled)],
      ['count_during_closure', String(values.countDuringClosure)],
      ['rate_basis_points', String(Math.round(values.ratePercent * 100))],
      ['payout_mode', values.payoutMode],
      ['payout_hour', String(values.payoutHour)],
      ['payout_minute', String(values.payoutMinute)],
      ['payout_interval_hours', String(values.payoutIntervalHours)],
      ['site_timezone', values.siteTimezone],
      ['inviter_limit_period', values.inviterLimitPeriod],
      ['invitee_eligible_money_limit_micros', toMicros(values.inviteeCap)],
      [
        'inviter_eligible_money_limit_micros',
        toMicros(values.inviterEligibleCap),
      ],
      ['inviter_min_paid_money_micros', toMicros(values.inviterMinPaid)],
      ['payout_hold_hours', String(values.payoutHoldHours)],
    ]
    if (updates.some(([, value]) => value === '')) {
      toast.error(t('Invalid rebate setting value'))
      return
    }
    for (const [field, value] of updates) {
      await updateOption.mutateAsync({ key: `rebate_setting.${field}`, value })
    }
    form.reset(values)
  }

  return (
    <SettingsSection title={t('Recharge Rebate Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel='Save recharge rebate settings'
          />
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between gap-4 border-b py-2.5'>
                <div>
                  <FormLabel>{t('Enable recharge rebates')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Only direct referral top-up spending qualifies; trial, gifted, and rebate credits do not qualify.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />
          {enabled && (
            <>
              <FormField
                control={form.control}
                name='countDuringClosure'
                render={({ field }) => (
                  <FormItem className='flex items-center justify-between gap-4 border-b py-2.5'>
                    <div>
                      <FormLabel>{t('Count rewards during closure')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Keep the ledger and source snapshots ready before enabling this feature.'
                        )}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />
              <div className='grid gap-6 sm:grid-cols-2'>
                <NumberField
                  form={form}
                  name='ratePercent'
                  label={t('Rebate rate (%)')}
                  min={0}
                  max={100}
                  step='0.01'
                />
                <FormField
                  control={form.control}
                  name='payoutMode'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Payout mode')}</FormLabel>
                      <Select
                        value={field.value}
                        onValueChange={field.onChange}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectItem value='immediate'>
                            {t('Immediate')}
                          </SelectItem>
                          <SelectItem value='scheduled'>
                            {t('Scheduled')}
                          </SelectItem>
                          <SelectItem value='half_day'>
                            {t('Twice daily')}
                          </SelectItem>
                          <SelectItem value='custom'>
                            {t('Custom interval')}
                          </SelectItem>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                {payoutMode === 'custom' && (
                  <NumberField
                    form={form}
                    name='payoutIntervalHours'
                    label={t('Payout interval (hours)')}
                    min={1}
                    max={720}
                    step='1'
                  />
                )}
                {payoutMode !== 'immediate' && (
                  <>
                    <NumberField
                      form={form}
                      name='payoutHour'
                      label={t('Payout hour')}
                      min={0}
                      max={23}
                      step='1'
                    />
                    <NumberField
                      form={form}
                      name='payoutMinute'
                      label={t('Payout minute')}
                      min={0}
                      max={59}
                      step='1'
                    />
                    <FormField
                      control={form.control}
                      name='siteTimezone'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Site timezone')}</FormLabel>
                          <FormControl>
                            <Input {...field} />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </>
                )}
                <FormField
                  control={form.control}
                  name='inviterLimitPeriod'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Inviter cap period')}</FormLabel>
                      <Select
                        value={field.value}
                        onValueChange={field.onChange}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectItem value='daily'>{t('Daily')}</SelectItem>
                          <SelectItem value='monthly'>
                            {t('Monthly')}
                          </SelectItem>
                          <SelectItem value='permanent'>
                            {t('Permanent')}
                          </SelectItem>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <NumberField
                  form={form}
                  name='inviteeCap'
                  label={t('Invitee lifetime spending cap')}
                  min={0}
                  step='0.000001'
                />
                <NumberField
                  form={form}
                  name='inviterEligibleCap'
                  label={t('Direct referrals spending cap')}
                  min={0}
                  step='0.000001'
                />
                <NumberField
                  form={form}
                  name='inviterMinPaid'
                  label={t('Inviter rebate threshold (lifetime paid top-up)')}
                  min={0}
                  step='0.000001'
                />
                <NumberField
                  form={form}
                  name='payoutHoldHours'
                  label={t('Payout risk hold (hours)')}
                  min={0}
                  max={720}
                  step='1'
                />
              </div>
            </>
          )}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
