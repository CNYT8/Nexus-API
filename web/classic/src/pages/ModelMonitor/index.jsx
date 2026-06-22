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

import React, { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Avatar,
  Card,
  Collapse,
  Empty,
  Progress,
  Skeleton,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { API, getLobeHubIcon, showError, timestamp2string } from '../../helpers';

const { Text, Title } = Typography;

const STATUS_META = {
  excellent: {
    tagColor: 'green',
    progressColor: 'var(--semi-color-success)',
  },
  good: {
    tagColor: 'yellow',
    progressColor: 'var(--semi-color-warning)',
  },
  unstable: {
    tagColor: 'pink',
    progressColor: 'var(--semi-color-danger-light-default)',
  },
  poor: {
    tagColor: 'red',
    progressColor: 'var(--semi-color-danger)',
  },
};

const getStatusMeta = (status) => STATUS_META[status] || STATUS_META.poor;

const getScoreStatus = (score) => {
  if (score >= 85) return 'excellent';
  if (score >= 70) return 'good';
  if (score >= 45) return 'unstable';
  return 'poor';
};

const renderVendorIcon = (vendor) => {
  if (vendor.icon) {
    return (
      <div className='w-10 h-10 flex items-center justify-center'>
        {getLobeHubIcon(vendor.icon, 32)}
      </div>
    );
  }
  return (
    <Avatar size='default'>
      {(vendor.name || '?').slice(0, 1).toUpperCase()}
    </Avatar>
  );
};

const ModelScoreBar = ({ score }) => {
  const meta = getStatusMeta(getScoreStatus(score));
  return (
    <Progress
      percent={score}
      stroke={meta.progressColor}
      aria-label='model monitor score'
      format={() => `${score}`}
      style={{ margin: 0 }}
    />
  );
};

const ModelMonitor = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [summary, setSummary] = useState(null);

  const fetchMonitor = async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/model_monitor');
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setSummary(data);
    } catch (error) {
      showError(t('加载失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchMonitor();
  }, []);

  const activeKeys = useMemo(() => {
    if (!summary?.vendors) return [];
    return summary.vendors.slice(0, 3).map((vendor) => String(vendor.name));
  }, [summary]);

  if (loading) {
    return (
      <div className='mt-[60px] px-2'>
        <Card>
          <Skeleton active placeholder={<Skeleton.Paragraph rows={8} />} />
        </Card>
      </div>
    );
  }

  if (!summary || !summary.vendors || summary.vendors.length === 0) {
    return (
      <div className='mt-[60px] px-2'>
        <Card>
          <Empty
            image={<IllustrationNoResult style={{ width: 150, height: 150 }} />}
            darkModeImage={
              <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
            }
            description={t('暂无模型监控数据')}
          />
        </Card>
      </div>
    );
  }

  return (
    <div className='mt-[60px] px-2'>
      <Card
        bodyStyle={{
          display: 'flex',
          flexDirection: 'column',
          gap: 16,
        }}
      >
        <div className='flex flex-col gap-3 md:flex-row md:items-center md:justify-between'>
          <div>
            <Title heading={4} style={{ margin: 0 }}>
              {t('模型监控')}
            </Title>
            <Text type='secondary'>
              {t('近7天全局模型体验评分，近3天请求权重更高')}
            </Text>
          </div>
          <Space wrap>
            <Tag color='blue' shape='circle'>
              {t('模型数')} {summary.model_count}
            </Tag>
            <Tag color='teal' shape='circle'>
              {t('供应商')} {summary.vendor_count}
            </Tag>
            <Tag color='violet' shape='circle'>
              {t('最高分')} {summary.best_score}
            </Tag>
            <Text type='secondary' size='small'>
              {t('更新时间')} {timestamp2string(summary.updated_at)}
            </Text>
          </Space>
        </div>

        <Collapse defaultActiveKey={activeKeys}>
          {summary.vendors.map((vendor) => {
            const meta = getStatusMeta(getScoreStatus(vendor.score));
            return (
              <Collapse.Panel
                key={vendor.name}
                itemKey={String(vendor.name)}
                header={
                  <div className='flex w-full items-center justify-between gap-3'>
                    <div className='flex min-w-0 items-center gap-3'>
                      {renderVendorIcon(vendor)}
                      <div className='min-w-0'>
                        <div className='truncate font-semibold'>
                          {vendor.name || t('未知供应商')}
                        </div>
                        <Text type='secondary' size='small'>
                          {t('模型')} {vendor.models?.length || 0}
                        </Text>
                      </div>
                    </div>
                    <div className='flex min-w-[148px] items-center gap-2'>
                      <Tag color={meta.tagColor} shape='circle'>
                        {vendor.score}
                      </Tag>
                    </div>
                  </div>
                }
              >
                <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'>
                  {vendor.models.map((model) => {
                    const modelMeta = getStatusMeta(getScoreStatus(model.score));
                    return (
                      <Card
                        key={model.model_name}
                        bodyStyle={{ padding: 16 }}
                        style={{
                          borderRadius: 8,
                          border: '1px solid var(--semi-color-border)',
                        }}
                      >
                        <div className='flex items-start justify-between gap-3'>
                          <div className='min-w-0'>
                            <div className='truncate font-semibold'>
                              {model.model_name}
                            </div>
                            <Text type='secondary' size='small'>
                              {t('体验分')} {model.score}
                            </Text>
                          </div>
                          <Tag color={modelMeta.tagColor} shape='circle'>
                            {model.score}
                          </Tag>
                        </div>
                        <div className='mt-3'>
                          <ModelScoreBar score={model.score} />
                        </div>
                      </Card>
                    );
                  })}
                </div>
              </Collapse.Panel>
            );
          })}
        </Collapse>
      </Card>
    </div>
  );
};

export default ModelMonitor;
