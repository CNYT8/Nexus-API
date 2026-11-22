import { useTranslation } from 'react-i18next'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { readResponseModel, isResponseModelMismatch } from '../lib/response-model'

export function ResponseModelObservation({ other, isAdmin }: { other: unknown; isAdmin: boolean }) {
  const { t } = useTranslation()
  const observation = readResponseModel(other, isAdmin)
  if (!observation) return null
  const mismatch = isResponseModelMismatch(observation)
  return (
    <Popover>
      <PopoverTrigger
        type='button'
        aria-label={t('响应模型观测')}
        onClick={(event) => event.stopPropagation()}
        className={`rounded border px-1.5 py-0.5 text-xs ${mismatch ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'}`}
      >
        {mismatch ? t('响应模型不匹配') : t('响应模型观测')}
      </PopoverTrigger>
      <PopoverContent align='start' className='w-80 max-w-[85vw] break-all'>
        <div className='font-medium'>{t('响应模型观测')}</div>
        <dl className='grid gap-1 text-xs'>
          <dt className='text-muted-foreground'>{t('请求模型')}</dt><dd>{observation.requested_model || '—'}</dd>
          <dt className='text-muted-foreground'>{t('上游模型')}</dt><dd>{observation.upstream_model || '—'}</dd>
          <dt className='text-muted-foreground'>{t('实际响应模型')}</dt><dd>{observation.returned_model}</dd>
        </dl>
        <p className='text-muted-foreground text-xs'>{t('仅记录上游响应声明的模型名称，不代表模型身份验证')}</p>
      </PopoverContent>
    </Popover>
  )
}
