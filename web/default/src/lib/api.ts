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
import axios, { type AxiosRequestConfig } from 'axios'
import { normalizeInterfaceLanguage } from '@/i18n/languages'
import i18next, { t } from 'i18next'
import { useAuthStore } from '@/stores/auth-store'
import {
  getServerMessage,
  isServerErrorCancelled,
  markServerErrorNotified,
  notifyServerError,
  safeServerErrorMessage,
} from '@/lib/server-error-message'
import {
  dispatchWebRiskVerificationRequired,
  isWebRiskVerificationRequired,
} from '@/lib/web-risk'

declare module 'axios' {
  export interface AxiosRequestConfig {
    skipBusinessError?: boolean
    skipErrorHandler?: boolean
    disableDuplicate?: boolean
  }
}

export type ApiRequestConfig = AxiosRequestConfig

// ============================================================================
// Axios Instance Configuration
// ============================================================================

// Base URL: empty string for same-origin API requests
const baseURL = ''

// Create axios instance with default config
export const api = axios.create({
  baseURL,
  withCredentials: true, // Include cookies in cross-origin requests
  headers: {
    'Cache-Control': 'no-store', // Prevent caching
  },
})

// ============================================================================
// Request Deduplication
// ============================================================================

// Deduplicate concurrent GET requests to the same URL
// Prevents multiple identical requests from being sent simultaneously
const inFlightGet = new Map<string, Promise<unknown>>()
const originalGet = api.get.bind(api)

api.get = ((url: string, config: ApiRequestConfig = {}) => {
  const disableDuplicate = config.disableDuplicate
  if (disableDuplicate) return originalGet(url, config)

  const params = config.params ? JSON.stringify(config.params) : '{}'
  const language =
    config.headers instanceof axios.AxiosHeaders
      ? config.headers.get('Accept-Language') || getRequestLanguage()
      : Object.entries(config.headers || {}).find(
          ([key]) => key.toLowerCase() === 'accept-language'
        )?.[1] || getRequestLanguage()
  const key = `${getUserId() || ''}|${language}|${Boolean(config.skipBusinessError)}|${Boolean(config.skipErrorHandler)}|${url}?${params}`

  // Return existing in-flight request if available
  if (inFlightGet.has(key)) return inFlightGet.get(key)!

  // Create new request and clean up after completion
  const req = originalGet(url, config).finally(() => inFlightGet.delete(key))
  inFlightGet.set(key, req)
  return req
}) as typeof api.get

// ============================================================================
// Response Interceptor
// ============================================================================

// Handle business logic errors and HTTP errors globally
api.interceptors.response.use(
  (response) => {
    const skipBusiness = response.config.skipBusinessError
    const message = getServerMessage(response.data)
    if (response.data && typeof response.data === 'object' && message) {
      response.data.message = message
    }

    // Unified business response format: { success, message, data }
    if (
      !skipBusiness &&
      response &&
      response.data &&
      typeof response.data.success === 'boolean'
    ) {
      if (!response.data.success) {
        notifyServerError(response.data)
      }
    }
    return response
  },
  (error) => {
    if (isServerErrorCancelled(error)) return Promise.reject(error)
    if (isWebRiskVerificationRequired(error)) {
      dispatchWebRiskVerificationRequired(
        error?.response?.data?.data?.turnstile_site_key || ''
      )
      markServerErrorNotified(error)
      return Promise.reject(error)
    }
    const skip = error?.config?.skipErrorHandler
    const status = error?.response?.status

    if (status === 401) {
      try {
        useAuthStore.getState().auth.reset()
      } catch {
        /* empty */
      }

      error.message = t('Session expired!')
      error[safeServerErrorMessage] = true
    }
    if (!skip) notifyServerError(error)
    return Promise.reject(error)
  }
)

// ============================================================================
// Common Headers Utility
// ============================================================================

/**
 * Get user ID from localStorage
 */
function getUserId(): string | null {
  try {
    if (typeof window !== 'undefined') {
      return window.localStorage.getItem('uid')
    }
  } catch {
    /* empty */
  }
  return null
}

/**
 * Get common request headers (for both axios and SSE requests)
 */
function getRequestLanguage(): string {
  const language = normalizeInterfaceLanguage(
    i18next.resolvedLanguage || i18next.language
  )
  return language === 'zh' ? 'zh-CN' : language
}

export function getCommonHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'Accept-Language': getRequestLanguage(),
  }

  const uid = getUserId()
  if (uid) {
    headers['New-Api-User'] = uid
  }

  return headers
}

// ============================================================================
// Request Interceptor
// ============================================================================

// Attach user ID header for all requests
api.interceptors.request.use((config) => {
  if (!config.headers.has('Accept-Language')) {
    config.headers.set('Accept-Language', getRequestLanguage())
  }
  const uid = getUserId()
  if (uid) {
    // Custom header for user identification
    ;(config.headers as Record<string, string>)['New-Api-User'] = uid
  }
  return config
})

// ============================================================================
// Common API Functions
// ============================================================================

// ----------------------------------------------------------------------------
// User APIs
// ----------------------------------------------------------------------------

// Get current user info
export async function getSelf() {
  const res = await api.get('/api/user/self', {
    // Avoid global 401 toast during guards/preloads
    skipErrorHandler: true,
  })
  return res.data
}

// Get user available models
export async function getUserModels(): Promise<{
  success: boolean
  message?: string
  data?: string[]
}> {
  const res = await api.get('/api/user/models')
  return res.data
}

// Get user groups with descriptions and ratios
export async function getUserGroups(): Promise<{
  success: boolean
  message?: string
  data?: Record<string, { desc: string; ratio: number | string }>
}> {
  const res = await api.get('/api/user/self/groups')
  return res.data
}

// ----------------------------------------------------------------------------
// System APIs
// ----------------------------------------------------------------------------

// Get system status
export async function getStatus() {
  const res = await api.get('/api/status')
  return res.data?.data as Record<string, unknown>
}

// Get system notice
export async function getNotice(): Promise<{
  success: boolean
  message?: string
  data?: string
}> {
  const res = await api.get('/api/notice')
  return res.data
}

// ----------------------------------------------------------------------------
// 2FA Management APIs
// ----------------------------------------------------------------------------

// Get 2FA status
export async function get2FAStatus() {
  const res = await api.get('/api/user/2fa/status')
  return res.data
}

// Setup 2FA
export async function setup2FA() {
  const res = await api.post('/api/user/2fa/setup')
  return res.data
}

// Enable 2FA with verification code
export async function enable2FA(code: string) {
  const res = await api.post('/api/user/2fa/enable', { code })
  return res.data
}

// Disable 2FA with verification code
export async function disable2FA(code: string) {
  const res = await api.post('/api/user/2fa/disable', { code })
  return res.data
}

// Regenerate 2FA backup codes
export async function regenerate2FABackupCodes(code: string) {
  const res = await api.post('/api/user/2fa/backup_codes', { code })
  return res.data
}
