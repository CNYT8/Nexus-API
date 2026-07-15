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

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Avatar,
  Card,
  Collapsible,
  Empty,
  Progress,
  Skeleton,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconChevronDown, IconChevronRight } from '@douyinfe/semi-icons';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { API, getLobeHubIcon, showError } from '../../helpers';
import CardPro from '../../components/common/ui/CardPro';

const { Text } = Typography;

const STATUS_META = {
  excellent: {
    tagColor: 'green',
    progressColor: 'var(--semi-color-success)',
    panelColor: 'transparent',
    panelBorderColor: 'var(--semi-color-border)',
    fallbackText: '优秀',
  },
  good: {
    tagColor: 'yellow',
    progressColor: 'var(--semi-color-warning)',
    panelColor: 'transparent',
    panelBorderColor: 'var(--semi-color-border)',
    fallbackText: '良好',
  },
  unstable: {
    tagColor: 'pink',
    progressColor: '#f43f5e',
    panelColor: 'transparent',
    panelBorderColor: 'var(--semi-color-border)',
    fallbackText: '不稳定',
  },
  poor: {
    tagColor: 'red',
    progressColor: 'var(--semi-color-danger)',
    panelColor: 'transparent',
    panelBorderColor: 'var(--semi-color-border)',
    fallbackText: '体验较差',
  },
  unknown: {
    tagColor: 'grey',
    progressColor: 'var(--semi-color-fill-2)',
    panelColor: 'transparent',
    panelBorderColor: 'var(--semi-color-border)',
    fallbackText: '未知状态',
  },
};

const getStatusByScore = (score) => {
  if (score >= 85) return 'excellent';
  if (score >= 70) return 'good';
  if (score >= 45) return 'unstable';
  return 'poor';
};

const getItemStatus = (item) => {
  if (!item || item.has_data === false || item.status === 'unknown') {
    return 'unknown';
  }
  return item.status || getStatusByScore(item.score || 0);
};

const getStatusMeta = (status) => STATUS_META[status] || STATUS_META.unknown;

const getStatusText = (status, t) => t(getStatusMeta(status).fallbackText);

const clampScore = (score) => Math.min(100, Math.max(0, Number(score) || 0));

const formatRefreshClock = (timestamp) => {
  if (!timestamp) {
    return '--:--';
  }
  const date = new Date(timestamp);
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');
  return `${hours}:${minutes}`;
};

const renderVendorIcon = (vendor) => {
  if (vendor.icon) {
    return (
      <div className='flex h-8 w-8 shrink-0 items-center justify-center'>
        {getLobeHubIcon(vendor.icon, 24)}
      </div>
    );
  }
  return (
    <Avatar size='small'>
      {(vendor.name || '?').slice(0, 1).toUpperCase()}
    </Avatar>
  );
};

const ModelScoreBar = ({ model }) => {
  const hasData = model.has_data !== false && getItemStatus(model) !== 'unknown';
  const meta = getStatusMeta(getItemStatus(model));
  const score = hasData ? clampScore(model.score) : 0;

  return (
    <div className='flex min-w-[138px] items-center justify-end gap-2'>
      <div className='w-[92px]'>
        <Progress
          percent={score}
          stroke={meta.progressColor}
          strokeWidth={8}
          showInfo={false}
          style={{ margin: 0 }}
        />
      </div>
      <Text
        type={hasData ? 'secondary' : 'tertiary'}
        size='small'
        className='inline-block w-8 text-right'
      >
        {hasData ? score : '-'}
      </Text>
    </div>
  );
};

const ModelRow = ({ model, t }) => {
  const status = getItemStatus(model);
  const meta = getStatusMeta(status);
  const group = model.group || 'default';

  return (
    <div
      className='my-1 flex flex-col gap-2 rounded-md border py-2 pl-3 pr-2 md:flex-row md:items-center md:justify-between'
      style={{
        backgroundColor: meta.panelColor,
        borderColor: meta.panelBorderColor,
        borderLeftColor: meta.progressColor,
        borderLeftWidth: status === 'unknown' ? 1 : 3,
      }}
    >
      <div className='min-w-0 md:flex-1'>
        <div className='flex min-w-0 flex-wrap items-center gap-1.5'>
          <Text className='block min-w-0 max-w-full truncate'>
            {model.model_name}
          </Text>
          <Tag
            color='grey'
            size='small'
            shape='circle'
            className='max-w-full whitespace-normal break-words [&_.semi-tag-content]:whitespace-normal [&_.semi-tag-content]:break-words'
            style={{ height: 'auto' }}
          >
            {group}
          </Tag>
        </div>
      </div>
      <div className='flex items-center justify-between gap-3 md:justify-end'>
        <Tag color={meta.tagColor} shape='circle'>
          {getStatusText(status, t)}
        </Tag>
        <ModelScoreBar model={model} />
      </div>
    </div>
  );
};

const VendorBlock = ({ vendor, open, onToggle, t }) => {
  const status = getItemStatus(vendor);
  const meta = getStatusMeta(status);
  const modelCount = vendor.models?.length || 0;

  return (
    <div
      className='border-b last:border-b-0'
      style={{ borderColor: 'var(--semi-color-border)' }}
    >
      <button
        type='button'
        className='flex w-full items-center gap-3 bg-transparent px-0 py-3 text-left'
        style={{ color: 'inherit' }}
        onClick={onToggle}
      >
        <span
          className='shrink-0'
          style={{ color: 'var(--semi-color-text-2)' }}
        >
          {open ? <IconChevronDown /> : <IconChevronRight />}
        </span>
        {renderVendorIcon(vendor)}
        <div className='min-w-0 flex-1'>
          <div className='flex min-w-0 flex-wrap items-center gap-2'>
            <Text strong className='truncate'>
              {vendor.name || t('未知供应商')}
            </Text>
            <Tag color={meta.tagColor} shape='circle'>
              {getStatusText(status, t)}
            </Tag>
          </div>
          <Space spacing={8} wrap>
            <Text type='secondary' size='small'>
              {t('模型')} {modelCount}
            </Text>
            <Text type='secondary' size='small'>
              {t('有数据')} {vendor.known_count || 0}
            </Text>
            {vendor.unknown_count > 0 && (
              <Text type='tertiary' size='small'>
                {t('未知')} {vendor.unknown_count}
              </Text>
            )}
          </Space>
        </div>
        <div className='hidden min-w-[54px] text-right md:block'>
          <Text type={status === 'unknown' ? 'tertiary' : 'secondary'}>
            {status === 'unknown' ? '-' : vendor.score}
          </Text>
        </div>
      </button>
      <Collapsible isOpen={open} keepDOM>
        <div className='pb-2 pl-8 md:pl-[76px]'>
          {(vendor.models || []).map((model) => (
            <ModelRow
              key={`${model.model_name}:${model.group || 'default'}`}
              model={model}
              t={t}
            />
          ))}
        </div>
      </Collapsible>
    </div>
  );
};

const ModelMonitor = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [summary, setSummary] = useState(null);
  const [expandedVendors, setExpandedVendors] = useState(null);
  const [nextRefreshAt, setNextRefreshAt] = useState(0);

  const fetchMonitor = useCallback(
    async (silent = false) => {
      if (!silent) {
        setLoading(true);
      }
      try {
        const res = await API.get('/api/model_monitor');
        const { success, message, data } = res.data;
        if (!success) {
          showError(message);
          return;
        }
        setSummary(data);
        setNextRefreshAt(
          Date.now() + (data?.refresh_seconds || 60) * 1000,
        );
      } catch (error) {
        showError(t('加载失败'));
      } finally {
        if (!silent) {
          setLoading(false);
        }
      }
    },
    [t],
  );

  useEffect(() => {
    fetchMonitor();
    const timer = setInterval(() => fetchMonitor(true), 60 * 1000);
    return () => clearInterval(timer);
  }, [fetchMonitor]);

  useEffect(() => {
    if (!summary?.vendors?.length) return;
    setExpandedVendors((prev) => {
      const validKeys = new Set(
        summary.vendors.map((vendor) => String(vendor.name)),
      );
      if (prev !== null) {
        return prev.filter((key) => validKeys.has(key));
      }
      return summary.vendors.slice(0, 3).map((vendor) => String(vendor.name));
    });
  }, [summary]);

  const expandedSet = useMemo(
    () => new Set(expandedVendors || []),
    [expandedVendors],
  );

  const toggleVendor = (name) => {
    const key = String(name);
    setExpandedVendors((prev) => {
      const current = new Set(prev || []);
      if (current.has(key)) {
        current.delete(key);
      } else {
        current.add(key);
      }
      return Array.from(current);
    });
  };

  const headerArea = summary && (
    <div className='flex flex-col gap-2 md:flex-row md:items-center md:justify-between'>
      <div className='min-w-0'>
        <Text strong>{t('模型监控')}</Text>
        <div>
          <Text type='secondary' size='small'>
            {t('近7天全局模型体验评分，依靠智能调度算法给出多维度综合评分。')}
          </Text>
        </div>
      </div>
      <Space spacing={10} wrap>
        <Text type='secondary' size='small'>
          {t('模型')} {summary.model_count}
        </Text>
        <Text type='secondary' size='small'>
          {t('有数据')} {summary.known_count}
        </Text>
        <Text type='secondary' size='small'>
          {t('未知')} {summary.unknown_count}
        </Text>
        <Space spacing={4} wrap>
          <Text type='tertiary' size='small'>
            {t('每1分钟动态更新数据')}
            <span
              className='ml-1 inline-block align-baseline'
              style={{ fontSize: 11, opacity: 0.72 }}
            >
              {t('下次更新时间:')} {formatRefreshClock(nextRefreshAt)}
            </span>
          </Text>
        </Space>
      </Space>
    </div>
  );

  if (loading) {
    return (
      <div className='mt-[60px] px-2'>
        <Card className='!rounded-2xl'>
          <Skeleton active placeholder={<Skeleton.Paragraph rows={8} />} />
        </Card>
      </div>
    );
  }

  if (!summary || !summary.vendors || summary.vendors.length === 0) {
    return (
      <div className='mt-[60px] px-2'>
        <Card className='!rounded-2xl'>
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
      <CardPro type='type2' t={t}>
        <div className='flex flex-col'>
          <div
            className='border-b pb-3'
            style={{ borderColor: 'var(--semi-color-border)' }}
          >
            {headerArea}
          </div>
          {summary.vendors.map((vendor) => {
            const key = String(vendor.name);
            return (
              <VendorBlock
                key={key}
                vendor={vendor}
                open={expandedSet.has(key)}
                onToggle={() => toggleVendor(key)}
                t={t}
              />
            );
          })}
        </div>
      </CardPro>
    </div>
  );
};

export default ModelMonitor;
