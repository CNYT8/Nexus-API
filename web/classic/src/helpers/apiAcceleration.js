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

const DEFAULT_GET_CACHE_TTL = 1200;
const MAX_GET_CACHE_SIZE = 80;
const CACHEABLE_GET_PATTERNS = [
  /^\/api\/status\/?$/,
  /^\/api\/notice\/?$/,
  /^\/api\/about\/?$/,
  /^\/api\/home_page_content\/?$/,
  /^\/api\/option(?:\/.*)?$/,
  /^\/api\/group\/?$/,
  /^\/api\/uptime\/status\/?$/,
  /^\/api\/user\/self\/?$/,
  /^\/api\/user\/self\/groups\/?$/,
  /^\/api\/user\/token\/?$/,
  /^\/api\/user\/passkey\/?$/,
  /^\/api\/user\/2fa\/status\/?$/,
  /^\/api\/user\/oauth\/bindings\/?$/,
  /^\/api\/admin_permissions\/?$/,
  /^\/api\/deployments\/settings\/?$/,
  /^\/api\/deployments\/hardware-types\/?$/,
];
const UNSAFE_GET_PATTERNS = [
  /\/logout\/?$/,
  /\/oauth\/state\/?$/,
  /\/test(?:\/|$)/,
  /\/update_balance(?:\/|$)/,
  /\/fix(?:\/|$)/,
  /\/ollama\/version(?:\/|$)/,
];
const INVALIDATING_GET_PATTERNS = [
  /\/logout\/?$/,
  /\/update_balance(?:\/|$)/,
  /\/fix(?:\/|$)/,
];

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

function getRequestPath(url) {
  if (typeof url !== 'string') {
    return '';
  }

  try {
    if (typeof URL === 'undefined') {
      throw new Error('URL is not supported');
    }
    const baseUrl =
      typeof window !== 'undefined' ? window.location.origin : 'http://localhost';
    return new URL(url, baseUrl).pathname;
  } catch {
    const [path] = url.split('?');
    return path || '';
  }
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

function shouldCacheGet(url, config = {}) {
  if (config?.disableCache || config?.disableDuplicate) {
    return false;
  }

  const path = getRequestPath(url);
  if (!path || UNSAFE_GET_PATTERNS.some((pattern) => pattern.test(path))) {
    return false;
  }

  return CACHEABLE_GET_PATTERNS.some((pattern) => pattern.test(path));
}

function getCacheTTL(config = {}) {
  if (typeof config.cacheTtl !== 'number') {
    return DEFAULT_GET_CACHE_TTL;
  }
  return Math.max(0, config.cacheTtl);
}

function pruneGetCache(cache) {
  while (cache.size > MAX_GET_CACHE_SIZE) {
    const firstKey = cache.keys().next().value;
    cache.delete(firstKey);
  }
}

function cloneResponseData(data) {
  if (data === null || typeof data !== 'object') {
    return data;
  }

  if (typeof structuredClone === 'function') {
    try {
      return structuredClone(data);
    } catch {}
  }

  try {
    return JSON.parse(JSON.stringify(data));
  } catch {
    return data;
  }
}

function cloneResponse(response) {
  return {
    ...response,
    data: cloneResponseData(response.data),
  };
}

export function installAPIAcceleration(instance, options = {}) {
  const { getUserId = () => '' } = options;
  const originalGet = instance.get.bind(instance);
  const inFlightGetRequests = new Map();
  const cachedGetResponses = new Map();
  let cacheVersion = 0;

  instance.interceptors.request.use((config) => {
    const method = (config.method || 'get').toLowerCase();
    const path = getRequestPath(config.url);

    if (
      method !== 'get' ||
      INVALIDATING_GET_PATTERNS.some((pattern) => pattern.test(path))
    ) {
      cacheVersion += 1;
      cachedGetResponses.clear();
      inFlightGetRequests.clear();
    }

    return config;
  });

  instance.get = (url, config = {}) => {
    if (config?.disableDuplicate) {
      return originalGet(url, config);
    }

    const key = getRequestKey(instance, url, config, getUserId);
    const now = Date.now();
    const requestCacheVersion = cacheVersion;
    const cacheable = shouldCacheGet(url, config);
    const cached = cachedGetResponses.get(key);
    if (cacheable && cached && cached.expiresAt > now) {
      return Promise.resolve(cloneResponse(cached.response));
    }
    if (cached) {
      cachedGetResponses.delete(key);
    }

    if (inFlightGetRequests.has(key)) {
      return inFlightGetRequests.get(key);
    }

    const reqPromise = originalGet(url, config)
      .then((response) => {
        const ttl = getCacheTTL(config);
        if (cacheable && ttl > 0 && requestCacheVersion === cacheVersion) {
          cachedGetResponses.set(key, {
            response: cloneResponse(response),
            expiresAt: Date.now() + ttl,
          });
          pruneGetCache(cachedGetResponses);
        }
        return response;
      })
      .finally(() => {
        if (inFlightGetRequests.get(key) === reqPromise) {
          inFlightGetRequests.delete(key);
        }
      });

    inFlightGetRequests.set(key, reqPromise);
    return reqPromise;
  };
}
