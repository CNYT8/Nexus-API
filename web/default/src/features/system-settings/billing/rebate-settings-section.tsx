import { useEffect, useRef, useState, type BaseSyntheticEvent } from 'react'
import { z } from 'zod'
import { useForm, type Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import type { RebateConfigSnapshot } from '../api'
import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import {
  useRebateConfig,
  useUpdateRebateConfig,
} from '../hooks/use-update-option'
import { microsToMoney, moneyToMicros } from './rebate-money'

const schema = z.object({
  enabled: z.boolean(),
  countDuringClosure: z.boolean(),
  ratePercent: z.coerce.number().finite().min(0).max(100),
  payoutMode: z.enum(['immediate', 'scheduled', 'half_day', 'custom']),
  payoutHour: z.coerce.number().int().min(0).max(23),
  payoutMinute: z.coerce.number().int().min(0).max(59),
  payoutIntervalHours: z.coerce.number().int().min(0).max(720),
  siteTimezone: z.string(),
  inviterLimitPeriod: z.enum(['daily', 'monthly', 'permanent']),
  // Keep decimal text intact, including stored values above 2^53.
  inviteeCap: z.string(),
  inviterEligibleCap: z.string(),
  inviterMinPaid: z.string(),
  payoutHoldHours: z.coerce.number().int().min(0).max(720),
})

type Values = z.infer<typeof schema>
function valuesFromSnapshot({ fields }: RebateConfigSnapshot): Values {
  return {
    enabled: fields.enabled === 'true',
    countDuringClosure: fields.count_during_closure === 'true',
    ratePercent: Number(fields.rate_basis_points) / 100,
    payoutMode: fields.payout_mode as Values['payoutMode'],
    payoutHour: Number(fields.payout_hour),
    payoutMinute: Number(fields.payout_minute),
    payoutIntervalHours: Number(fields.payout_interval_hours),
    siteTimezone: fields.site_timezone,
    inviterLimitPeriod:
      fields.inviter_limit_period as Values['inviterLimitPeriod'],
    inviteeCap: microsToMoney(fields.invitee_eligible_money_limit_micros),
    inviterEligibleCap: microsToMoney(
      fields.inviter_eligible_money_limit_micros
    ),
    inviterMinPaid: microsToMoney(fields.inviter_min_paid_money_micros),
    payoutHoldHours: Number(fields.payout_hold_hours),
  }
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
  decimal?: boolean
}

function NumberField({
  form,
  name,
  label,
  min,
  max,
  step,
  decimal,
}: FieldProps) {
  return (
    <FormField
      control={form.control}
      name={name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <Input
              type={decimal ? 'text' : 'number'}
              inputMode={decimal ? 'decimal' : undefined}
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
  const configQuery = useRebateConfig()
  const updateConfig = useUpdateRebateConfig()
  const baseline = useRef<RebateConfigSnapshot | null>(null)
  const baselineUnavailable = useRef(true)
  const pending = useRef(false)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const [reloadRequired, setReloadRequired] = useState(false)
  const form = useForm<Values>({
    resolver: zodResolver(schema, {
      error: () => t('Invalid rebate setting value'),
    }) as unknown as Resolver<Values>,
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
      // Generic options may already have lost integer precision. Wait for
      // the dedicated string snapshot before displaying editable amounts.
      inviteeCap: '0',
      inviterEligibleCap: '0',
      inviterMinPaid: '0',
      payoutHoldHours: defaultValues.payoutHoldHours,
    },
  })
  // Subscribing to dirtyFields is required for keepDirtyValues on a refresh.
  const { isDirty, isSubmitting, dirtyFields } = form.formState
  void dirtyFields
  useEffect(() => {
    if (
      !configQuery.data ||
      !configQuery.isFetchedAfterMount ||
      configQuery.isFetching ||
      configQuery.isError ||
      pending.current
    ) {
      return
    }
    baseline.current = configQuery.data
    baselineUnavailable.current = false
    form.reset(valuesFromSnapshot(configQuery.data), { keepDirtyValues: true })
  }, [
    configQuery.data,
    configQuery.isFetchedAfterMount,
    configQuery.isFetching,
    configQuery.isError,
    form,
  ])
  const enabled = form.watch('enabled')
  const payoutMode = form.watch('payoutMode')

  const disabled =
    isSubmitting ||
    updateConfig.isPending ||
    reloadRequired ||
    configQuery.isFetching ||
    configQuery.isError ||
    !configQuery.isFetchedAfterMount ||
    !configQuery.data

  async function reloadConfig() {
    if (pending.current || isSubmitting || configQuery.isFetching) return
    pending.current = true
    baselineUnavailable.current = true
    try {
      const result = await configQuery.refetch({ throwOnError: true })
      if (mounted.current && result.isSuccess) {
        baseline.current = result.data
        form.reset(valuesFromSnapshot(result.data), { keepDirtyValues: true })
        baselineUnavailable.current = false
        setReloadRequired(false)
      }
    } catch {
      if (mounted.current) setReloadRequired(true)
    } finally {
      pending.current = false
    }
  }

  async function submitForm(event?: BaseSyntheticEvent) {
    event?.preventDefault()
    if (pending.current || baselineUnavailable.current || disabled) return
    pending.current = true
    const submitted = form.getValues()
    try {
      await form.handleSubmit((values) => onSubmit(values, submitted))(event)
    } finally {
      pending.current = false
    }
  }

  async function onSubmit(values: Values, submitted: Values) {
    if (!baseline.current || baselineUnavailable.current) return
    const previous = valuesFromSnapshot(baseline.current)
    const fieldNames: Array<keyof Values> = [
      'enabled',
      'countDuringClosure',
      'ratePercent',
      'payoutMode',
      'payoutHour',
      'payoutMinute',
      'payoutIntervalHours',
      'siteTimezone',
      'inviterLimitPeriod',
      'inviteeCap',
      'inviterEligibleCap',
      'inviterMinPaid',
      'payoutHoldHours',
    ]
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
      [
        'invitee_eligible_money_limit_micros',
        moneyToMicros(values.inviteeCap) ?? '',
      ],
      [
        'inviter_eligible_money_limit_micros',
        moneyToMicros(values.inviterEligibleCap) ?? '',
      ],
      [
        'inviter_min_paid_money_micros',
        moneyToMicros(values.inviterMinPaid) ?? '',
      ],
      ['payout_hold_hours', String(values.payoutHoldHours)],
    ]
    // Preserve unchanged decimal strings, including values above JS's safe
    // integer range. Only edited inputs pass through the display conversion.
    const changed = updates.filter(
      (_, index) => values[fieldNames[index]] !== previous[fieldNames[index]]
    )
    if (
      (values.payoutMode === 'custom' && values.payoutIntervalHours === 0) ||
      changed.some(([key, value]) => key !== 'site_timezone' && value === '')
    ) {
      toast.error(t('Invalid rebate setting value'))
      return
    }
    try {
      await updateConfig.mutateAsync({
        revision: baseline.current.revision,
        fields: { ...baseline.current.fields, ...Object.fromEntries(changed) },
      })
      if (!mounted.current) return
      const result = await configQuery.refetch({ throwOnError: true })
      if (!mounted.current) return
      if (!result.isSuccess) throw new Error('Failed to reload rebate settings')
      const snapshot = result.data
      baseline.current = snapshot
      // Preserve any changes made while the request was in flight.
      const current = form.getValues()
      form.reset(valuesFromSnapshot(snapshot))
      for (const name of Object.keys(values) as Array<keyof Values>) {
        if (current[name] !== submitted[name]) {
          form.setValue(name, current[name], { shouldDirty: true })
        }
      }
    } catch {
      if (!mounted.current) return
      // A failed or uncertain write keeps all draft fields. Refreshing the
      // persisted version is required before retrying, including after a 409.
      baselineUnavailable.current = true
      setReloadRequired(true)
    }
  }

  return (
    <SettingsSection title={t('Recharge Rebate Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={submitForm} autoComplete='off' noValidate>
          <SettingsPageFormActions
            onSave={submitForm}
            isSaving={updateConfig.isPending || isSubmitting}
            isSaveDisabled={!isDirty || disabled}
            saveLabel='Save recharge rebate settings'
          />
          {(reloadRequired || configQuery.isError) && (
            <Button
              type='button'
              variant='outline'
              onClick={() => void reloadConfig()}
              disabled={
                isSubmitting || updateConfig.isPending || configQuery.isFetching
              }
            >
              {t('Refresh')}
            </Button>
          )}
          <fieldset disabled={disabled} className='contents'>
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
                      disabled={disabled}
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
                        <FormLabel>
                          {t('Count rewards during closure')}
                        </FormLabel>
                        <FormDescription>
                          {t(
                            'Keep the ledger and source snapshots ready before enabling this feature.'
                          )}
                        </FormDescription>
                      </div>
                      <FormControl>
                        <Switch
                          disabled={disabled}
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
                          disabled={disabled}
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
                          disabled={disabled}
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
                    decimal
                    label={t('Invitee lifetime spending cap')}
                    min={0}
                    step='0.000001'
                  />
                  <NumberField
                    form={form}
                    name='inviterEligibleCap'
                    decimal
                    label={t('Direct referrals spending cap')}
                    min={0}
                    step='0.000001'
                  />
                  <NumberField
                    form={form}
                    name='inviterMinPaid'
                    decimal
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
          </fieldset>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
