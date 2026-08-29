/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useRef, useState } from 'react';
import {
  Button,
  Col,
  Form,
  Row,
  Spin,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess, showWarning } from '../../../helpers';
import { useTranslation } from 'react-i18next';

const normalizeConfig = (config) => ({
  enabled: Boolean(config?.enabled),
  action: config?.action === 'error' ? 'error' : 'truncate',
  match_percent: Math.min(
    100,
    Math.max(1, Number(config?.match_percent) || 20),
  ),
  patterns: Array.isArray(config?.patterns) ? [...config.patterns] : [],
});

export default function SettingsOutputSensitiveWords({ config, refresh }) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [newPattern, setNewPattern] = useState('');
  const [inputs, setInputs] = useState(() => normalizeConfig(config));
  const original = useRef(JSON.stringify(normalizeConfig(config)));
  const formRef = useRef();

  useEffect(() => {
    const next = normalizeConfig(config);
    setInputs(next);
    original.current = JSON.stringify(next);
    formRef.current?.setValues(next);
  }, [config]);

  const update = (key, value) => {
    setInputs((current) => ({ ...current, [key]: value }));
  };

  const addPattern = () => {
    const value = newPattern.trim();
    if (!value) return;
    if (!inputs.patterns.includes(value)) {
      update('patterns', [...inputs.patterns, value]);
    }
    setNewPattern('');
  };

  const removePattern = (index) => {
    update(
      'patterns',
      inputs.patterns.filter((_, patternIndex) => patternIndex !== index),
    );
  };

  const onSubmit = async () => {
    const next = normalizeConfig(inputs);
    if (JSON.stringify(next) === original.current) {
      showWarning(t('你似乎并没有修改什么'));
      return;
    }
    setLoading(true);
    try {
      const res = await API.put('/api/option/output-sensitive', next);
      if (!res.data?.success) {
        showError(res.data?.message || t('保存失败，请重试'));
        return;
      }
      showSuccess(t('保存成功'));
      await refresh();
    } catch (error) {
      showError(t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(api) => {
          formRef.current = api;
        }}
      >
        <Form.Section text={t('输出敏感词设置')}>
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <Form.Switch
                field='enabled'
                label={t('启用输出敏感词')}
                checkedText='｜'
                uncheckedText='〇'
                onChange={(value) => update('enabled', value)}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.InputNumber
                field='match_percent'
                label={t('命中阈值百分比')}
                min={1}
                max={100}
                suffix='%'
                onChange={(value) => update('match_percent', value)}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Select
                field='action'
                label={t('命中后的处理')}
                optionList={[
                  { value: 'truncate', label: t('立即截断') },
                  { value: 'error', label: t('报错并停止') },
                ]}
                onChange={(value) => update('action', value)}
              />
            </Col>
          </Row>

          <Typography.Text strong>{t('新增输出敏感词')}</Typography.Text>
          <div
            style={{
              display: 'flex',
              gap: 8,
              alignItems: 'flex-end',
              marginTop: 8,
            }}
          >
            <TextArea
              value={newPattern}
              onChange={setNewPattern}
              placeholder={t('输入 1234 后点击添加，不需要换行')}
              autosize={{ minRows: 2, maxRows: 8 }}
              style={{ flex: 1, fontFamily: 'JetBrains Mono, Consolas' }}
            />
            <Button onClick={addPattern}>{t('添加规则')}</Button>
          </div>

          <Typography.Text strong style={{ display: 'block', marginTop: 18 }}>
            {t('输出敏感词列表')}
          </Typography.Text>
          <div
            style={{
              display: 'flex',
              flexWrap: 'wrap',
              gap: 8,
              marginTop: 8,
              marginBottom: 16,
            }}
          >
            {inputs.patterns.map((pattern, index) => {
              const preview = pattern.replace(/\s+/g, ' ');
              return (
                <Tag
                  key={`${index}-${pattern.length}`}
                  closable
                  onClose={() => removePattern(index)}
                  title={pattern}
                >
                  {preview.length > 80 ? `${preview.slice(0, 80)}...` : preview}
                </Tag>
              );
            })}
          </div>

          <Button size='default' onClick={onSubmit}>
            {t('保存输出敏感词设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}
