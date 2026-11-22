import i18next from 'i18next';
import { Toast } from '@douyinfe/semi-ui';

export const safeServerErrorMessage = Symbol('safeServerErrorMessage');
const notifiedErrors = new WeakSet();
const isRecord = (value) => value !== null && typeof value === 'object';

export function getServerErrorSources(value) {
  const sources = [];
  const pending = [value];
  const seen = new Set();
  for (let index = 0; index < pending.length; index++) {
    const source = pending[index];
    if (!isRecord(source) || seen.has(source)) continue;
    seen.add(source);
    sources.push(source);
    if (source[safeServerErrorMessage]) continue;
    if (isRecord(source.response)) pending.push(source.response.data);
    pending.push(source.cause, source.error);
  }
  return sources;
}

function messageText(value) {
  if (typeof value !== 'string' || !value.trim() || value.trim() === 'success') return undefined;
  if (/^\s*(?:<!doctype|<html[\s>])/i.test(value)) return undefined;
  return value;
}

export function translateServerText(t, text) {
  return t(text, { skipInterpolation: true, keySeparator: false, nsSeparator: false });
}

export function getServerMessage(value) {
  if (!isRecord(value)) return messageText(value);
  if (value[safeServerErrorMessage]) return messageText(value.message);
  const key = messageText(value.message_key);
  if (key && i18next.exists(key, { keySeparator: false, nsSeparator: false })) {
    const params = isRecord(value.message_params) ? value.message_params : {};
    const result = i18next.t(key, {
      replace: params,
      count: typeof params.count === 'number' ? params.count : undefined,
      keySeparator: false,
      nsSeparator: false,
      returnObjects: false,
      interpolation: { escapeValue: false, skipOnVariables: true, nestingPrefix: '\u0000' },
    });
    if (typeof result === 'string') return messageText(result);
  }
  const message = messageText(value.message);
  if (message) return translateServerText(i18next.t.bind(i18next), message);
  const detail = messageText(value.error) || messageText(value.title);
  if (detail) return translateServerText(i18next.t.bind(i18next), detail);
  return key;
}

export function getServerErrorStatus(value) {
  for (const source of getServerErrorSources(value)) {
    const status = isRecord(source.response) ? source.response.status : source.status;
    if (typeof status === 'number') return status;
  }
  return undefined;
}

export function isServerErrorCancelled(value) {
  return getServerErrorSources(value).some((source) =>
    source.__CANCEL__ === true || source.code === 'ERR_CANCELED' ||
    source.name === 'AbortError' || source.name === 'CancelledError',
  );
}

export function getServerErrorMessage(value, fallback) {
  const sources = getServerErrorSources(value);
  const safe = sources.find((source) => source[safeServerErrorMessage]);
  if (safe) return getServerMessage(safe) || fallback || i18next.t('请求失败');
  for (const source of sources) {
    if (isRecord(source.response)) {
      const message = getServerMessage(source.response.data);
      if (message) return message;
    }
    if (!(source instanceof Error) && !source.isAxiosError && !isRecord(source.response)) {
      const message = getServerMessage(source);
      if (message) return message;
    }
  }
  for (const source of [...sources].reverse()) {
    const message = getServerMessage(source);
    if (message) return message;
  }
  const text = messageText(value);
  return (text && translateServerText(i18next.t.bind(i18next), text)) || fallback || i18next.t('请求失败');
}

export function isServerErrorNotified(value) {
  return getServerErrorSources(value).some((source) => notifiedErrors.has(source));
}

export function markServerErrorNotified(value) {
  getServerErrorSources(value).forEach((source) => notifiedErrors.add(source));
}

export function notifyServerError(value, fallback) {
  if (isServerErrorCancelled(value) || isServerErrorNotified(value)) return;
  markServerErrorNotified(value);
  Toast.error(getServerErrorMessage(value, fallback));
}

export function createServerError(value, fallback) {
  const error = new Error(getServerErrorMessage(value, fallback), { cause: value });
  if (isServerErrorNotified(value)) notifiedErrors.add(error);
  return error;
}

export function requireServerSuccess(response) {
  if (isRecord(response) && response.success === false) throw createServerError(response);
  return response;
}
