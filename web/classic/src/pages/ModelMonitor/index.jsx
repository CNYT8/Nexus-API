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
  Card,
  Collapsible,
  Empty,
  Skeleton,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import { IconChevronDown, IconChevronRight } from '@douyinfe/semi-icons';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { API, showError } from '../../helpers';
import CardPro from '../../components/common/ui/CardPro';

const { Text } = Typography;

// Three availability colors driven by the scheduling-score thresholds:
// green >= 70, orange 45-70, red < 45, gray means no traffic at all.
const STATUS_BAR_COLOR = {
  excellent: 'var(--semi-color-success)',
  good: 'var(--semi-color-success)',
  unstable: 'var(--semi-color-warning)',
  poor: 'var(--semi-color-danger)',
  unknown: 'var(--semi-color-fill-1)',
};

const STATUS_BAR_TEXT = {
  excellent: '可用',
  good: '可用',
  unstable: '不稳定',
  poor: '体验较差',
  unknown: '无调用',
};

const LEGEND_ITEMS = [
  { color: STATUS_BAR_COLOR.excellent, label: '可用' },
  { color: STATUS_BAR_COLOR.unstable, label: '不稳定' },
  { color: STATUS_BAR_COLOR.poor, label: '体验较差' },
  { color: STATUS_BAR_COLOR.unknown, label: '无调用' },
];

const padTimePart = (value) => String(value).padStart(2, '0');

const formatBucketTime = (timestamp) => {
  if (!timestamp) return '--';
  const date = new Date(timestamp * 1000);
  return `${padTimePart(date.getMonth() + 1)}-${padTimePart(date.getDate())} ${padTimePart(date.getHours())}:${padTimePart(date.getMinutes())}`;
};

const formatRefreshClock = (timestamp) => {
  if (!timestamp) {
    return '--:--';
  }
  const date = new Date(timestamp);
  return `${padTimePart(date.getHours())}:${padTimePart(date.getMinutes())}`;
};

const getBarStatus = (bucket) =>
  bucket?.has_data ? bucket.status || 'unknown' : 'unknown';

const AvailabilityStrip = ({ buckets, windowEnd, height = 26, t }) => (
  <div
    className='flex w-full items-stretch gap-[3px]'
    style={{ height }}
    role='img'
    aria-label={t('滚动24小时分组可用性，每2小时一个时段，按智能调度算法计算。')}
  >
    {(buckets || []).map((bucket, index) => {
      const status = getBarStatus(bucket);
      const isInProgress =
        index === (buckets || []).length - 1 && bucket.end > windowEnd;
      const displayEnd = isInProgress ? windowEnd : bucket.end;
      return (
        <Tooltip
          key={bucket.start}
          showArrow
          content={
            <div className='text-center'>
              <div>
                {formatBucketTime(bucket.start)} –{' '}
                {formatBucketTime(displayEnd)}
              </div>
              <div>
                {t(STATUS_BAR_TEXT[status])}
                {isInProgress ? ` · ${t('进行中')}` : ''}
              </div>
            </div>
          }
        >
          <div
            className='h-full min-w-0 flex-1 cursor-default rounded-[3px]'
            style={{
              backgroundColor: STATUS_BAR_COLOR[status],
              backgroundImage:
                isInProgress && bucket.has_data
                  ? 'repeating-linear-gradient(45deg, rgba(255, 255, 255, 0.25) 0 3px, transparent 3px 6px)'
                  : undefined,
            }}
          />
        </Tooltip>
      );
    })}
  </div>
);

const ModelRow = ({ model, windowEnd, t }) => (
  <div
    className='flex flex-col gap-1.5 rounded-md border px-3 py-2'
    style={{ borderColor: 'var(--semi-color-border)' }}
  >
    <span className='truncate font-mono text-[13px]'>
      {model.model_name}
    </span>
    <AvailabilityStrip
      buckets={model.buckets}
      windowEnd={windowEnd}
      height={16}
      t={t}
    />
  </div>
);

const GroupBlock = ({ group, windowEnd, open, onToggle, t }) => (
  <div
    className='border-b last:border-b-0'
    style={{ borderColor: 'var(--semi-color-border)' }}
  >
    <button
      type='button'
      className='w-full bg-transparent px-0 py-3 text-left'
      style={{ color: 'inherit' }}
      onClick={onToggle}
    >
      <div className='flex flex-col gap-2 md:flex-row md:items-center md:gap-4'>
        <div className='flex min-w-0 items-center gap-2 md:w-44 md:shrink-0'>
          <span
            className='shrink-0'
            style={{ color: 'var(--semi-color-text-2)' }}
          >
            {open ? <IconChevronDown /> : <IconChevronRight />}
          </span>
          <Text strong className='truncate'>
            {group.group}
          </Text>
        </div>
        <div className='min-w-0 flex-1'>
          <AvailabilityStrip
            buckets={group.buckets}
            windowEnd={windowEnd}
            height={26}
            t={t}
          />
        </div>
      </div>
    </button>
    <Collapsible isOpen={open} keepDOM>
      <div className='grid gap-1.5 pb-3 pl-6 md:grid-cols-2 md:gap-x-6 md:pl-14'>
        {(group.models || []).map((model) => (
          <ModelRow
            key={model.model_name}
            model={model}
            windowEnd={windowEnd}
            t={t}
          />
        ))}
      </div>
    </Collapsible>
  </div>
);

const ModelMonitor = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [availability, setAvailability] = useState(null);
  const [expandedGroups, setExpandedGroups] = useState(() => new Set());
  const [nextRefreshAt, setNextRefreshAt] = useState(0);

  const fetchMonitor = useCallback(
    async (silent = false) => {
      if (!silent) {
        setLoading(true);
      }
      try {
        const res = await API.get('/api/model_monitor/availability');
        const { success, message, data } = res.data;
        if (!success) {
          showError(message);
          return;
        }
        setAvailability(data);
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

  const toggleGroup = (name) => {
    const key = String(name);
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  const headerArea = availability && (
    <div className='flex flex-col gap-2.5'>
      <div className='flex flex-col gap-2 md:flex-row md:items-center md:justify-between'>
        <div className='min-w-0'>
          <Text strong>{t('模型监控')}</Text>
          <div>
            <Text type='secondary' size='small'>
              {t(
                '滚动24小时分组可用性，每2小时一个时段，按智能调度算法计算。',
              )}
            </Text>
          </div>
        </div>
        <Text type='tertiary' size='small' className='whitespace-nowrap'>
          {t('每1分钟动态更新数据')}
          <span
            className='ml-1 inline-block align-baseline'
            style={{ fontSize: 11, opacity: 0.72 }}
          >
            {t('下次更新时间:')} {formatRefreshClock(nextRefreshAt)}
          </span>
        </Text>
      </div>
      <div className='flex flex-wrap items-center gap-x-4 gap-y-1.5'>
        {LEGEND_ITEMS.map((item) => (
          <span key={item.label} className='flex items-center gap-1.5'>
            <span
              className='inline-block h-2.5 w-2.5 shrink-0 rounded-[3px]'
              style={{ backgroundColor: item.color }}
            />
            <Text type='tertiary' size='small'>
              {t(item.label)}
            </Text>
          </span>
        ))}
      </div>
    </div>
  );

  const windowEnd = useMemo(
    () => availability?.window_end || 0,
    [availability],
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

  if (!availability || !availability.groups || availability.groups.length === 0) {
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
          {availability.groups.map((group) => {
            const key = String(group.group);
            return (
              <GroupBlock
                key={key}
                group={group}
                windowEnd={windowEnd}
                open={expandedGroups.has(key)}
                onToggle={() => toggleGroup(key)}
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
