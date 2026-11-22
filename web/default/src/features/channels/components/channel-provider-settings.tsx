import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import type { ChannelFormValues } from '../lib/channel-form'

export function ChannelProviderSettings({ form }: { form: UseFormReturn<ChannelFormValues> }) {
  const { t } = useTranslation()
  const [type, raw] = useWatch({ control: form.control, name: ['type', 'settings'] })
  let settings: Record<string, unknown> | null
  try {
    const parsed = JSON.parse(raw || '{}')
    settings = parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : null
  } catch { settings = null }
  const update = (key: string, value: string | boolean) => {
    if (!settings) return
    form.setValue('settings', JSON.stringify({ ...settings, [key]: value }), { shouldDirty: true, shouldValidate: true })
  }
  const select = (key: string, label: string, fallback: string, options: string[]) => (
    <div className='space-y-2'>
      <p className='text-sm font-medium'>{label}</p>
      <Select disabled={!settings} value={typeof settings?.[key] === 'string' ? settings[key] as string : fallback} onValueChange={(value) => update(key, value)}>
        <SelectTrigger aria-label={label}><SelectValue /></SelectTrigger>
        <SelectContent>{options.map((value) => <SelectItem key={value} value={value}>{value === 'invoke_model' ? 'InvokeModel' : value === 'mantle' ? 'Mantle' : value}</SelectItem>)}</SelectContent>
      </Select>
    </div>
  )
  const toggle = (key: string, label: string) => (
    <div className='flex items-center justify-between gap-4'>
      <p className='text-sm font-medium'>{label}</p>
      <Switch aria-label={label} disabled={!settings} checked={settings?.[key] === true} onCheckedChange={(value) => update(key, value)} />
    </div>
  )
  return (
    <div className='space-y-4'>
      <p className='text-sm font-semibold'>{t('提供商接口设置')}</p>
      {!settings && <p role='alert' className='text-destructive text-sm'>{t('请先修正渠道 settings JSON，接口设置暂不可编辑')}</p>}
      {Number(type) === 33 && select('aws_api_type', t('AWS 接口类型'), 'invoke_model', ['invoke_model', 'mantle'])}
      {Number(type) === 41 && select('vertex_api_version', t('Vertex API 版本'), 'v1', ['v1', 'v1beta1'])}
      {Number(type) === 36 && toggle('ollama_openai_chat', t('Ollama 使用 OpenAI 兼容聊天接口'))}
      {toggle('disable_task_polling_sleep', t('关闭任务轮询休眠'))}
      <p className='text-muted-foreground text-xs'>{t('仅影响对应提供商；未修改的扩展设置会保留')}</p>
    </div>
  )
}
