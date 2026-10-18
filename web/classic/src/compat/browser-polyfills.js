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

// Lightweight browser compatibility shims for legacy Safari / iOS 15.0-15.3
// and older WebKit / Chromium environments. Purely additive: never overrides
// native browser implementations when already present.

const g =
  typeof globalThis !== 'undefined'
    ? globalThis
    : typeof window !== 'undefined'
      ? window
      : typeof self !== 'undefined'
        ? self
        : {};

// 1. Object.hasOwn (Safari 15.4+)
if (typeof Object.hasOwn !== 'function') {
  Object.defineProperty(Object, 'hasOwn', {
    value: function hasOwn(object, property) {
      if (object == null) {
        throw new TypeError('Cannot convert undefined or null to object');
      }
      return Object.prototype.hasOwnProperty.call(Object(object), property);
    },
    configurable: true,
    writable: true,
  });
}

// 2. Array.prototype.at / String.prototype.at (Safari 15.4+)
function atItem(n) {
  let index = Math.trunc(n) || 0;
  if (index < 0) index += this.length;
  if (index < 0 || index >= this.length) return undefined;
  return this[index];
}

if (typeof Array.prototype.at !== 'function') {
  Object.defineProperty(Array.prototype, 'at', {
    value: atItem,
    configurable: true,
    writable: true,
  });
}

if (typeof String.prototype.at !== 'function') {
  Object.defineProperty(String.prototype, 'at', {
    value: atItem,
    configurable: true,
    writable: true,
  });
}

// 3. Array.prototype.findLast / findLastIndex (Safari 15.4+)
if (typeof Array.prototype.findLast !== 'function') {
  Object.defineProperty(Array.prototype, 'findLast', {
    value: function findLast(predicate, thisArg) {
      if (this == null) {
        throw new TypeError(
          'Array.prototype.findLast called on null or undefined',
        );
      }
      if (typeof predicate !== 'function') {
        throw new TypeError('predicate must be a function');
      }
      const list = Object(this);
      const length = list.length >>> 0;
      for (let i = length - 1; i >= 0; i--) {
        const value = list[i];
        if (predicate.call(thisArg, value, i, list)) {
          return value;
        }
      }
      return undefined;
    },
    configurable: true,
    writable: true,
  });
}

if (typeof Array.prototype.findLastIndex !== 'function') {
  Object.defineProperty(Array.prototype, 'findLastIndex', {
    value: function findLastIndex(predicate, thisArg) {
      if (this == null) {
        throw new TypeError(
          'Array.prototype.findLastIndex called on null or undefined',
        );
      }
      if (typeof predicate !== 'function') {
        throw new TypeError('predicate must be a function');
      }
      const list = Object(this);
      const length = list.length >>> 0;
      for (let i = length - 1; i >= 0; i--) {
        if (predicate.call(thisArg, list[i], i, list)) {
          return i;
        }
      }
      return -1;
    },
    configurable: true,
    writable: true,
  });
}

// 4. String.prototype.replaceAll (Safari 13.1+)
if (typeof String.prototype.replaceAll !== 'function') {
  Object.defineProperty(String.prototype, 'replaceAll', {
    value: function replaceAll(searchValue, replaceValue) {
      if (this == null) {
        throw new TypeError(
          'String.prototype.replaceAll called on null or undefined',
        );
      }
      const str = String(this);
      if (searchValue instanceof RegExp) {
        if (!searchValue.global) {
          throw new TypeError(
            'String.prototype.replaceAll called with a non-global RegExp argument',
          );
        }
        return str.replace(searchValue, replaceValue);
      }
      const substr = String(searchValue);
      if (typeof replaceValue === 'function') {
        const parts = str.split(substr);
        let result = parts[0];
        let offset = result.length;
        for (let i = 1; i < parts.length; i++) {
          result += String(replaceValue(substr, offset, str)) + parts[i];
          offset += substr.length + parts[i].length;
        }
        return result;
      }
      return str.split(substr).join(String(replaceValue));
    },
    configurable: true,
    writable: true,
  });
}

// 5. globalThis.structuredClone (Safari 15.4+)
function fallbackStructuredClone(value, seen = new WeakMap()) {
  if (value === null || typeof value !== 'object') {
    return value;
  }
  if (seen.has(value)) {
    return seen.get(value);
  }
  if (value instanceof Date) {
    return new Date(value.getTime());
  }
  if (value instanceof RegExp) {
    return new RegExp(value.source, value.flags);
  }
  if (value instanceof Map) {
    const out = new Map();
    seen.set(value, out);
    value.forEach((v, k) => {
      out.set(
        fallbackStructuredClone(k, seen),
        fallbackStructuredClone(v, seen),
      );
    });
    return out;
  }
  if (value instanceof Set) {
    const out = new Set();
    seen.set(value, out);
    value.forEach((v) => {
      out.add(fallbackStructuredClone(v, seen));
    });
    return out;
  }
  if (ArrayBuffer.isView(value)) {
    if (typeof value.slice === 'function') {
      return value.slice();
    }
    return new value.constructor(value.buffer.slice(0));
  }
  if (value instanceof ArrayBuffer) {
    return value.slice(0);
  }
  if (Array.isArray(value)) {
    const out = new Array(value.length);
    seen.set(value, out);
    for (let i = 0; i < value.length; i++) {
      out[i] = fallbackStructuredClone(value[i], seen);
    }
    return out;
  }
  const proto = Object.getPrototypeOf(value);
  const out =
    proto === null ? Object.create(null) : Object.create(Object.prototype);
  seen.set(value, out);
  for (const key of Object.keys(value)) {
    out[key] = fallbackStructuredClone(value[key], seen);
  }
  return out;
}

if (typeof g.structuredClone !== 'function') {
  g.structuredClone = function structuredClone(value) {
    return fallbackStructuredClone(value);
  };
}

// 6. crypto.randomUUID (Safari 15.4+) using CSPRNG crypto.getRandomValues only
if (
  typeof g.crypto !== 'undefined' &&
  typeof g.crypto.getRandomValues === 'function' &&
  typeof g.crypto.randomUUID !== 'function'
) {
  try {
    g.crypto.randomUUID = function randomUUID() {
      const bytes = new Uint8Array(16);
      g.crypto.getRandomValues(bytes);
      bytes[6] = (bytes[6] & 0x0f) | 0x40;
      bytes[8] = (bytes[8] & 0x3f) | 0x80;
      const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0'));
      return (
        hex.slice(0, 4).join('') +
        '-' +
        hex.slice(4, 6).join('') +
        '-' +
        hex.slice(6, 8).join('') +
        '-' +
        hex.slice(8, 10).join('') +
        '-' +
        hex.slice(10, 16).join('')
      );
    };
  } catch {
    // Ignore if crypto object is frozen in restricted environments
  }
}

// 7. MediaQueryList.prototype.addEventListener / removeEventListener (< Safari 14)
if (
  typeof g.MediaQueryList !== 'undefined' &&
  g.MediaQueryList.prototype &&
  typeof g.MediaQueryList.prototype.addEventListener !== 'function' &&
  typeof g.MediaQueryList.prototype.addListener === 'function'
) {
  g.MediaQueryList.prototype.addEventListener = function addEventListener(
    type,
    listener,
  ) {
    if (type === 'change' && typeof listener === 'function') {
      this.addListener(listener);
    }
  };
  g.MediaQueryList.prototype.removeEventListener = function removeEventListener(
    type,
    listener,
  ) {
    if (type === 'change' && typeof listener === 'function') {
      this.removeListener(listener);
    }
  };
}
