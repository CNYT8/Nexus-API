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
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import i18next from 'i18next';
import {
  API,
  showError,
  showSuccess,
  showWarning,
  toBoolean,
} from '../../../helpers';
import { microsToMoney, moneyToMicros } from './rebate-money';

const defaults = {
  enabled: false,
  countDuringClosure: false,
  ratePercent: 0,
  payoutMode: 'immediate',
  payoutHour: 7,
  payoutMinute: 0,
  payoutIntervalHours: 24,
  siteTimezone: 'UTC',
  inviterLimitPeriod: 'monthly',
  inviteeCap: '0',
  inviterEligibleCap: '0',
  inviterMinPaid: '0',
  payoutHoldHours: 0,
};
const moneyFields = new Set([
  'inviteeCap',
  'inviterEligibleCap',
  'inviterMinPaid',
]);
const numberFields = new Set([
  'ratePercent',
  'payoutHour',
  'payoutMinute',
  'payoutIntervalHours',
  'payoutHoldHours',
  ...moneyFields,
]);
const optionKey = {
  enabled: 'enabled',
  countDuringClosure: 'count_during_closure',
  ratePercent: 'rate_basis_points',
  payoutMode: 'payout_mode',
  payoutHour: 'payout_hour',
  payoutMinute: 'payout_minute',
  payoutIntervalHours: 'payout_interval_hours',
  siteTimezone: 'site_timezone',
  inviterLimitPeriod: 'inviter_limit_period',
  inviteeCap: 'invitee_eligible_money_limit_micros',
  inviterEligibleCap: 'inviter_eligible_money_limit_micros',
  inviterMinPaid: 'inviter_min_paid_money_micros',
  payoutHoldHours: 'payout_hold_hours',
};
const labels = {
  inviteeCap: '每个被邀请人终身消费基数上限',
  inviterEligibleCap: '直属下级消费基数上限',
  inviterMinPaid: '邀请人返利门槛（累计合格充值金额）',
};

const toDisplayValue = (field, value) => {
  if (moneyFields.has(field)) return microsToMoney(String(value));
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return 0;
  if (field === 'ratePercent') return numeric / 100;
  return numeric;
};

const toStoredValue = (field, value) => {
  if (moneyFields.has(field)) return moneyToMicros(value);
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric < 0) return null;
  if (field === 'ratePercent') return Math.round(numeric * 100);
  return numeric;
};

const readRebateOptions = (options) => {
  const next = { ...defaults };
  Object.keys(optionKey).forEach((field) => {
    const raw = options?.[optionKey[field]];
    if (raw === undefined) return;
    if (field === 'enabled' || field === 'countDuringClosure') {
      next[field] = toBoolean(raw);
    } else if (numberFields.has(field)) {
      next[field] = toDisplayValue(field, raw);
    } else {
      next[field] = String(raw);
    }
  });
  return next;
};

export default function SettingsRebate() {
  const { t } = useTranslation();
  const [inputs, setInputs] = useState(defaults);
  const [original, setOriginal] = useState(defaults);
  const [loading, setLoading] = useState(true);
  const [reloadRequired, setReloadRequired] = useState(true);
  const pending = useRef(false);
  const baselineUnavailable = useRef(true);
  const baseline = useRef(null);
  const unsavedChanges = useRef({});
  const refForm = useRef();
  const reloadController = useRef(null);

  const update = (field, value) => {
    if (pending.current || baselineUnavailable.current) return;
    if (value === original[field]) {
      delete unsavedChanges.current[field];
    } else {
      unsavedChanges.current[field] = value;
    }
    setInputs((current) => ({ ...current, [field]: value }));
  };

  const reloadSavedOptions = useCallback(async (signal) => {
    baselineUnavailable.current = true;
    setReloadRequired(true);
    try {
      const { data } = await API.get('/api/option/rebate', {
        disableDuplicate: true,
        skipErrorHandler: true,
        signal,
      });
      if (signal?.aborted) return;
      if (data?.success !== true || !data.data?.revision || !data.data.fields) {
        throw new Error('Failed to reload saved rebate settings');
      }
      baseline.current = data.data;
      const next = readRebateOptions(data.data.fields);
      const draft = { ...next, ...unsavedChanges.current };
      setInputs(draft);
      setOriginal(structuredClone(next));
      refForm.current?.setValues(draft);
      baselineUnavailable.current = false;
      setReloadRequired(false);
    } catch (_error) {
      if (!signal?.aborted) showError(i18next.t('刷新失败'));
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    reloadController.current = controller;
    pending.current = true;
    setLoading(true);
    void reloadSavedOptions(controller.signal).finally(() => {
      if (controller.signal.aborted) return;
      pending.current = false;
      setLoading(false);
    });
    return () => controller.abort();
  }, [reloadSavedOptions]);

  const retryReload = async () => {
    if (pending.current) return;
    pending.current = true;
    setLoading(true);
    try {
      await reloadSavedOptions(reloadController.current?.signal);
    } finally {
      pending.current = false;
      setLoading(false);
    }
  };

  const onSubmit = async () => {
    if (pending.current || baselineUnavailable.current || !baseline.current)
      return;
    const changed = Object.keys(optionKey).filter(
      (field) => inputs[field] !== original[field],
    );
    if (!changed.length) return showWarning(t('你似乎并没有修改什么'));

    if (
      inputs.ratePercent < 0 ||
      inputs.ratePercent > 100 ||
      inputs.payoutHour < 0 ||
      inputs.payoutHour > 23 ||
      inputs.payoutMinute < 0 ||
      inputs.payoutMinute > 59 ||
      inputs.payoutHoldHours < 0 ||
      inputs.payoutHoldHours > 720 ||
      inputs.payoutIntervalHours < 0 ||
      inputs.payoutIntervalHours > 720 ||
      (inputs.payoutMode === 'custom' && inputs.payoutIntervalHours === 0) ||
      !['immediate', 'scheduled', 'half_day', 'custom'].includes(
        inputs.payoutMode,
      ) ||
      !['daily', 'monthly', 'permanent'].includes(inputs.inviterLimitPeriod)
    ) {
      return showError(t('返利设置数值无效'));
    }

    const fields = { ...baseline.current.fields };
    for (const field of changed) {
      const value = numberFields.has(field)
        ? toStoredValue(field, inputs[field])
        : inputs[field];
      if (
        value === null ||
        (typeof value === 'number' && !Number.isSafeInteger(value))
      ) {
        return showError(t('返利设置数值无效'));
      }
      fields[optionKey[field]] = String(value);
    }

    pending.current = true;
    setLoading(true);
    try {
      const { data } = await API.put(
        '/api/option/rebate',
        {
          fields,
          revision: baseline.current.revision,
        },
        { skipErrorHandler: true },
      );
      if (data?.success !== true || !data.data?.revision || !data.data.fields) {
        throw new Error(data?.message || 'Failed to save rebate settings');
      }
      baseline.current = data.data;
      for (const field of changed) {
        // Only acknowledge this submitted draft; retain any later edits.
        if (unsavedChanges.current[field] === inputs[field]) {
          delete unsavedChanges.current[field];
        }
      }
      const next = readRebateOptions(data.data.fields);
      const draft = { ...next, ...unsavedChanges.current };
      setInputs(draft);
      setOriginal(structuredClone(next));
      refForm.current?.setValues(draft);
      showSuccess(t('保存成功'));
    } catch (_error) {
      // Keep the entire draft, even when the response was lost after commit.
      baselineUnavailable.current = true;
      setReloadRequired(true);
      showError(t('保存失败，请重试'));
    } finally {
      pending.current = false;
      setLoading(false);
    }
  };

  return (
    <Spin spinning={loading}>
      <Form
        disabled={loading || reloadRequired}
        values={inputs}
        getFormApi={(formApi) => {
          refForm.current = formApi;
        }}
        style={{ marginBottom: 15 }}
      >
        <Form.Section text={t('充值返利设置')}>
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <Form.Switch
                field='enabled'
                label={t('启用充值返利')}
                onChange={(value) => update('enabled', value)}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Switch
                field='countDuringClosure'
                label={t('闭站期间计入返利')}
                onChange={(value) => update('countDuringClosure', value)}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.InputNumber
                field='ratePercent'
                label={t('返利比例（百分比）')}
                min={0}
                max={100}
                step={0.01}
                onChange={(value) => update('ratePercent', Number(value))}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Select
                field='payoutMode'
                label={t('发放模式')}
                onChange={(value) => update('payoutMode', value)}
                optionList={[
                  { value: 'immediate', label: t('立即') },
                  { value: 'scheduled', label: t('定时') },
                  { value: 'half_day', label: t('每日两次') },
                  { value: 'custom', label: t('自定义间隔') },
                ]}
              />
            </Col>
            {inputs.payoutMode === 'custom' && (
              <Col xs={24} sm={12} md={8}>
                <Form.InputNumber
                  field='payoutIntervalHours'
                  label={t('发放间隔（小时）')}
                  min={1}
                  max={720}
                  step={1}
                  onChange={(value) =>
                    update('payoutIntervalHours', Number(value))
                  }
                />
              </Col>
            )}
            <Col xs={24} sm={12} md={8}>
              <Form.InputNumber
                field='payoutHoldHours'
                label={t('返利风险冻结（小时）')}
                min={0}
                max={720}
                step={1}
                onChange={(value) => update('payoutHoldHours', Number(value))}
              />
            </Col>
            {inputs.payoutMode !== 'immediate' && (
              <>
                <Col xs={24} sm={12} md={8}>
                  <Form.Input
                    field='siteTimezone'
                    label={t('站点时区')}
                    onChange={(value) => update('siteTimezone', value)}
                  />
                </Col>
                <Col xs={24} sm={12} md={8}>
                  <Form.InputNumber
                    field='payoutHour'
                    label={t('发放小时')}
                    min={0}
                    max={23}
                    step={1}
                    onChange={(value) => update('payoutHour', Number(value))}
                  />
                </Col>
                <Col xs={24} sm={12} md={8}>
                  <Form.InputNumber
                    field='payoutMinute'
                    label={t('发放分钟')}
                    min={0}
                    max={59}
                    step={1}
                    onChange={(value) => update('payoutMinute', Number(value))}
                  />
                </Col>
              </>
            )}
            <Col xs={24} sm={12} md={8}>
              <Form.Select
                field='inviterLimitPeriod'
                label={t('邀请人上限周期')}
                onChange={(value) => update('inviterLimitPeriod', value)}
                optionList={[
                  { value: 'daily', label: t('每日') },
                  { value: 'monthly', label: t('每月') },
                  { value: 'permanent', label: t('永久') },
                ]}
              />
            </Col>
            {Object.keys(labels).map((field) => (
              <Col xs={24} sm={12} md={8} key={field}>
                <Form.Input
                  field={field}
                  label={t(labels[field])}
                  type='text'
                  inputMode='decimal'
                  onChange={(value) => update(field, value)}
                />
              </Col>
            ))}
          </Row>
          <Button disabled={loading || reloadRequired} onClick={onSubmit}>
            {t('保存返利设置')}
          </Button>
          {reloadRequired && !loading && (
            <div className='mt-3 flex items-center gap-3' role='status'>
              <span>{t('刷新失败')}</span>
              <Button onClick={retryReload}>{t('重试')}</Button>
            </div>
          )}
        </Form.Section>
      </Form>
    </Spin>
  );
}
