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
import { STORAGE_KEYS } from '../constants'
import type { PlaygroundConfig, ParameterEnabled, Message } from '../types'
import { sanitizeMessagesOnLoad } from './message-utils'
import { messageHistoryAdapter, restoreMessages } from './message-schema'
import { trimHistory, writeHistory } from '../../../../../shared/playground-history-storage'

function storageKey(baseKey: string, userId: string | null): string | null {
  if (!userId) return null
  // Drop pre-isolation keys that had no owner and were shared across accounts.
  for (const legacyKey of Object.values(STORAGE_KEYS)) {
    localStorage.removeItem(legacyKey)
  }
  return `${baseKey}:u${userId}`
}
/**
 * Load playground config from localStorage
 */
export function loadConfig(userId: string | null): Partial<PlaygroundConfig> {
  try {
    const key = storageKey(STORAGE_KEYS.CONFIG, userId)
    if (!key) return {}
    const saved = localStorage.getItem(key)
    if (saved) {
      return JSON.parse(saved)
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load config:', error)
  }
  return {}
}

/**
 * Save playground config to localStorage
 */
export function saveConfig(
  config: Partial<PlaygroundConfig>,
  userId: string | null
): void {
  try {
    const key = storageKey(STORAGE_KEYS.CONFIG, userId)
    if (!key) return
    localStorage.setItem(key, JSON.stringify(config))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save config:', error)
  }
}

/**
 * Load parameter enabled state from localStorage
 */
export function loadParameterEnabled(
  userId: string | null
): Partial<ParameterEnabled> {
  try {
    const key = storageKey(STORAGE_KEYS.PARAMETER_ENABLED, userId)
    if (!key) return {}
    const saved = localStorage.getItem(key)
    if (saved) {
      return JSON.parse(saved)
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load parameter enabled:', error)
  }
  return {}
}

/**
 * Save parameter enabled state to localStorage
 */
export function saveParameterEnabled(
  parameterEnabled: Partial<ParameterEnabled>,
  userId: string | null
): void {
  try {
    const key = storageKey(STORAGE_KEYS.PARAMETER_ENABLED, userId)
    if (!key) return
    localStorage.setItem(key, JSON.stringify(parameterEnabled))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save parameter enabled:', error)
  }
}

/**
 * Load messages from localStorage
 */
export function loadMessages(userId: string | null): Message[] | null {
  try {
    const key = storageKey(STORAGE_KEYS.MESSAGES, userId)
    if (!key) return null
    const saved = localStorage.getItem(key)
    if (saved) {
      const parsed: unknown = JSON.parse(saved)
      // Recover old arrays/envelopes row by row before applying current limits.
      // Loading does not rewrite the old snapshot (important on quota failure).
      return trimHistory(sanitizeMessagesOnLoad(restoreMessages(parsed)), messageHistoryAdapter)
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load messages:', error)
  }
  return null
}

/**
 * Save messages to localStorage
 */
export function saveMessages(messages: Message[], userId: string | null): boolean {
  try {
    const key = storageKey(STORAGE_KEYS.MESSAGES, userId)
    if (!key) return false
    return writeHistory(localStorage, key, messages, messageHistoryAdapter)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save messages:', error)
    return false
  }
}

/**
 * Clear all playground data
 */
export function clearPlaygroundData(userId: string | null): void {
  try {
    const configKey = storageKey(STORAGE_KEYS.CONFIG, userId)
    const paramKey = storageKey(STORAGE_KEYS.PARAMETER_ENABLED, userId)
    const messagesKey = storageKey(STORAGE_KEYS.MESSAGES, userId)
    if (configKey) localStorage.removeItem(configKey)
    if (paramKey) localStorage.removeItem(paramKey)
    if (messagesKey) localStorage.removeItem(messagesKey)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to clear playground data:', error)
  }
}
