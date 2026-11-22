import React, { useEffect, useState } from 'react';
import { Button, Form, Typography, useFormApi, useFormState } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API } from '../../../../helpers';

export default function AutoGroupsControl({ onPolicy }) {
  const { t } = useTranslation();
  const formApi = useFormApi();
  const { values } = useFormState();
  const [policy, setPolicy] = useState(null);
  const [loading, setLoading] = useState(true);
  const [retry, setRetry] = useState(0);
  const groups = (values.auto_groups_text || '').split('\n').map((group) => group.trim()).filter(Boolean);
  useEffect(() => {
    let active = true;
    setLoading(true);
    setPolicy(null);
    onPolicy(null);
    API.get('/api/token/auto-groups', { skipErrorHandler: true }).then(({ data: response }) => {
      const data = response?.data;
      if (!response?.success || !Array.isArray(data?.groups) || data.groups.some((group) => typeof group !== 'string' || !group || group === 'auto') || !Number.isSafeInteger(data.max_count) || data.max_count < 1) throw new Error('Invalid auto group policy');
      if (active) {
        setPolicy(data);
        onPolicy(data.max_count);
      }
    }).catch(() => { /* The inline error avoids exposing server internals. */ }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; onPolicy(null); };
  }, [retry, onPolicy]);

  return <div className='space-y-2'>
    <Form.TextArea
      field='auto_groups_text'
      label={t('自动分组顺序')}
      extraText={t('每行一个分组，留空继承站点配置；按填写顺序重试。')}
      rows={3}
    />
    <div aria-live='polite' className='text-xs break-all space-y-2'>
      <Typography.Text type={policy && groups.length > policy.max_count ? 'danger' : 'tertiary'}>
        {policy ? t('已填写 {{count}} 个分组，上限 {{max}} 个', { count: groups.length, max: policy.max_count }) : t('已填写 {{count}} 个分组', { count: groups.length })}
      </Typography.Text>
      <p>{groups.length ? t('当前使用自定义顺序') : t('当前继承全局自动分组')}</p>
      <p>{t('全局自动分组顺序')}：{loading ? t('正在加载自动分组配置…') : policy ? (policy.groups.length ? policy.groups.join(' → ') : t('全局未配置可用自动分组')) : t('加载自动分组配置失败；可保存继承配置，自定义顺序需重试成功')}</p>
      {!loading && !policy && <Button htmlType='button' size='small' onClick={() => setRetry((value) => value + 1)}>{t('重试加载自动分组')}</Button>}
      <Button htmlType='button' size='small' disabled={!groups.length && !values.auto_groups_text} onClick={() => formApi.setValue('auto_groups_text', '')}>{t('恢复全局自动分组')}</Button>
    </div>
  </div>;
}
