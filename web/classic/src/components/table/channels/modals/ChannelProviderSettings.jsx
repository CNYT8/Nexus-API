import React from 'react';
import { Select, Switch, Typography, useFormApi, useFormState } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

export default function ChannelProviderSettings({ onChange }) {
  const { t } = useTranslation();
  const formApi = useFormApi();
  const { values } = useFormState();
  const type = Number(values.type);
  let settings;
  try {
    settings = JSON.parse(values.settings || '{}');
    if (!settings || typeof settings !== 'object' || Array.isArray(settings)) settings = null;
  } catch { settings = null; }
  const update = (key, value) => {
    if (!settings) return;
    const next = JSON.stringify({ ...settings, [key]: value });
    formApi.setValue('settings', next);
    onChange('settings', next);
  };
  const select = (key, label, fallback, options) => (
    <div className='space-y-2'>
      <Typography.Text>{label}</Typography.Text>
      <Select
        aria-label={label}
        style={{ width: '100%' }}
        disabled={!settings}
        value={typeof settings?.[key] === 'string' ? settings[key] : fallback}
        optionList={options.map((value) => ({ value, label: value === 'invoke_model' ? 'InvokeModel' : value === 'mantle' ? 'Mantle' : value }))}
        onChange={(value) => update(key, value)}
      />
    </div>
  );
  const toggle = (key, label) => (
    <div className='flex items-center justify-between gap-4'>
      <Typography.Text>{label}</Typography.Text>
      <Switch aria-label={label} disabled={!settings} checked={settings?.[key] === true} onChange={(value) => update(key, value)} />
    </div>
  );
  return (
    <div className='space-y-3'>
      <Typography.Text strong>{t('提供商接口设置')}</Typography.Text>
      {!settings && <p role='alert'>{t('请先修正渠道 settings JSON，接口设置暂不可编辑')}</p>}
      {type === 33 && select('aws_api_type', t('AWS 接口类型'), 'invoke_model', ['invoke_model', 'mantle'])}
      {type === 41 && select('vertex_api_version', t('Vertex API 版本'), 'v1', ['v1', 'v1beta1'])}
      {type === 36 && toggle('ollama_openai_chat', t('Ollama 使用 OpenAI 兼容聊天接口'))}
      {toggle('disable_task_polling_sleep', t('关闭任务轮询休眠'))}
      <p className='text-xs text-gray-500'>{t('仅影响对应提供商；未修改的扩展设置会保留')}</p>
    </div>
  );
}
