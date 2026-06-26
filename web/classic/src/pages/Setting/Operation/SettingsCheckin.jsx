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
  InputNumber,
  Popconfirm,
  Row,
  Select,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IconChevronDown,
  IconChevronUp,
  IconDelete,
  IconPlus,
} from '@douyinfe/semi-icons';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

let stageIdSeed = 0;
const createStageId = () => `checkin_stage_${++stageIdSeed}`;

const defaultStageRule = () => ({
  _id: createStageId(),
  request_threshold: 0,
  token_threshold: 0,
  allow_checkin: true,
  min_quota: 0,
  max_quota: 0,
});

function parseStageRules(value) {
  if (!value || !String(value).trim()) {
    return [];
  }
  try {
    const parsed = JSON.parse(value);
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed.map((rule) => ({
      _id: createStageId(),
      request_threshold: Number(rule.request_threshold) || 0,
      token_threshold: Number(rule.token_threshold) || 0,
      allow_checkin: rule.allow_checkin !== false,
      min_quota: rule.allow_checkin === false ? 0 : Number(rule.min_quota) || 0,
      max_quota: rule.allow_checkin === false ? 0 : Number(rule.max_quota) || 0,
    }));
  } catch {
    return [];
  }
}

function serializeStageRules(rules) {
  const cleaned = rules
    .map((rule) => ({
      request_threshold: Math.max(0, Number(rule.request_threshold) || 0),
      token_threshold: Math.max(0, Number(rule.token_threshold) || 0),
      allow_checkin: rule.allow_checkin !== false,
      min_quota:
        rule.allow_checkin === false
          ? 0
          : Math.max(0, Number(rule.min_quota) || 0),
      max_quota:
        rule.allow_checkin === false
          ? 0
          : Math.max(0, Number(rule.max_quota) || 0),
    }))
    .filter(
      (rule) =>
        rule.request_threshold > 0 ||
        rule.token_threshold > 0 ||
        rule.allow_checkin === false ||
        rule.min_quota > 0 ||
        rule.max_quota > 0,
    )
    .map((rule) => ({
      ...rule,
      max_quota: Math.max(rule.min_quota, rule.max_quota),
    }));
  return cleaned.length === 0 ? '' : JSON.stringify(cleaned);
}

function StageRulesEditor({ disabled, rules, onChange, t }) {
  const emit = (nextRules) => {
    onChange(nextRules, serializeStageRules(nextRules));
  };

  const updateRule = (id, field, value) => {
    emit(
      rules.map((rule) =>
        rule._id === id ? { ...rule, [field]: value } : rule,
      ),
    );
  };

  const updateRuleCheckin = (id, allowCheckin) => {
    emit(
      rules.map((rule) =>
        rule._id === id
          ? {
              ...rule,
              allow_checkin: allowCheckin,
              min_quota: allowCheckin ? rule.min_quota : 0,
              max_quota: allowCheckin ? rule.max_quota : 0,
            }
          : rule,
      ),
    );
  };

  const removeRule = (id) => {
    emit(rules.filter((rule) => rule._id !== id));
  };

  const moveRule = (index, offset) => {
    const nextIndex = index + offset;
    if (nextIndex < 0 || nextIndex >= rules.length) {
      return;
    }
    const nextRules = [...rules];
    [nextRules[index], nextRules[nextIndex]] = [
      nextRules[nextIndex],
      nextRules[index],
    ];
    emit(nextRules);
  };

  return (
    <div className='space-y-2'>
      <div className='flex items-center justify-between gap-2'>
        <Text strong>{t('阶段规则')}</Text>
        <Text type='tertiary' size='small'>
          {t('从上到下匹配，首个命中阶段生效')}
        </Text>
      </div>
      {rules.length === 0 ? (
        <Text type='tertiary' className='block text-center py-4'>
          {t('暂无阶段规则，添加后按阶段判断')}
        </Text>
      ) : (
        rules.map((rule, index) => (
          <div
            key={rule._id}
            className='flex flex-col gap-2 rounded-lg border p-2 md:flex-row md:items-end'
            style={{ borderColor: 'var(--semi-color-border)' }}
          >
            <div className='flex items-center gap-1 md:w-24'>
              <Tag color='grey' shape='circle'>
                {index + 1}
              </Tag>
              <Button
                icon={<IconChevronUp />}
                theme='borderless'
                size='small'
                disabled={disabled || index === 0}
                onClick={() => moveRule(index, -1)}
              />
              <Button
                icon={<IconChevronDown />}
                theme='borderless'
                size='small'
                disabled={disabled || index === rules.length - 1}
                onClick={() => moveRule(index, 1)}
              />
            </div>
            <div className='flex flex-col gap-1' style={{ flex: 1 }}>
              <Text type='tertiary' size='small'>
                {t('调用超过')}
              </Text>
              <InputNumber
                size='small'
                value={rule.request_threshold}
                min={0}
                disabled={disabled}
                placeholder={t('调用次数')}
                onChange={(value) =>
                  updateRule(rule._id, 'request_threshold', value || 0)
                }
                style={{ width: '100%' }}
              />
            </div>
            <div className='flex flex-col gap-1' style={{ flex: 1 }}>
              <Text type='tertiary' size='small'>
                {t('用量超过')}
              </Text>
              <InputNumber
                size='small'
                value={rule.token_threshold}
                min={0}
                disabled={disabled}
                placeholder={t('Token 用量')}
                onChange={(value) =>
                  updateRule(rule._id, 'token_threshold', value || 0)
                }
                style={{ width: '100%' }}
              />
            </div>
            <div className='flex flex-col gap-1' style={{ width: 128 }}>
              <Text type='tertiary' size='small'>
                {t('结果')}
              </Text>
              <Select
                size='small'
                value={rule.allow_checkin ? 'allow' : 'deny'}
                disabled={disabled}
                onChange={(value) =>
                  updateRuleCheckin(rule._id, value === 'allow')
                }
                style={{ width: '100%' }}
              >
                <Select.Option value='allow'>{t('允许签到')}</Select.Option>
                <Select.Option value='deny'>{t('无法签到')}</Select.Option>
              </Select>
            </div>
            <div className='flex flex-col gap-1' style={{ flex: 1 }}>
              <Text type='tertiary' size='small'>
                {t('最小额度')}
              </Text>
              <InputNumber
                size='small'
                value={rule.min_quota}
                min={0}
                disabled={disabled || !rule.allow_checkin}
                placeholder={t('最小额度')}
                onChange={(value) =>
                  updateRule(rule._id, 'min_quota', value || 0)
                }
                style={{ width: '100%' }}
              />
            </div>
            <div className='flex flex-col gap-1' style={{ flex: 1 }}>
              <Text type='tertiary' size='small'>
                {t('最大额度')}
              </Text>
              <InputNumber
                size='small'
                value={rule.max_quota}
                min={0}
                disabled={disabled || !rule.allow_checkin}
                placeholder={t('最大额度')}
                onChange={(value) =>
                  updateRule(rule._id, 'max_quota', value || 0)
                }
                style={{ width: '100%' }}
              />
            </div>
            <Popconfirm
              title={t('确定删除该阶段？')}
              onConfirm={() => removeRule(rule._id)}
              disabled={disabled}
            >
              <Button
                icon={<IconDelete />}
                type='danger'
                theme='borderless'
                size='small'
                disabled={disabled}
              />
            </Popconfirm>
          </div>
        ))
      )}
    </div>
  );
}

export default function SettingsCheckin(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    'checkin_setting.enabled': false,
    'checkin_setting.condition_enabled': false,
    'checkin_setting.request_threshold': 0,
    'checkin_setting.token_threshold': 0,
    'checkin_setting.stage_rules': '',
    'checkin_setting.min_quota': 1000,
    'checkin_setting.max_quota': 10000,
  });
  const [stageRules, setStageRules] = useState([]);
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  function handleFieldChange(fieldName) {
    return (value) => {
      setInputs((inputs) => ({ ...inputs, [fieldName]: value }));
    };
  }

  function handleStageRulesChange(nextRules, jsonValue) {
    setStageRules(nextRules);
    setInputs((inputs) => ({
      ...inputs,
      'checkin_setting.stage_rules': jsonValue,
    }));
  }

  function updateStageRules(nextRules) {
    handleStageRulesChange(nextRules, serializeStageRules(nextRules));
  }

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      let value = '';
      if (typeof inputs[item.key] === 'boolean') {
        value = String(inputs[item.key]);
      } else {
        value = String(inputs[item.key]);
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
    const mergedInputs = {
      ...inputs,
      ...currentInputs,
    };
    setInputs(mergedInputs);
    setInputsRow(structuredClone(mergedInputs));
    setStageRules(parseStageRules(mergedInputs['checkin_setting.stage_rules']));
    refForm.current?.setValues(mergedInputs);
  }, [props.options]);

  const checkinEnabled = inputs['checkin_setting.enabled'];
  const stageEnabled =
    checkinEnabled && inputs['checkin_setting.condition_enabled'];

  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('签到设置')}>
            <Typography.Text
              type='tertiary'
              style={{ marginBottom: 16, display: 'block' }}
            >
              {t('签到功能允许用户每日签到获取额度奖励')}
            </Typography.Text>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'checkin_setting.enabled'}
                  label={t('启用签到功能')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('checkin_setting.enabled')}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'checkin_setting.condition_enabled'}
                  label={t('启用阶段签到')}
                  extraText={t(
                    '按前一天调用次数或 Token 用量命中阶段，可设置固定额度、随机额度或无法签到',
                  )}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange(
                    'checkin_setting.condition_enabled',
                  )}
                  disabled={!checkinEnabled}
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'checkin_setting.min_quota'}
                  label={t('默认最小额度')}
                  placeholder={t('未启用阶段签到时使用')}
                  onChange={handleFieldChange('checkin_setting.min_quota')}
                  min={0}
                  disabled={!checkinEnabled}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'checkin_setting.max_quota'}
                  label={t('默认最大额度')}
                  placeholder={t('未启用阶段签到时使用')}
                  onChange={handleFieldChange('checkin_setting.max_quota')}
                  min={0}
                  disabled={!checkinEnabled}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'checkin_setting.request_threshold'}
                  label={t('兼容调用阈值')}
                  placeholder={t('无阶段规则时生效')}
                  onChange={handleFieldChange(
                    'checkin_setting.request_threshold',
                  )}
                  min={0}
                  disabled={!stageEnabled || stageRules.length > 0}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'checkin_setting.token_threshold'}
                  label={t('兼容用量阈值')}
                  placeholder={t('无阶段规则时生效')}
                  onChange={handleFieldChange(
                    'checkin_setting.token_threshold',
                  )}
                  min={0}
                  disabled={!stageEnabled || stageRules.length > 0}
                />
              </Col>
            </Row>
            <div style={{ marginTop: 12, marginBottom: 16 }}>
              <StageRulesEditor
                disabled={!stageEnabled}
                rules={stageRules}
                onChange={handleStageRulesChange}
                t={t}
              />
            </div>
            <Row>
              <div className='flex flex-wrap gap-2'>
                <Button size='default' onClick={onSubmit}>
                  {t('保存签到设置')}
                </Button>
                <Button
                  type='tertiary'
                  icon={<IconPlus />}
                  disabled={!stageEnabled}
                  onClick={() =>
                    updateStageRules([...stageRules, defaultStageRule()])
                  }
                >
                  {t('添加阶段')}
                </Button>
              </div>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
