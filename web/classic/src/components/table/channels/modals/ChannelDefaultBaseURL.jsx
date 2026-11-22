import React, { useEffect, useState } from 'react';
import { useFormState } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API } from '../../../../helpers';

export default function ChannelDefaultBaseURL() {
  const { t } = useTranslation();
  const { values = {} } = useFormState();
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState({ loading: true, failed: false, urls: {} });
  useEffect(() => {
    let active = true;
    setState({ loading: true, failed: false, urls: {} });
    API.get('/api/channel/default_base_urls', { skipErrorHandler: true })
      .then(({ data }) => {
        if (!active) return;
        if (data?.success !== true || !data.data || typeof data.data !== 'object' || Array.isArray(data.data)) {
          throw new Error('Invalid default base URLs response');
        }
        setState({ loading: false, failed: false, urls: data.data });
      })
      .catch(() => { if (active) setState({ loading: false, failed: true, urls: {} }); });
    return () => { active = false; };
  }, [attempt]);
  const value = state.urls[String(values.type)];
  const baseURL = typeof value === 'string' ? value.trim() : '';
  return (
    <div className='space-y-1 text-sm' aria-live='polite'>
      <p className='font-medium'>{t('服务端默认地址')}</p>
      {state.loading ? <p className='text-gray-500'>{t('加载中...')}</p> : state.failed ? (
        <div className='flex flex-wrap items-center gap-2'>
          <span role='alert'>{t('默认地址加载失败')}</span>
          <button type='button' className='underline' onClick={() => setAttempt((value) => value + 1)}>{t('重试')}</button>
        </div>
      ) : <p className='break-all text-gray-500'>{baseURL || t('该渠道类型未提供默认地址')}</p>}
      <p className='text-xs text-gray-500'>{t('仅供参考，不会覆盖已填写的代理地址')}</p>
    </div>
  );
}
