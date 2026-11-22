import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import type { ChannelFormValues } from '../lib/channel-form'
import { ChannelProviderSettings } from './channel-provider-settings'
import { ChannelDefaultBaseURL } from './channel-default-base-url'

export function ChannelHTTPSettings({
  form,
}: {
  form: UseFormReturn<ChannelFormValues>
}) {
  const { t } = useTranslation()
  const protocol = useWatch({ control: form.control, name: 'http_protocol' })

  return (
    <div className='space-y-4'>
      <ChannelDefaultBaseURL form={form} />
      <ChannelProviderSettings form={form} />
      <p className='text-muted-foreground text-xs'>
        {t('Applies to shared HTTP relay requests; SDK and WebSocket connections are not included.')}
      </p>
      <div className='grid gap-4 sm:grid-cols-2'>
        <FormField
          control={form.control}
          name='http_protocol'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('HTTP Protocol')}</FormLabel>
              <Select
                value={field.value || 'auto'}
                onValueChange={(value) => {
                  const next = value === 'http1' ? 'http1' : 'auto'
                  field.onChange(next)
                  if (next === 'http1') {
                    form.setValue('http2_connection_shards', 1, {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }
                }}
              >
                <FormControl>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                </FormControl>
                <SelectContent>
                  <SelectItem value='auto'>{t('Auto')}</SelectItem>
                  <SelectItem value='http1'>{t('HTTP/1.1')}</SelectItem>
                </SelectContent>
              </Select>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='http2_connection_shards'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('HTTP/2 Connection Shards')}</FormLabel>
              <Select
                disabled={protocol === 'http1'}
                value={String(protocol === 'http1' ? 1 : field.value || 1)}
                onValueChange={(value) => field.onChange(Number(value))}
              >
                <FormControl>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                </FormControl>
                <SelectContent>
                  {Array.from({ length: 8 }, (_, index) => index + 1).map((count) => (
                    <SelectItem key={count} value={String(count)}>{count}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>
      <FormField
        control={form.control}
        name='tls_insecure_skip_verify'
        render={({ field }) => (
          <FormItem className='flex items-center justify-between gap-4'>
            <div className='space-y-1'>
              <FormLabel>{t('Skip Upstream TLS Verification')}</FormLabel>
              <FormDescription>
                {t('Accept upstream certificates that fail validation, such as self-signed ones. Only enable for trusted internal upstreams.')}
              </FormDescription>
            </div>
            <FormControl>
              <Switch checked={field.value === true} onCheckedChange={field.onChange} />
            </FormControl>
          </FormItem>
        )}
      />
    </div>
  )
}
