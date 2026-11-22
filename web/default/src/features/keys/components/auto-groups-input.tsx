import { useEffect, useRef, useState } from 'react'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { AxiosRequestConfig } from 'axios'
import type { ApiKeyFormValues } from '../lib/api-key-form'

type AutoGroupPolicy = { groups: string[]; max_count: number }

export function AutoGroupsInput({
  value,
  onChange,
}: {
  value: string[]
  onChange: (value: string[]) => void
}) {
  const { t } = useTranslation()
  const { control, setValue } = useFormContext<ApiKeyFormValues>()
  const limit = useWatch({ control, name: 'auto_group_limit' })
  const [policy, setPolicy] = useState<AutoGroupPolicy | null>(null)
  const [loading, setLoading] = useState(true)
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    let active = true
    setLoading(true)
    setPolicy(null)
    setValue('auto_group_limit', null)
    api.get('/api/token/auto-groups', { skipErrorHandler: true, skipBusinessError: true } as AxiosRequestConfig).then(({ data: response }) => {
      const data = response?.data
      if (!response?.success || !Array.isArray(data?.groups) || data.groups.some((group: unknown) => typeof group !== 'string' || !group || group === 'auto') || !Number.isSafeInteger(data.max_count) || data.max_count < 1) throw new Error('Invalid auto group policy')
      if (active) setPolicy(data as AutoGroupPolicy)
    }).catch(() => { /* Show only the safe inline failure below. */ }).finally(() => {
      if (active) setLoading(false)
    })
    return () => { active = false; setValue('auto_group_limit', null) }
  }, [retry, setValue])
  useEffect(() => {
    const next = policy?.max_count ?? null
    if (next !== limit) setValue('auto_group_limit', next, { shouldValidate: true })
  }, [limit, policy, setValue])
  const [text, setText] = useState(value.join('\n'))
  const serialized = JSON.stringify(value)
  const lastValue = useRef(serialized)
  useEffect(() => {
    if (lastValue.current === serialized) return
    lastValue.current = serialized
    setText((JSON.parse(serialized) as string[]).join('\n'))
  }, [serialized])
  return (
    <div className='space-y-2'>
    <Textarea
      aria-label={t('自动分组顺序')}
      value={text}
      onChange={(event) => {
        const next = event.target.value
        const groups = next.split('\n').map((group) => group.trim()).filter(Boolean)
        lastValue.current = JSON.stringify(groups)
        setText(next)
        onChange(groups)
      }}
      rows={3}
    />
    <div aria-live='polite' className='text-muted-foreground space-y-2 break-all text-xs'>
      <p className={policy && value.length > policy.max_count ? 'text-destructive' : undefined}>{policy ? t('已填写 {{count}} 个分组，上限 {{max}} 个', { count: value.length, max: policy.max_count }) : t('已填写 {{count}} 个分组', { count: value.length })}</p>
      <p>{value.length ? t('当前使用自定义顺序') : t('当前继承全局自动分组')}</p>
      <p>{t('全局自动分组顺序')}：{loading ? t('正在加载自动分组配置…') : policy ? (policy.groups.length ? policy.groups.join(' → ') : t('全局未配置可用自动分组')) : t('加载自动分组配置失败；可保存继承配置，自定义顺序需重试成功')}</p>
      <div className='flex flex-wrap gap-2'>
        {!loading && !policy && <Button type='button' size='sm' variant='outline' onClick={() => setRetry((count) => count + 1)}>{t('重试加载自动分组')}</Button>}
        <Button type='button' size='sm' variant='outline' disabled={!value.length && !text} onClick={() => {
          lastValue.current = '[]'
          setText('')
          onChange([])
        }}>{t('恢复全局自动分组')}</Button>
      </div>
    </div>
    </div>
  )
}
