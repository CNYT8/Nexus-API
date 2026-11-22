import { useQuery } from '@tanstack/react-query'
import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { ChannelFormValues } from '../lib/channel-form'

export function ChannelDefaultBaseURL({ form }: { form: UseFormReturn<ChannelFormValues> }) {
  const { t } = useTranslation()
  const type = useWatch({ control: form.control, name: 'type' })
  const { data, isPending, isError, refetch, isFetching } = useQuery({
    queryKey: ['channels', 'default-base-urls'],
    queryFn: async () => {
      const response = await api.get('/api/channel/default_base_urls', {
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      const payload = response.data
      if (payload?.success !== true || !payload.data || typeof payload.data !== 'object' || Array.isArray(payload.data)) {
        throw new Error('Invalid default base URLs response')
      }
      return payload.data as Record<string, unknown>
    },
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
  const value = data?.[String(type)]
  const baseURL = typeof value === 'string' ? value.trim() : ''

  return (
    <div className='space-y-1 text-sm' aria-live='polite'>
      <p className='font-medium'>{t('服务端默认地址')}</p>
      {isPending ? <p className='text-muted-foreground'>{t('加载中...')}</p> : isError ? (
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-destructive'>{t('默认地址加载失败')}</span>
          <button type='button' className='underline' disabled={isFetching} onClick={() => void refetch()}>{t('重试')}</button>
        </div>
      ) : <p className='break-all text-muted-foreground'>{baseURL || t('该渠道类型未提供默认地址')}</p>}
      <p className='text-xs text-muted-foreground'>{t('仅供参考，不会覆盖已填写的代理地址')}</p>
    </div>
  )
}
