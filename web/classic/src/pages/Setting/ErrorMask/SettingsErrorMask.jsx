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
import { Banner, Button, Col, Form, Row, Spin, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import {
  API,
  compareObjects,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import ErrorMaskRules from './components/ErrorMaskRules';

const { Text } = Typography;

export default function SettingsErrorMask(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    'error_mask_setting.enabled': false,
    'error_mask_setting.rules': '',
  });
  const [inputsRow, setInputsRow] = useState(inputs);
  const [dataVersion, setDataVersion] = useState(0);
  const refForm = useRef();

  function handleFieldChange(field) {
    return (value) => {
      setInputs((prev) => ({ ...prev, [field]: value }));
    };
  }

  function onRulesChange(jsonStr) {
    setInputs((prev) => ({ ...prev, 'error_mask_setting.rules': jsonStr }));
  }

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      let value = '';
      if (typeof inputs[item.key] === 'boolean') {
        value = String(inputs[item.key]);
      } else {
        value = String(inputs[item.key] ?? '');
      }
      return API.put('/api/option/', { key: item.key, value });
    });
    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (requestQueue.length === 1) {
          if (res.includes(undefined)) return;
        } else if (requestQueue.length > 1) {
          if (res.includes(undefined))
            return showError(t('部分保存失败，请重试'));
        }
        showSuccess(t('保存成功'));
        props.refresh?.();
      })
      .catch(() => {
        showError(t('保存失败，请重试'));
      })
      .finally(() => {
        setLoading(false);
      });
  }

  useEffect(() => {
    const currentInputs = {};
    for (let key in props.options) {
      if (Object.keys(inputs).includes(key)) {
        if (typeof inputs[key] === 'boolean') {
          currentInputs[key] =
            props.options[key] === 'true' || props.options[key] === true;
        } else {
          currentInputs[key] = props.options[key];
        }
      }
    }
    const merged = { ...inputs, ...currentInputs };
    setInputs(merged);
    setInputsRow(merged);
    setDataVersion((v) => v + 1);
    if (refForm.current) {
      refForm.current.setValues(merged);
    }
  }, [props.options]);

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(formAPI) => (refForm.current = formAPI)}
        style={{ marginBottom: 15 }}
      >
        <Form.Section text={t('错误掩码')}>
          <Banner
            type='info'
            description={t(
              '错误掩码用于在上游返回错误时按规则替换错误信息，避免向终端用户暴露敏感细节，并可同时覆盖响应的 HTTP 状态码。规则按从上到下顺序匹配，首条命中即应用。',
            )}
            style={{ marginBottom: 16 }}
          />
          <Row gutter={16} style={{ marginBottom: 12 }}>
            <Col xs={24} sm={12} md={8} lg={8} xl={8}>
              <Form.Switch
                field={'error_mask_setting.enabled'}
                label={t('启用错误掩码')}
                extraText={t('关闭时所有规则不生效，零开销')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={handleFieldChange('error_mask_setting.enabled')}
              />
            </Col>
          </Row>

          <div style={{ marginBottom: 12 }}>
            <Text strong>{t('可用占位符：')}</Text>
            <Text type='tertiary' size='small'>
              {'{message} {code} {type} {param} {status} {channel_id} {channel_name} {model} {request_id}'}
            </Text>
          </div>

          <ErrorMaskRules
            key={`emr_${dataVersion}`}
            value={inputs['error_mask_setting.rules']}
            onChange={onRulesChange}
          />

          <Row style={{ marginTop: 16 }}>
            <Button size='default' onClick={onSubmit}>
              {t('保存错误掩码设置')}
            </Button>
          </Row>
        </Form.Section>
      </Form>
    </Spin>
  );
}
