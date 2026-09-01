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

import React, { useCallback, useEffect, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Col,
  Empty,
  Row,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IconClock,
  IconAlertTriangle,
} from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import {
  API,
  renderQuota,
  showError,
  showSuccess,
  timestamp2string,
} from '../../helpers';

const { Title, Text } = Typography;

export default function EmptyResponsePage() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [claiming, setClaiming] = useState(false);
  const [status, setStatus] = useState(null);
  const [records, setRecords] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);

  const load = useCallback(
    async (nextPage = page) => {
      setLoading(true);
      try {
        const statusRes = await API.get('/api/empty-responses/self');
        if (!statusRes.data?.success) {
          showError(statusRes.data?.message || t('加载空回检测失败'));
          return;
        }
        setStatus(statusRes.data.data);
        if (!statusRes.data.data?.enabled) {
          setRecords([]);
          setTotal(0);
          setPage(nextPage);
          return;
        }

        const firstListRes = await API.get(
          `/api/empty-responses?p=1&page_size=100`,
        );
        if (!firstListRes.data?.success) {
          showError(firstListRes.data?.message || t('加载空回检测失败'));
          return;
        }
        const listData = firstListRes.data.data;
        const allRecords = [...(listData?.items || [])];
        const pageCount = Math.ceil((listData?.total || 0) / 100);
        for (let listPage = 2; listPage <= pageCount; listPage += 1) {
          const nextListRes = await API.get(
            `/api/empty-responses?p=${listPage}&page_size=100`,
          );
          if (!nextListRes.data?.success) {
            showError(nextListRes.data?.message || t('加载空回检测失败'));
            return;
          }
          allRecords.push(...(nextListRes.data.data?.items || []));
        }
        setRecords(allRecords);
        setTotal(listData?.total || 0);
        setPage(nextPage);
      } catch (error) {
        showError(t('加载空回检测失败'));
      } finally {
        setLoading(false);
      }
    },
    [page, t],
  );

  const claim = async () => {
    setClaiming(true);
    try {
      const res = await API.post('/api/empty-responses/claim');
      if (!res.data?.success) {
        showError(res.data?.message || t('空回赔付失败'));
        return;
      }
      showSuccess(
        t('空回赔付成功，共赔付 {{count}} 条，返还 {{quota}}', {
          count: res.data.data?.created || 0,
          quota: renderQuota(res.data.data?.refund_quota || 0),
        }),
      );
      await load(1);
    } catch (error) {
      showError(t('空回赔付失败'));
    } finally {
      setClaiming(false);
    }
  };

  useEffect(() => {
    load(1);
  }, []);

  const enabled = status?.enabled === true;

  const columns = [
    {
      title: t('时间'),
      dataIndex: 'log_created_at',
      render: (value) => timestamp2string(value),
    },
    {
      title: t('模型'),
      dataIndex: 'model_name',
      render: (value) => <Tag>{value || '-'}</Tag>,
    },
    {
      title: t('输入 Tokens'),
      dataIndex: 'prompt_tokens',
    },
    {
      title: t('当时扣费'),
      dataIndex: 'quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('赔付比例'),
      dataIndex: 'refund_percent',
      render: (value) => `${value}%`,
    },
    {
      title: t('预计赔付'),
      dataIndex: 'refund_quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      render: (value) =>
        value === 'refunded' ? (
          <Tag color='green'>{t('已赔付')}</Tag>
        ) : value === 'expired' ? (
          <Tag color='grey'>{t('已过期')}</Tag>
        ) : (
          <Tag color='orange'>{t('待赔付')}</Tag>
        ),
    },
  ];

  return (
    <Spin spinning={loading} size='large'>
      <div className='h-full min-h-0 w-full min-w-0 overflow-y-auto pt-16'>
        <div className='mx-auto w-full min-w-0 max-w-7xl px-3 py-4'>
          <Space vertical spacing='medium' style={{ width: '100%' }}>
            <Row
              align='middle'
              justify='space-between'
              type='flex'
              style={{ width: '100%', flexWrap: 'wrap', gap: 12 }}
            >
              <div style={{ minWidth: 0, flex: '1 1 320px' }}>
                <Title heading={3}>{t('空回检测')}</Title>
                <Text type='tertiary'>
                  {t(
                    '检测最近 {{days}} 天内有输入但没有生成输出的消费日志，赔付按日志当时的扣费金额计算。',
                    { days: status?.period_days || 3 },
                  )}
                </Text>
              </div>
              <Button
                theme='solid'
                type='primary'
                icon={<IconAlertTriangle />}
                loading={claiming}
                disabled={!enabled || claiming}
                onClick={claim}
              >
                {t('赔付')}
              </Button>
            </Row>

            {!enabled && (
              <Banner
                type='info'
                icon={<IconClock />}
                description={t('空回赔付功能当前未开启，请联系管理员。')}
              />
            )}

            <Row gutter={16}>
              <Col xs={24} sm={12} md={6}>
                <Card>
                  <Text type='tertiary'>{t('当前空回记录')}</Text>
                  <Title heading={2}>{status?.pending_count || 0}</Title>
                </Card>
              </Col>
              <Col xs={24} sm={12} md={6}>
                <Card>
                  <Text type='tertiary'>{t('预计赔付额度')}</Text>
                  <Title heading={2}>
                    {renderQuota(status?.pending_quota || 0)}
                  </Title>
                </Card>
              </Col>
              <Col xs={24} sm={12} md={6}>
                <Card>
                  <Text type='tertiary'>{t('已过期记录')}</Text>
                  <Title heading={2}>{status?.expired_count || 0}</Title>
                </Card>
              </Col>
              <Col xs={24} sm={12} md={6}>
                <Card>
                  <Text type='tertiary'>{t('记录周期')}</Text>
                  <Title heading={2}>
                    {t('{{days}} 天', { days: status?.period_days || 3 })}
                  </Title>
                </Card>
              </Col>
            </Row>

            <Card style={{ minWidth: 0, overflow: 'hidden' }}>
              <div className='hidden w-full min-w-0 overflow-x-auto sm:block'>
                <Table
                  columns={columns}
                  dataSource={records}
                  loading={loading}
                  empty={<Empty title={t('暂无空回记录')} />}
                  scroll={{ x: 'max-content' }}
                />
              </div>
              <div className='divide-y sm:hidden'>
                {records.length === 0 ? (
                  <Empty title={t('暂无空回记录')} />
                ) : (
                  records.map((record) => (
                    <div key={record.id} className='space-y-3 px-4 py-4'>
                      <div className='flex items-start justify-between gap-3'>
                        <div className='min-w-0'>
                          <div className='truncate font-medium'>
                            {record.model_name || '-'}
                          </div>
                          <Text type='tertiary'>
                            {timestamp2string(record.log_created_at)}
                          </Text>
                        </div>
                        {record.status === 'refunded' ? (
                          <Tag color='green'>{t('已赔付')}</Tag>
                        ) : record.status === 'expired' ? (
                          <Tag color='grey'>{t('已过期')}</Tag>
                        ) : (
                          <Tag color='orange'>{t('待赔付')}</Tag>
                        )}
                      </div>
                      <Row gutter={8}>
                        <Col span={12}>
                          <Text type='tertiary'>{t('输入 Tokens')}</Text>
                          <div>{record.prompt_tokens}</div>
                        </Col>
                        <Col span={12}>
                          <Text type='tertiary'>{t('当时扣费')}</Text>
                          <div>{renderQuota(record.quota)}</div>
                        </Col>
                        <Col span={12}>
                          <Text type='tertiary'>{t('预计赔付')}</Text>
                          <div>{renderQuota(record.refund_quota)}</div>
                        </Col>
                        <Col span={12}>
                          <Text type='tertiary'>{t('赔付比例')}</Text>
                          <div>{record.refund_percent}%</div>
                        </Col>
                      </Row>
                    </div>
                  ))
                )}
              </div>
            </Card>
          </Space>
        </div>
      </div>
    </Spin>
  );
}
