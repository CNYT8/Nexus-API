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

// Lightweight browser compatibility shims for legacy Safari / iOS 15.0-15.3.
// Purely additive: never overrides native browser implementations.

const g: Record<string, unknown> =
  typeof globalThis !== 'undefined'
    ? (globalThis as unknown as Record<string, unknown>)
    : typeof window !== 'undefined'
      ? (window as unknown as Record<string, unknown>)
      : {}

if (typeof Object.hasOwn !== 'function') {
  Object.defineProperty(Object, 'hasOwn', {
    value: function hasOwn(object: unknown, property: PropertyKey) {
      if (object == null) {
        throw new TypeError('Cannot convert undefined or null to object')
      }
      return Object.prototype.hasOwnProperty.call(Object(object), property)
    },
    configurable: true,
    writable: true,
  })
}

function fallbackStructuredClone<T>(
  value: T,
  seen = new WeakMap<object, unknown>()
): T {
  if (value === null || typeof value !== 'object') {
    return value
  }
  const obj = value as unknown as object
  if (seen.has(obj)) {
    return seen.get(obj) as T
  }
  if (value instanceof Date) {
    return new Date(value.getTime()) as unknown as T
  }
  if (value instanceof RegExp) {
    return new RegExp(value.source, value.flags) as unknown as T
  }
  if (value instanceof Map) {
    const out = new Map()
    seen.set(obj, out)
    value.forEach((v, k) => {
      out.set(
        fallbackStructuredClone(k, seen),
        fallbackStructuredClone(v, seen)
      )
    })
    return out as unknown as T
  }
  if (value instanceof Set) {
    const out = new Set()
    seen.set(obj, out)
    value.forEach((v) => {
      out.add(fallbackStructuredClone(v, seen))
    })
    return out as unknown as T
  }
  if (Array.isArray(value)) {
    const out = new Array(value.length)
    seen.set(obj, out)
    for (let i = 0; i < value.length; i++) {
      out[i] = fallbackStructuredClone(value[i], seen)
    }
    return out as unknown as T
  }
  const proto = Object.getPrototypeOf(obj)
  const out = (
    proto === null ? Object.create(null) : Object.create(Object.prototype)
  ) as Record<string, unknown>
  seen.set(obj, out)
  for (const key of Object.keys(obj as Record<string, unknown>)) {
    out[key] = fallbackStructuredClone(
      (obj as Record<string, unknown>)[key],
      seen
    )
  }
  return out as unknown as T
}

if (typeof g.structuredClone !== 'function') {
  g.structuredClone = function structuredClone<T>(value: T): T {
    return fallbackStructuredClone(value)
  }
}
