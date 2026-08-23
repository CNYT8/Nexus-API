import React, { useEffect, useRef, useState } from 'react';
import { Button, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import { API, compareObjects, showError, showSuccess, showWarning } from '../../../helpers';
import { useTranslation } from 'react-i18next';

export default function SettingsOutputSensitiveWords({ options, refresh }) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({ OutputSensitiveEnabled: false, OutputSensitiveAction: 'truncate', OutputSensitiveMatchPercent: 20, OutputSensitiveWords: '' });
  const original = useRef(inputs);
  const formRef = useRef();

  useEffect(() => {
    const next = {
      OutputSensitiveEnabled: !!options.OutputSensitiveEnabled,
      OutputSensitiveAction: options.OutputSensitiveAction === 'error' ? 'error' : 'truncate',
      OutputSensitiveMatchPercent: Math.min(100, Math.max(1, Number(options.OutputSensitiveMatchPercent) || 20)),
      OutputSensitiveWords: options.OutputSensitiveWords || '',
    };
    setInputs(next);
    original.current = structuredClone(next);
    formRef.current?.setValues(next);
  }, [options]);

  const update = (key, value) => setInputs((current) => ({ ...current, [key]: value }));
  const onSubmit = async () => {
    const changes = compareObjects(original.current, inputs);
    if (!changes.length) return showWarning(t('你似乎并没有修改什么'));
    setLoading(true);
    try {
      await Promise.all(changes.map(({ key }) => API.put('/api/option/', { key, value: String(inputs[key] ?? '') })));
      showSuccess(t('保存成功'));
      refresh();
    } catch (error) {
      showError(t('保存失败，请重试'));
    } finally { setLoading(false); }
  };

  return <Spin spinning={loading}>
    <Form values={inputs} getFormApi={(api) => { formRef.current = api; }}>
      <Form.Section text={t('输出敏感词设置')}>
        <Row gutter={16}>
          <Col xs={24} sm={12} md={8}>
            <Form.Switch field='OutputSensitiveEnabled' label={t('启用输出敏感词')} checkedText='｜' uncheckedText='〇' onChange={(value) => update('OutputSensitiveEnabled', value)} />
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.InputNumber field='OutputSensitiveMatchPercent' label={t('命中阈值百分比')} min={1} max={100} suffix='%' onChange={(value) => update('OutputSensitiveMatchPercent', value)} />
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Select field='OutputSensitiveAction' label={t('命中后的处理')} optionList={[{ value: 'truncate', label: t('立即截断') }, { value: 'error', label: t('报错并停止') }]} onChange={(value) => update('OutputSensitiveAction', value)} />
          </Col>
        </Row>
        <Form.TextArea field='OutputSensitiveWords' label={t('输出敏感词列表')} extraText={t('支持几千字长文本；一行一个规则，可跨流式分片匹配。未开启时不会扫描输出。')} placeholder={t('粘贴输出敏感词或整段文本，一行一个规则')} onChange={(value) => update('OutputSensitiveWords', value)} style={{ fontFamily: 'JetBrains Mono, Consolas' }} autosize={{ minRows: 8, maxRows: 18 }} />
        <Button size='default' onClick={onSubmit}>{t('保存输出敏感词设置')}</Button>
      </Form.Section>
    </Form>
  </Spin>;
}
