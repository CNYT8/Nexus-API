import { useId, useMemo, useState, type ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { simulateRequest } from '@/features/pricing/lib/request-simulation'
import { TieredPricingEditor as PricingEditor } from './tiered-pricing-editor'

function RequestSimulation({ expression, rules }: { expression: string; rules: string }) {
  const { t } = useTranslation()
  const id = useId()
  const [open, setOpen] = useState(false)
  const [bodyText, setBodyText] = useState('{}')
  const [headersText, setHeadersText] = useState('{}')
  const [timeText, setTimeText] = useState('')
  const [variablesText, setVariablesText] = useState('{"p":1000,"c":500}')
  const [currentTime, setCurrentTime] = useState(() => new Date().toISOString())
  const result = useMemo(
    () => open ? simulateRequest({ expression, rules, bodyText, headersText, timeText: timeText || currentTime, variablesText }) : null,
    [open, expression, rules, bodyText, headersText, timeText, variablesText, currentTime]
  )
  const errors: Record<string, string> = {
    empty: t('请先填写计费表达式'),
    body: t('请求体必须是 JSON 对象'),
    headers: t('请求头必须是值均为字符串的 JSON 对象'),
    time: t('指定时间须为包含时区的有效 ISO 时间'),
    variables: t('用量变量必须是非负安全整数，仅支持下方列出的变量'),
    path: t('本地模拟不支持此参数路径或对象运算，请使用简单字段路径'),
    local_zone: t('本地模拟无法确定服务器 Local 时区，请改用明确的时区名称'),
    result: t('计费结果或请求规则倍率无效，必须为有限非负数'),
    expression: t('表达式无效或包含本地模拟不支持的语法'),
  }

  return (
    <details className='rounded-lg border p-3' onToggle={(event) => {
      setOpen(event.currentTarget.open)
      if (event.currentTarget.open) setCurrentTime(new Date().toISOString())
    }}>
      <summary className='cursor-pointer text-sm font-medium'>{t('请求情境模拟')}</summary>
      {open && (
        <div className='mt-3 space-y-3'>
          <p className='text-muted-foreground text-xs'>
            {t('仅在本地试算当前表达式与请求规则，不发送请求；费用为分组倍率前的美元金额，模拟输入不会保存。')}
          </p>
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='min-w-0'>
              <label htmlFor={`${id}-body`} className='mb-1 block text-sm'>{t('请求体（JSON）')}</label>
              <Textarea id={`${id}-body`} value={bodyText} onChange={(event) => setBodyText(event.target.value)} rows={5} maxLength={65536} className='font-mono text-xs' />
            </div>
            <div className='min-w-0'>
              <label htmlFor={`${id}-headers`} className='mb-1 block text-sm'>{t('请求头（JSON）')}</label>
              <Textarea id={`${id}-headers`} value={headersText} onChange={(event) => setHeadersText(event.target.value)} rows={5} maxLength={65536} className='font-mono text-xs' />
            </div>
          </div>
          <div>
            <label htmlFor={`${id}-time`} className='mb-1 block text-sm'>{t('指定请求时间')}</label>
            <Input id={`${id}-time`} value={timeText} onChange={(event) => setTimeText(event.target.value)} placeholder='2026-11-21T18:00:00+08:00' />
            <p className='text-muted-foreground mt-1 text-xs'>{t('留空使用本次试算的当前时间；时区由表达式中的时间函数指定。')}</p>
          </div>
          <div>
            <label htmlFor={`${id}-variables`} className='mb-1 block text-sm'>{t('用量变量（JSON）')}</label>
            <Textarea id={`${id}-variables`} value={variablesText} onChange={(event) => setVariablesText(event.target.value)} rows={3} maxLength={65536} className='font-mono text-xs' />
            <p className='text-muted-foreground mt-1 text-xs'>{t('填写归一化后的用量；未填写的变量为 0，len 默认合计输入项，也可显式指定。')}</p>
            <code className='block text-xs break-words'>p, c, len, cr, cc, cc1h, cc12h, img, img_o, ai, ao</code>
          </div>
          <Button type='button' size='sm' variant='outline' onClick={() => setCurrentTime(new Date().toISOString())}>{t('重新试算')}</Button>
          <div aria-live='polite' className='rounded-md border p-3 text-sm'>
            {result?.ok ? (
              <dl className='grid grid-cols-1 gap-2 sm:grid-cols-2'>
                {[
                  [t('基础费用（美元）'), `$${Number(result.baseCost.toPrecision(12))}`],
                  [t('请求规则倍率'), `× ${Number(result.multiplier.toPrecision(12))}`],
                  [t('模拟总费用（美元）'), `$${Number(result.cost.toPrecision(12))}`],
                  [t('命中档位'), result.tier || t('无档位')],
                  [t('本次试算时间'), result.time],
                ].map(([label, value]) => (
                  <div key={label} className='min-w-0'>
                    <dt className='text-muted-foreground text-xs'>{label}</dt>
                    <dd className='font-mono break-words'>{value}</dd>
                  </div>
                ))}
              </dl>
            ) : result ? <p role='alert' className='text-destructive'>{errors[result.error] || errors.expression}</p> : null}
          </div>
        </div>
      )}
    </details>
  )
}

export function TieredPricingEditor(props: ComponentProps<typeof PricingEditor>) {
  return (
    <>
      <PricingEditor {...props} />
      <RequestSimulation key={props.modelName} expression={props.billingExpr} rules={props.requestRuleExpr} />
    </>
  )
}
