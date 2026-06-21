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

function stableStringify(value) {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (
    typeof URLSearchParams !== 'undefined' &&
    value instanceof URLSearchParams
  ) {
    return stableStringify(Array.from(value.entries()).sort());
  }
  if (value instanceof Date) {
    return JSON.stringify(value.toISOString());
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => stableStringify(item)).join(',')}]`;
  }
  return `{${Object.keys(value)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${stableStringify(value[key])}`)
    .join(',')}}`;
}

function getHeaderValue(headers, name) {
  if (!headers) {
    return '';
  }

  if (typeof headers.get === 'function') {
    return headers.get(name) || '';
  }

  return headers[name] || headers[name.toLowerCase()] || '';
}

function getRequestUserKey(instance, config = {}, getUserId) {
  return String(
    getHeaderValue(config.headers, 'New-API-User') ||
      getHeaderValue(instance.defaults.headers?.common, 'New-API-User') ||
      getHeaderValue(instance.defaults.headers, 'New-API-User') ||
      getUserId() ||
      '',
  );
}

function getRequestKey(instance, url, config = {}, getUserId) {
  const params = config.params ? stableStringify(config.params) : '{}';
  return `${getRequestUserKey(instance, config, getUserId)}|${url}?${params}`;
}

export function installAPIAcceleration(instance, options = {}) {
  const { getUserId = () => '' } = options;
  const originalGet = instance.get.bind(instance);
  const inFlightGetRequests = new Map();

  instance.interceptors.request.use((config) => {
    const method = (config.method || 'get').toLowerCase();

    if (method !== 'get') {
      inFlightGetRequests.clear();
    }

    return config;
  });

  instance.get = (url, config = {}) => {
    if (config?.disableDuplicate) {
      return originalGet(url, config);
    }

    const key = getRequestKey(instance, url, config, getUserId);
    if (inFlightGetRequests.has(key)) {
      return inFlightGetRequests.get(key);
    }

    const reqPromise = originalGet(url, config).finally(() => {
      if (inFlightGetRequests.get(key) === reqPromise) {
        inFlightGetRequests.delete(key);
      }
    });

    inFlightGetRequests.set(key, reqPromise);
    return reqPromise;
  };
}
