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

import { API } from './index';

const PERFORMANCE_WINDOW_HOURS = 24;
const PERFORMANCE_CACHE_MS = 60 * 1000;
const performanceCache = new Map();
const performanceRequests = new Map();
let performanceSummaryCache = null;
let performanceSummaryRequest = null;

export function getCachedModelPerformanceMetrics(modelName) {
  const cached = performanceCache.get(modelName);
  if (!cached) return null;
  if (cached.expiresAt <= Date.now()) {
    performanceCache.delete(modelName);
    return null;
  }
  return cached.groups;
}

export function prefetchModelPerformanceMetrics(modelName) {
  if (!modelName) return Promise.resolve([]);
  const cachedGroups = getCachedModelPerformanceMetrics(modelName);
  if (cachedGroups) return Promise.resolve(cachedGroups);
  const existingRequest = performanceRequests.get(modelName);
  if (existingRequest) return existingRequest;

  const request = API.get('/api/perf-metrics', {
    params: {
      model: modelName,
      hours: PERFORMANCE_WINDOW_HOURS,
    },
    skipErrorHandler: true,
  })
    .then((res) => {
      if (!res.data?.success) {
        throw new Error(res.data?.message || 'failed to load perf metrics');
      }
      const groups = Array.isArray(res.data?.data?.groups)
        ? res.data.data.groups
        : [];
      performanceCache.set(modelName, {
        groups,
        expiresAt: Date.now() + PERFORMANCE_CACHE_MS,
      });
      return groups;
    })
    .finally(() => {
      performanceRequests.delete(modelName);
    });

  performanceRequests.set(modelName, request);
  return request;
}

export function getCachedModelPerformanceSummary() {
  if (!performanceSummaryCache) return null;
  if (performanceSummaryCache.expiresAt <= Date.now()) {
    performanceSummaryCache = null;
    return null;
  }
  return performanceSummaryCache.models;
}

export function prefetchModelPerformanceSummary() {
  const cachedModels = getCachedModelPerformanceSummary();
  if (cachedModels) return Promise.resolve(cachedModels);
  if (performanceSummaryRequest) return performanceSummaryRequest;

  const request = API.get('/api/perf-metrics/summary', {
    params: { hours: PERFORMANCE_WINDOW_HOURS },
    skipErrorHandler: true,
  })
    .then((res) => {
      if (!res.data?.success) {
        throw new Error(res.data?.message || 'failed to load perf metrics');
      }
      const models = Array.isArray(res.data?.data?.models)
        ? res.data.data.models
        : [];
      performanceSummaryCache = {
        models,
        expiresAt: Date.now() + PERFORMANCE_CACHE_MS,
      };
      return models;
    })
    .finally(() => {
      performanceSummaryRequest = null;
    });

  performanceSummaryRequest = request;
  return request;
}
