/*
Copyright (C) 2023-2026 QuantumNous

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
import type {
  PerformanceGroup,
  PerformanceSeriesPoint,
  PerfModelSummary,
} from '../types'

type WeightedMetricKey =
  | 'weighted_avg_tps'
  | 'weighted_avg_ttft_ms'
  | 'weighted_avg_latency_ms'
  | 'weighted_success_rate'

type MetricKey = 'avg_tps' | 'avg_ttft_ms' | 'avg_latency_ms' | 'success_rate'

type WeightedSummaryMetricKey =
  | 'weighted_avg_tps'
  | 'weighted_avg_latency_ms'
  | 'weighted_success_rate'

type SummaryMetricKey = 'avg_tps' | 'avg_latency_ms' | 'success_rate'

const metricFallbackMap: Record<WeightedMetricKey, MetricKey> = {
  weighted_avg_tps: 'avg_tps',
  weighted_avg_ttft_ms: 'avg_ttft_ms',
  weighted_avg_latency_ms: 'avg_latency_ms',
  weighted_success_rate: 'success_rate',
}

const summaryMetricFallbackMap: Record<
  WeightedSummaryMetricKey,
  SummaryMetricKey
> = {
  weighted_avg_tps: 'avg_tps',
  weighted_avg_latency_ms: 'avg_latency_ms',
  weighted_success_rate: 'success_rate',
}

function finiteNumber(value: unknown, fallback = 0): number {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : fallback
}

export function weightedMetric(
  group: PerformanceGroup,
  key: WeightedMetricKey
): number {
  const weightedValue = Number(group[key])
  if (Number.isFinite(weightedValue)) return weightedValue
  return finiteNumber(group[metricFallbackMap[key]])
}

export function weightedSummaryMetric(
  summary: PerfModelSummary | undefined,
  key: WeightedSummaryMetricKey
): number {
  if (!summary) return Number.NaN
  const weightedValue = Number(summary[key])
  if (Number.isFinite(weightedValue)) return weightedValue
  return finiteNumber(summary[summaryMetricFallbackMap[key]])
}

export function groupWeight(group: PerformanceGroup): number {
  const weightedCount = finiteNumber(group.weighted_request_count)
  if (weightedCount > 0) return weightedCount
  const requestCount = finiteNumber(group.request_count)
  return requestCount > 0 ? requestCount : 1
}

export function pointWeight(point: PerformanceSeriesPoint): number {
  const requestCount = finiteNumber(point.request_count)
  return requestCount > 0 ? requestCount : 1
}

export function weightedAverage<T>(
  rows: T[],
  valueSelector: (row: T) => number,
  weightSelector: (row: T) => number,
  predicate: (value: number) => boolean = Number.isFinite
): number {
  let weightedSum = 0
  let weightSum = 0

  for (const row of rows) {
    const value = valueSelector(row)
    const weight = weightSelector(row)
    if (!predicate(value) || !Number.isFinite(weight) || weight <= 0) continue
    weightedSum += value * weight
    weightSum += weight
  }

  return weightSum > 0 ? weightedSum / weightSum : 0
}
