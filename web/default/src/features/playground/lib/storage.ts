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
import { useAuthStore } from '@/stores/auth-store'
import { STORAGE_KEYS } from '../constants'
import type { PlaygroundConfig, ParameterEnabled, Message } from '../types'
import { sanitizeMessagesOnLoad } from './message-utils'

function storageKey(baseKey: string): string | null {
  const userId = useAuthStore.getState().auth.user?.id
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
export function loadConfig(): Partial<PlaygroundConfig> {
  try {
    const key = storageKey(STORAGE_KEYS.CONFIG)
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
export function saveConfig(config: Partial<PlaygroundConfig>): void {
  try {
    const key = storageKey(STORAGE_KEYS.CONFIG)
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
export function loadParameterEnabled(): Partial<ParameterEnabled> {
  try {
    const key = storageKey(STORAGE_KEYS.PARAMETER_ENABLED)
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
  parameterEnabled: Partial<ParameterEnabled>
): void {
  try {
    const key = storageKey(STORAGE_KEYS.PARAMETER_ENABLED)
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
export function loadMessages(): Message[] | null {
  try {
    const key = storageKey(STORAGE_KEYS.MESSAGES)
    if (!key) return null
    const saved = localStorage.getItem(key)
    if (saved) {
      const parsed: unknown = JSON.parse(saved)
      if (!Array.isArray(parsed)) {
        return null
      }
      const sanitized = sanitizeMessagesOnLoad(parsed as Message[])
      // Persist sanitized result to avoid re-sanitizing on subsequent loads
      if (sanitized !== parsed) {
        saveMessages(sanitized)
      }
      return sanitized
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
export function saveMessages(messages: Message[]): void {
  try {
    const key = storageKey(STORAGE_KEYS.MESSAGES)
    if (!key) return
    // Inline base64 images can be several MB each; writing them into
    // localStorage would blow the quota and silently lose all history.
    const sanitized = messages.map((message) => {
      const versions = Array.isArray(message.versions)
        ? message.versions.map((version) =>
            Array.isArray(version.images) && version.images.length > 0
              ? { ...version, images: [] }
              : version
          )
        : message.versions
      return { ...message, versions }
    })
    localStorage.setItem(key, JSON.stringify(sanitized))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save messages:', error)
  }
}

/**
 * Clear all playground data
 */
export function clearPlaygroundData(): void {
  try {
    const configKey = storageKey(STORAGE_KEYS.CONFIG)
    const paramKey = storageKey(STORAGE_KEYS.PARAMETER_ENABLED)
    const messagesKey = storageKey(STORAGE_KEYS.MESSAGES)
    if (configKey) localStorage.removeItem(configKey)
    if (paramKey) localStorage.removeItem(paramKey)
    if (messagesKey) localStorage.removeItem(messagesKey)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to clear playground data:', error)
  }
}
