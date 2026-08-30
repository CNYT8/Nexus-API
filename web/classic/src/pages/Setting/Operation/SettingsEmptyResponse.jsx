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
import { Button, Card, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import { API, showError, showSuccess, showWarning } from '../../../helpers';
import { useTranslation } from 'react-i18next';

const defaultSettings = {
  enabled: false,
  period_days: 3,
  refund_percent: 70,
};

const normalize = (value) => ({
  enabled: Boolean(value?.enabled),
  period_days: Math.min(30, Math.max(1, Number(value?.period_days) || 3)),
  refund_percent: Math.min(
    100,
    Math.max(1, Number(value?.refund_percent) || 70),
  ),
});

export default function SettingsEmptyResponse() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultSettings);
  const original = useRef(JSON.stringify(defaultSettings));
  const formRef = useRef();

  const refresh = async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/empty-responses/settings', {
        disableDuplicate: true,
      });
      if (!res.data?.success) {
        showError(res.data?.message || t('加载空回赔付设置失败'));
        return;
      }
      const next = normalize(res.data.data);
      setInputs(next);
      original.current = JSON.stringify(next);
      formRef.current?.setValues(next);
    } catch (error) {
      showError(t('加载空回赔付设置失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const update = (key, value) => {
    setInputs((current) => ({ ...current, [key]: value }));
  };

  const submit = async () => {
    const next = normalize(inputs);
    if (JSON.stringify(next) === original.current) {
      showWarning(t('你似乎并没有修改什么'));
      return;
    }
    setLoading(true);
    try {
      const res = await API.put('/api/empty-responses/settings', next);
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
      <Card style={{ marginTop: '10px' }}>
        <Form values={inputs} getFormApi={(api) => (formRef.current = api)}>
          <Form.Section text={t('空回赔付设置')}>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8}>
                <Form.Switch
                  field='enabled'
                  label={t('启用空回赔付')}
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) => update('enabled', value)}
                />
              </Col>
              <Col xs={24} sm={12} md={8}>
                <Form.InputNumber
                  field='period_days'
                  label={t('记录周期')}
                  min={1}
                  max={30}
                  suffix={t('天')}
                  onChange={(value) => update('period_days', value)}
                />
              </Col>
              <Col xs={24} sm={12} md={8}>
                <Form.InputNumber
                  field='refund_percent'
                  label={t('赔付百分比')}
                  min={1}
                  max={100}
                  suffix='%'
                  onChange={(value) => update('refund_percent', value)}
                />
              </Col>
            </Row>
            <Button onClick={submit}>{t('保存空回赔付设置')}</Button>
          </Form.Section>
        </Form>
      </Card>
    </Spin>
  );
}
