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

import React, { useEffect, useState, useRef } from 'react';
import { Button, Col, Form, Row, Spin, Tag } from '@douyinfe/semi-ui';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

export default function SettingsSensitiveWords(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    CheckSensitiveEnabled: false,
    CheckSensitiveOnPromptEnabled: false,
    OutputSensitiveEnabled: false,
    OutputSensitiveAction: 'truncate',
    OutputSensitiveMatchPercent: 20,
    OutputSensitiveWords: '',
    SensitiveWords: '',
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      let value = '';
      if (typeof inputs[item.key] === 'boolean') {
        value = String(inputs[item.key]);
      } else {
        value = inputs[item.key];
      }
      return API.put('/api/option/', {
        key: item.key,
        value,
      });
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
        props.refresh();
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
        currentInputs[key] = props.options[key];
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current.setValues(currentInputs);
  }, [props.options]);
  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('屏蔽词过滤设置')}>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'CheckSensitiveEnabled'}
                  label={t('启用屏蔽词过滤功能')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      CheckSensitiveEnabled: value,
                    });
                  }}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'OutputSensitiveEnabled'}
                  label={t('启用输出敏感词')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) => setInputs({ ...inputs, OutputSensitiveEnabled: value })}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'CheckSensitiveOnPromptEnabled'}
                  label={t('启用 Prompt 检查')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      CheckSensitiveOnPromptEnabled: value,
                    })
                  }
                />
              </Col>
            </Row>
            <Row>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field='OutputSensitiveMatchPercent'
                  label={t('命中阈值百分比')}
                  min={1}
                  max={100}
                  suffix='%'
                  onChange={(value) => setInputs({ ...inputs, OutputSensitiveMatchPercent: value })}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Select
                  field='OutputSensitiveAction'
                  label={t('命中后的处理')}
                  optionList={[{ value: 'truncate', label: t('立即截断') }, { value: 'error', label: t('报错并停止') }]}
                  onChange={(value) => setInputs({ ...inputs, OutputSensitiveAction: value })}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.TextArea
                  label={t('输出敏感词列表')}
                  extraText={t('支持几千字长文本；一行一个规则，可跨流式分片匹配')}
                  placeholder={t('粘贴输出敏感词或整段文本，一行一个规则')}
                  field='OutputSensitiveWords'
                  onChange={(value) => setInputs({ ...inputs, OutputSensitiveWords: value })}
                  style={{ fontFamily: 'JetBrains Mono, Consolas' }}
                  autosize={{ minRows: 6, maxRows: 12 }}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.TextArea
                  label={t('屏蔽词列表')}
                  extraText={t('一行一个屏蔽词，不需要符号分割')}
                  placeholder={t('一行一个屏蔽词，不需要符号分割')}
                  field={'SensitiveWords'}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      SensitiveWords: value,
                    })
                  }
                  style={{ fontFamily: 'JetBrains Mono, Consolas' }}
                  autosize={{ minRows: 6, maxRows: 12 }}
                />
              </Col>
            </Row>
            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存屏蔽词过滤设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
