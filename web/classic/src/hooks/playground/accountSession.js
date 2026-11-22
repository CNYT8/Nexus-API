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

import { useSyncExternalStore } from 'react';

export const readAccountId = () => {
  try {
    const id = JSON.parse(localStorage.getItem('user'))?.id;
    return id ? String(id) : null;
  } catch {
    return null;
  }
};

let revision = 0;
const listeners = new Set();
const createSession = (userId) => {
  const invalidators = new Set();
  let invalidated = false;
  const session = {
    userId,
    revision: revision++,
    isCurrent: () => getSnapshot() === session && userId !== null,
    onInvalidate: (callback) => {
      if (invalidated) callback();
      else invalidators.add(callback);
      return () => invalidators.delete(callback);
    },
    invalidate: () => {
      invalidated = true;
      invalidators.forEach((callback) => callback());
      invalidators.clear();
    },
  };
  return session;
};
let current = createSession(readAccountId());

const refresh = (force = false) => {
  const userId = readAccountId();
  if (force || userId !== current.userId) {
    const previous = current;
    current = createSession(userId);
    previous.invalidate();
    listeners.forEach((listener) => listener());
  }
  return current;
};
const getSnapshot = () => refresh();

const subscribe = (listener) => {
  listeners.add(listener);
  // storage 不通知写入方；局部订阅写操作可捕获同一批次内的登出/重登录。
  // 最后一个 Playground 订阅卸载时恢复原方法，不改变存储数据格式。
  if (listeners.size === 1) startWatching();
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) stopWatching();
  };
};
let stopWatching = () => {};
const startWatching = () => {
  const restores = [];
  for (const name of ['setItem', 'removeItem', 'clear']) {
    const original = Storage.prototype[name];
    const wrapped = function (...args) {
      const result = original.apply(this, args);
      if (this === localStorage && (name === 'clear' || args[0] === 'user')) {
        refresh();
      }
      return result;
    };
    Storage.prototype[name] = wrapped;
    restores.push(() => {
      if (Storage.prototype[name] === wrapped)
        Storage.prototype[name] = original;
    });
  }
  const onStorage = (event) => {
    if (event.storageArea && event.storageArea !== localStorage) return;
    if (event.key !== null && event.key !== 'user') return;
    // 快速 A→B→A 时读取值可能已回到 A，仍须废弃事件对应的旧会话。
    const idOf = (raw) => {
      try {
        return String(JSON.parse(raw)?.id || '');
      } catch {
        return '';
      }
    };
    refresh(
      event.key === null || idOf(event.oldValue) !== idOf(event.newValue),
    );
  };
  window.addEventListener('storage', onStorage);
  refresh();
  stopWatching = () => {
    restores.forEach((restore) => restore());
    window.removeEventListener('storage', onStorage);
  };
};

export const useAccountSession = () =>
  useSyncExternalStore(subscribe, getSnapshot);
