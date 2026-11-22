import React from 'react';
import { Form } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import ChannelProviderSettings from './ChannelProviderSettings';
import ChannelDefaultBaseURL from './ChannelDefaultBaseURL';

export default function ChannelHTTPSettings({ settings, onChange }) {
  const { t } = useTranslation();
  return (
    <div className='space-y-3'>
      <ChannelDefaultBaseURL />
      <ChannelProviderSettings onChange={onChange} />
      <p className='text-xs text-gray-500'>
        {t('Applies to shared HTTP relay requests; SDK and WebSocket connections are not included.')}
      </p>
      <Form.Select
        field='http_protocol'
        label={t('HTTP Protocol')}
        optionList={[
          { value: 'auto', label: t('Auto') },
          { value: 'http1', label: t('HTTP/1.1') },
        ]}
        onChange={(value) => {
          onChange('http_protocol', value);
          if (value === 'http1') onChange('http2_connection_shards', 1);
        }}
        style={{ width: '100%' }}
      />
      <Form.Select
        field='http2_connection_shards'
        label={t('HTTP/2 Connection Shards')}
        disabled={settings.http_protocol === 'http1'}
        optionList={Array.from({ length: 8 }, (_, index) => ({
          value: index + 1,
          label: String(index + 1),
        }))}
        onChange={(value) => onChange('http2_connection_shards', value)}
        style={{ width: '100%' }}
      />
      <Form.Switch
        field='tls_insecure_skip_verify'
        label={t('Skip Upstream TLS Verification')}
        onChange={(value) => onChange('tls_insecure_skip_verify', value)}
        extraText={t('Accept upstream certificates that fail validation, such as self-signed ones. Only enable for trusted internal upstreams.')}
      />
    </div>
  );
}
