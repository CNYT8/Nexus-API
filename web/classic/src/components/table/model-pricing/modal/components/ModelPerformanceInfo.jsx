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
import { Avatar, Empty, Spin, Table, Tag, Typography } from '@douyinfe/semi-ui';
import { IconPulse } from '@douyinfe/semi-icons';
import { VChart } from '@visactor/react-vchart';

import { useActualTheme } from '../../../../../context/Theme';
import { useDashboardChartTheme } from '../../../../../hooks/dashboard/useDashboardChartTheme';
import { CHART_CONFIG } from '../../../../../constants/dashboard.constants';
import {
  getCachedModelPerformanceMetrics,
  prefetchModelPerformanceMetrics,
} from '../../../../../helpers/modelPerformanceMetrics';

const { Text } = Typography;

function getFiniteNumber(value, fallback = 0) {
  const numberValue = Number(value);
  return Number.isFinite(numberValue) ? numberValue : fallback;
}

function getWeightedMetric(source, weightedKey, fallbackKey) {
  const weightedValue = Number(source?.[weightedKey]);
  if (Number.isFinite(weightedValue)) return weightedValue;
  return getFiniteNumber(source?.[fallbackKey]);
}

function getMetricWeight(source) {
  const weightedCount = getFiniteNumber(source?.weighted_request_count);
  if (weightedCount > 0) return weightedCount;
  const requestCount = getFiniteNumber(source?.request_count);
  return requestCount > 0 ? requestCount : 1;
}

function getPointTtft(point) {
  const adjusted = getFiniteNumber(point?.adjusted_avg_ttft_ms);
  if (adjusted > 0) return adjusted;
  return getFiniteNumber(point?.avg_ttft_ms);
}

function weightedAverage(items, valueSelector, predicate = Number.isFinite) {
  let weightedSum = 0;
  let weightSum = 0;

  items.forEach((item) => {
    const value = valueSelector(item);
    const weight = getMetricWeight(item);
    if (!predicate(value) || !Number.isFinite(weight) || weight <= 0) return;
    weightedSum += value * weight;
    weightSum += weight;
  });

  return weightSum > 0 ? weightedSum / weightSum : 0;
}

function formatLatency(ms) {
  const value = getFiniteNumber(ms);
  if (value <= 0) return '-';
  if (value >= 1000) {
    return `${(value / 1000).toFixed(value >= 10000 ? 1 : 2)} s`;
  }
  return `${Math.round(value)} ms`;
}

function formatThroughput(tps) {
  const value = getFiniteNumber(tps);
  if (value <= 0) return '-';
  if (value >= 1000) {
    return `${(value / 1000).toFixed(1)}K t/s`;
  }
  return `${value.toFixed(value < 10 ? 2 : 1)} t/s`;
}

function formatPercent(value) {
  const numberValue = Number(value);
  if (!Number.isFinite(numberValue)) return '-';
  const normalized = Math.min(100, Math.max(0, numberValue));
  return `${normalized.toFixed(normalized >= 99 ? 2 : 1)}%`;
}

function formatTimeLabel(timestamp) {
  const date = new Date(timestamp * 1000);
  if (Number.isNaN(date.getTime())) return '';
  return `${String(date.getHours()).padStart(2, '0')}:${String(
    date.getMinutes(),
  ).padStart(2, '0')}`;
}

function normalizeGroups(groups) {
  if (!Array.isArray(groups)) return [];

  return groups.map((group) => ({
    group: group.group || 'default',
    avg_tps: getWeightedMetric(group, 'weighted_avg_tps', 'avg_tps'),
    avg_ttft_ms: getWeightedMetric(
      group,
      'weighted_avg_ttft_ms',
      'avg_ttft_ms',
    ),
    avg_latency_ms: getWeightedMetric(
      group,
      'weighted_avg_latency_ms',
      'avg_latency_ms',
    ),
    success_rate: getWeightedMetric(
      group,
      'weighted_success_rate',
      'success_rate',
    ),
    request_count: getFiniteNumber(group.request_count),
    weighted_request_count: getFiniteNumber(group.weighted_request_count),
    series: Array.isArray(group.series)
      ? group.series.map((point) => ({
          ...point,
          request_count: getFiniteNumber(point?.request_count),
        }))
      : [],
  }));
}

function buildLatencySeries(groups) {
  const byTimestamp = new Map();

  groups.forEach((group) => {
    group.series.forEach((point) => {
      const timestamp = Number(point.ts);
      const ttft = getPointTtft(point);
      if (!Number.isFinite(timestamp) || ttft <= 0) return;

      const weight = getMetricWeight(point);
      const current = byTimestamp.get(timestamp) || { sum: 0, weight: 0 };
      current.sum += ttft * weight;
      current.weight += weight;
      byTimestamp.set(timestamp, current);
    });
  });

  return Array.from(byTimestamp.entries())
    .sort(([left], [right]) => left - right)
    .map(([timestamp, value]) => ({
      time: formatTimeLabel(timestamp),
      value: value.weight > 0 ? Math.round(value.sum / value.weight) : 0,
    }));
}

function buildLatencyTrendSpec(values, t) {
  return {
    type: 'line',
    data: [
      {
        id: 'ttftTrend',
        values,
      },
    ],
    xField: 'time',
    yField: 'value',
    padding: [8, 8, 4, 0],
    line: {
      style: {
        lineWidth: 2,
      },
    },
    point: {
      visible: true,
      style: {
        size: 4,
      },
    },
    axes: [
      {
        orient: 'bottom',
        label: {
          visible: true,
        },
      },
      {
        orient: 'left',
        label: {
          visible: true,
        },
        title: {
          visible: true,
          text: 'ms',
        },
      },
    ],
    tooltip: {
      mark: {
        title: {
          value: (datum) => datum.time,
        },
        content: [
          {
            key: t('平均首字'),
            value: (datum) => formatLatency(datum.value),
          },
        ],
      },
    },
  };
}

function MetricItem({ label, value }) {
  return (
    <div
      className='rounded-lg'
      style={{
        padding: '10px 12px',
        border: '1px solid var(--semi-color-border)',
        background: 'var(--semi-color-fill-0)',
      }}
    >
      <div className='text-xs text-gray-500 mb-1'>{label}</div>
      <Text strong className='font-mono'>
        {value}
      </Text>
    </div>
  );
}

const ModelPerformanceInfo = ({ modelData, t }) => {
  const actualTheme = useActualTheme();
  const chartThemeKey = useDashboardChartTheme(actualTheme);
  const modelName = modelData?.model_name || modelData?.modelName || '';
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const [groups, setGroups] = useState(
    () => normalizeGroups(getCachedModelPerformanceMetrics(modelName)),
  );

  useEffect(() => {
    let isCurrent = true;

    const loadPerformanceMetrics = async () => {
      if (!modelName) {
        setGroups([]);
        return;
      }

      const cachedGroups = getCachedModelPerformanceMetrics(modelName);
      if (cachedGroups) {
        setGroups(normalizeGroups(cachedGroups));
        setLoading(false);
        setFailed(false);
        return;
      }

      setLoading(true);
      setFailed(false);

      try {
        const nextGroups = await prefetchModelPerformanceMetrics(modelName);

        if (!isCurrent) return;

        setGroups(normalizeGroups(nextGroups));
      } catch (error) {
        if (!isCurrent) return;
        console.error(error);
        setGroups([]);
        setFailed(true);
      } finally {
        if (isCurrent) {
          setLoading(false);
        }
      }
    };

    loadPerformanceMetrics();

    return () => {
      isCurrent = false;
    };
  }, [modelName]);

  const stats = useMemo(() => {
    const avgTps = weightedAverage(
      groups,
      (group) => group.avg_tps,
      (value) => value > 0,
    );
    const avgTtft = weightedAverage(
      groups,
      (group) => group.avg_ttft_ms,
      (value) => value > 0,
    );
    const successRate = Math.min(
      100,
      Math.max(
        0,
        weightedAverage(
          groups,
          (group) => group.success_rate,
          Number.isFinite,
        ),
      ),
    );

    return {
      avgTps,
      avgTtft,
      successRate,
    };
  }, [groups]);

  const latencySeries = useMemo(() => buildLatencySeries(groups), [groups]);
  const latencyTrendSpec = useMemo(
    () => buildLatencyTrendSpec(latencySeries, t),
    [latencySeries, t],
  );

  const columns = useMemo(
    () => [
      {
        title: t('分组'),
        dataIndex: 'group',
        render: (group) => (
          <Tag color='blue' size='small' shape='circle'>
            {group}
          </Tag>
        ),
      },
      {
        title: t('平均TPS'),
        dataIndex: 'avg_tps',
        align: 'right',
        render: (value) => (
          <Text className='font-mono'>{formatThroughput(value)}</Text>
        ),
      },
      {
        title: t('平均首字'),
        dataIndex: 'avg_ttft_ms',
        align: 'right',
        render: (value) => (
          <Text className='font-mono'>{formatLatency(value)}</Text>
        ),
      },
      {
        title: t('平均延迟'),
        dataIndex: 'avg_latency_ms',
        align: 'right',
        render: (value) => (
          <Text type='secondary' className='font-mono'>
            {formatLatency(value)}
          </Text>
        ),
      },
      {
        title: t('成功率'),
        dataIndex: 'success_rate',
        align: 'right',
        render: (value) => (
          <Text className='font-mono'>{formatPercent(value)}</Text>
        ),
      },
    ],
    [t],
  );

  return (
    <div>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='green' className='mr-2 shadow-md'>
          <IconPulse size={16} />
        </Avatar>
        <div>
          <Text className='text-lg font-medium'>{t('模型性能')}</Text>
          <div className='text-xs text-gray-600'>
            {t('近24小时模型性能概览')}
          </div>
        </div>
      </div>

      {loading && (
        <div className='flex justify-center items-center py-8'>
          <Spin size='middle' />
        </div>
      )}

      {!loading && (failed || groups.length === 0) && (
        <div
          className='rounded-lg'
          style={{
            border: '1px solid var(--semi-color-border)',
            background: 'var(--semi-color-fill-0)',
            padding: 16,
          }}
        >
          <Empty
            image={<IconPulse size={28} />}
            title={failed ? t('模型性能数据加载失败') : t('暂无模型性能数据')}
            description={t('近24小时暂无可展示的性能采样')}
          />
        </div>
      )}

      {!loading && !failed && groups.length > 0 && (
        <div className='space-y-4'>
          <div className='grid grid-cols-1 sm:grid-cols-3 gap-2'>
            <MetricItem
              label={t('平均TPS')}
              value={formatThroughput(stats.avgTps)}
            />
            <MetricItem
              label={t('平均首字')}
              value={formatLatency(stats.avgTtft)}
            />
            <MetricItem
              label={t('成功率')}
              value={formatPercent(stats.successRate)}
            />
          </div>

          <div>
            <Text
              strong
              className='text-sm'
              style={{ display: 'block', marginBottom: 8 }}
            >
              {t('近24小时首字趋势')}
            </Text>
            <div
              className='rounded-lg'
              style={{
                height: 180,
                border: '1px solid var(--semi-color-border)',
                background: 'var(--semi-color-fill-0)',
                padding: 8,
              }}
            >
              {latencySeries.length > 0 ? (
                <VChart
                  key={`model-detail-ttft-${chartThemeKey}-${modelName}`}
                  spec={latencyTrendSpec}
                  option={CHART_CONFIG}
                />
              ) : (
                <div className='flex justify-center items-center h-full text-gray-500 text-sm'>
                  {t('暂无模型性能数据')}
                </div>
              )}
            </div>
          </div>

          <div>
            <Text
              strong
              className='text-sm'
              style={{ display: 'block', marginBottom: 8 }}
            >
              {t('分组性能')}
            </Text>
            <Table
              dataSource={groups}
              columns={columns}
              pagination={false}
              size='small'
              rowKey='group'
              bordered={false}
              className='!rounded-lg'
              scroll={{ x: 520 }}
            />
          </div>
        </div>
      )}
    </div>
  );
};

export default ModelPerformanceInfo;
