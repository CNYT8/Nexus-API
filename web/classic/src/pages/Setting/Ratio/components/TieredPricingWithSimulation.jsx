import React, { useId, useMemo, useState } from 'react';
import { Button, Input, TextArea } from '@douyinfe/semi-ui';
import TieredPricingEditor from './TieredPricingEditor';
import { simulateRequest } from './request-simulation';

function RequestSimulation({ expression, rules, t }) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [bodyText, setBodyText] = useState('{}');
  const [headersText, setHeadersText] = useState('{}');
  const [timeText, setTimeText] = useState('');
  const [variablesText, setVariablesText] = useState('{"p":1000,"c":500}');
  const [currentTime, setCurrentTime] = useState(() => new Date().toISOString());
  const result = useMemo(
    () => open ? simulateRequest({ expression, rules, bodyText, headersText, timeText: timeText || currentTime, variablesText }) : null,
    [open, expression, rules, bodyText, headersText, timeText, variablesText, currentTime],
  );
  const errors = {
    empty: t('请先填写计费表达式'),
    body: t('请求体必须是 JSON 对象'),
    headers: t('请求头必须是值均为字符串的 JSON 对象'),
    time: t('指定时间须为包含时区的有效 ISO 时间'),
    variables: t('用量变量必须是非负安全整数，仅支持下方列出的变量'),
    path: t('本地模拟不支持此参数路径或对象运算，请使用简单字段路径'),
    local_zone: t('本地模拟无法确定服务器 Local 时区，请改用明确的时区名称'),
    result: t('计费结果或请求规则倍率无效，必须为有限非负数'),
    expression: t('表达式无效或包含本地模拟不支持的语法'),
  };

  return (
    <details
      className='mt-4 rounded-lg border p-3'
      onToggle={(event) => {
        setOpen(event.currentTarget.open);
        if (event.currentTarget.open) setCurrentTime(new Date().toISOString());
      }}
    >
      <summary className='cursor-pointer font-medium'>{t('请求情境模拟')}</summary>
      {open && (
        <div className='mt-3 space-y-3'>
          <p className='text-xs text-gray-500'>
            {t('仅在本地试算当前表达式与请求规则，不发送请求；费用为分组倍率前的美元金额，模拟输入不会保存。')}
          </p>
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='min-w-0'>
              <label htmlFor={`${id}-body`} className='mb-1 block text-sm'>{t('请求体（JSON）')}</label>
              <TextArea id={`${id}-body`} value={bodyText} onChange={setBodyText} rows={5} maxLength={65536} style={{ fontFamily: 'monospace' }} />
            </div>
            <div className='min-w-0'>
              <label htmlFor={`${id}-headers`} className='mb-1 block text-sm'>{t('请求头（JSON）')}</label>
              <TextArea id={`${id}-headers`} value={headersText} onChange={setHeadersText} rows={5} maxLength={65536} style={{ fontFamily: 'monospace' }} />
            </div>
          </div>
          <div>
            <label htmlFor={`${id}-time`} className='mb-1 block text-sm'>{t('指定请求时间')}</label>
            <Input id={`${id}-time`} value={timeText} onChange={setTimeText} placeholder='2026-11-21T18:00:00+08:00' showClear />
            <p className='mt-1 text-xs text-gray-500'>{t('留空使用本次试算的当前时间；时区由表达式中的时间函数指定。')}</p>
          </div>
          <div>
            <label htmlFor={`${id}-variables`} className='mb-1 block text-sm'>{t('用量变量（JSON）')}</label>
            <TextArea id={`${id}-variables`} value={variablesText} onChange={setVariablesText} rows={3} maxLength={65536} style={{ fontFamily: 'monospace' }} />
            <p className='mt-1 text-xs text-gray-500'>{t('填写归一化后的用量；未填写的变量为 0，len 默认合计输入项，也可显式指定。')}</p>
            <code className='block break-words text-xs'>p, c, len, cr, cc, cc1h, cc12h, img, img_o, ai, ao</code>
          </div>
          <Button htmlType='button' size='small' onClick={() => setCurrentTime(new Date().toISOString())}>{t('重新试算')}</Button>
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
                    <dt className='text-xs text-gray-500'>{label}</dt>
                    <dd className='break-words font-mono'>{value}</dd>
                  </div>
                ))}
              </dl>
            ) : result ? <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>{errors[result.error] || errors.expression}</p> : null}
          </div>
        </div>
      )}
    </details>
  );
}

export default function TieredPricingWithSimulation(props) {
  return (
    <>
      <TieredPricingEditor {...props} />
      <RequestSimulation key={props.model?.name} expression={props.model?.billingExpr || ''} rules={props.requestRuleExpr || ''} t={props.t} />
    </>
  );
}
