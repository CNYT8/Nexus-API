import React from 'react';
import { Popover, Tag } from '@douyinfe/semi-ui';
import { readResponseModel, isResponseModelMismatch } from './response-model';

export default function ResponseModelObservation({ other, isAdmin, t }) {
  const observation = readResponseModel(other, isAdmin);
  if (!observation) return null;
  const mismatch = isResponseModelMismatch(observation);
  return (
    <Popover
      trigger='click'
      position='bottomLeft'
      content={
        <div style={{ padding: 12, maxWidth: 'min(360px, 85vw)', overflowWrap: 'anywhere' }}>
          <div style={{ fontWeight: 600 }}>{t('响应模型观测')}</div>
          <dl style={{ margin: '8px 0' }}>
            <dt>{t('请求模型')}</dt><dd>{observation.requested_model || '—'}</dd>
            <dt>{t('上游模型')}</dt><dd>{observation.upstream_model || '—'}</dd>
            <dt>{t('实际响应模型')}</dt><dd>{observation.returned_model}</dd>
          </dl>
          <div style={{ color: 'var(--semi-color-text-2)', fontSize: 12 }}>
            {t('仅记录上游响应声明的模型名称，不代表模型身份验证')}
          </div>
        </div>
      }
    >
      <button
        type='button'
        aria-label={t('响应模型观测')}
        onClick={(event) => event.stopPropagation()}
        style={{ border: 0, padding: 0, background: 'transparent', cursor: 'pointer' }}
      >
        <Tag color={mismatch ? 'orange' : 'blue'} size='small'>
          {mismatch ? t('响应模型不匹配') : t('响应模型观测')}
        </Tag>
      </button>
    </Popover>
  );
}
