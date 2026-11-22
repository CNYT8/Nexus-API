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
import {
  Button,
  Col,
  Form,
  Row,
  Spin,
  Typography,
  Modal,
} from '@douyinfe/semi-ui';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';

import { useLogCleanupTask } from '../../../hooks/usage-logs/useLogCleanupTask';
import LogPolicySettings from '../../../components/settings/LogPolicySettings';

const { Text } = Typography;

export default function SettingsLog(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const cleanup = useLogCleanupTask();
  const loadingCleanHistoryLog = cleanup.creating || cleanup.active;
  const taskStatus = {
    queued: t('日志清理排队中'),
    running: t('日志清理进行中'),
    succeeded: t('日志清理已完成'),
    failed: t('日志清理失败'),
  };
  const [inputs, setInputs] = useState({
    LogConsumeEnabled: false,
    historyTimestamp: dayjs().subtract(1, 'month').toDate(),
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  async function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow).filter(
      (item) => item.key !== 'historyTimestamp',
    );

    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    setLoading(true);
    try {
      for (const item of updateArray) {
        const value = typeof inputs[item.key] === 'boolean'
          ? String(inputs[item.key])
          : inputs[item.key];
        const response = await API.put('/api/option/', { key: item.key, value });
        if (!response.data.success) {
          throw new Error(response.data.message || t('保存失败，请重试'));
        }
      }
      showSuccess(t('保存成功'));
      await props.refresh();
    } catch (error) {
      showError(error?.message || t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  }
  async function onCleanHistoryLog() {
    if (loadingCleanHistoryLog) return;
    const now = dayjs();
    const targetDate = inputs.historyTimestamp ? dayjs(inputs.historyTimestamp) : null;
    if (!targetDate?.isValid() || targetDate.unix() <= 0 || targetDate.isAfter(now)) {
      showError(t('请选择过去的日志记录时间'));
      return;
    }
    const targetTime = targetDate.format('YYYY-MM-DD HH:mm:ss');
    const currentTime = now.format('YYYY-MM-DD HH:mm:ss');
    const daysDiff = now.diff(targetDate, 'day');

    Modal.confirm({
      title: t('确认清除历史日志'),
      content: (
        <div style={{ lineHeight: '1.8' }}>
          <p>
            <Text>{t('当前时间')}：</Text>
            <Text strong style={{ color: '#52c41a' }}>
              {currentTime}
            </Text>
          </p>
          <p>
            <Text>{t('选择时间')}：</Text>
            <Text strong type='danger'>
              {targetTime}
            </Text>
            {daysDiff > 0 && (
              <Text type='tertiary'>
                {' '}
                ({t('约')} {daysDiff} {t('天前')})
              </Text>
            )}
          </p>
          <div
            style={{
              background: '#fff7e6',
              border: '1px solid #ffd591',
              padding: '12px',
              borderRadius: '4px',
              marginTop: '12px',
              color: '#333',
            }}
          >
            <Text strong style={{ color: '#d46b08' }}>
              ⚠️ {t('注意')}：
            </Text>
            <Text style={{ color: '#333' }}>{t('将删除')} </Text>
            <Text strong style={{ color: '#cf1322' }}>
              {targetTime}
            </Text>
            {daysDiff > 0 && (
              <Text style={{ color: '#8c8c8c' }}>
                {' '}
                ({t('约')} {daysDiff} {t('天前')})
              </Text>
            )}
            <Text style={{ color: '#333' }}> {t('之前的所有日志')}</Text>
          </div>
          <p style={{ marginTop: '12px' }}>
            <Text type='danger'>
              {t('此操作不可恢复，请仔细确认时间后再操作！')}
            </Text>
          </p>
        </div>
      ),
      okText: t('确认删除'),
      cancelText: t('取消'),
      okType: 'danger',
      onOk: async () => {
        try {
          await cleanup.create(targetDate.unix());
        } catch (error) {
          showError(error?.message || t('日志清理失败'));
          throw error;
        }
      },
    });
  }

  useEffect(() => {
    const currentInputs = {};
    for (let key in props.options) {
      if (Object.keys(inputs).includes(key)) {
        currentInputs[key] = props.options[key];
      }
    }
    currentInputs['historyTimestamp'] = inputs.historyTimestamp;
    setInputs(Object.assign(inputs, currentInputs));
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
          <Form.Section text={t('日志设置')}>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'LogConsumeEnabled'}
                  label={t('启用额度消费日志记录')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      LogConsumeEnabled: value,
                    });
                  }}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Spin spinning={cleanup.creating}>
                  <Form.DatePicker
                    label={t('清除历史日志')}
                    field={'historyTimestamp'}
                    type='dateTime'
                    inputReadOnly={true}
                    onChange={(value) => {
                      setInputs({
                        ...inputs,
                        historyTimestamp: value,
                      });
                    }}
                  />
                  <Text
                    type='tertiary'
                    size='small'
                    style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
                  >
                    {t('将清除选定时间之前的所有日志')}
                  </Text>
                  <Button
                    size='default'
                    type='danger'
                    onClick={onCleanHistoryLog}
                    disabled={loadingCleanHistoryLog}
                  >
                    {t('清除历史日志')}
                  </Button>
                </Spin>
                {cleanup.taskId > 0 && (
                  <div className='mt-3 space-y-2 rounded border p-3 text-sm' role='status' aria-live='polite'>
                    <p>{t('日志清理任务 #{{id}}', { id: cleanup.taskId })}</p>
                    {cleanup.task && (
                      <>
                        <p>{taskStatus[cleanup.task.status]}</p>
                        <p>{t('已处理 {{count}} 条日志。', { count: cleanup.task.processed })}</p>
                        <p>{t('清理 {{date}} 之前的日志', { date: dayjs.unix(cleanup.task.target).format('YYYY-MM-DD HH:mm:ss') })}</p>
                        {cleanup.task.error && <p style={{ color: 'var(--semi-color-danger)' }} role='alert'>{cleanup.task.error}</p>}
                      </>
                    )}
                    {cleanup.error && <p style={{ color: 'var(--semi-color-danger)' }} role='alert'>{cleanup.error.message}</p>}
                    {cleanup.active && <p>{t('自动查询五分钟后暂停，请刷新状态继续查看。')}</p>}
                    <Button size='small' disabled={cleanup.fetching} onClick={cleanup.refresh}>
                      {t('刷新状态')}
                    </Button>
                  </div>
                )}
              </Col>
            </Row>

            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存日志设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
      <LogPolicySettings />
    </>
  );
}
