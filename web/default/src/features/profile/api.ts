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
import { AxiosError, CanceledError, type AxiosRequestConfig } from 'axios'
import { useAuthStore } from '@/stores/auth-store'
import { api } from '@/lib/api'
import {
  buildAssertionResult,
  isPasskeySupported,
  prepareCredentialRequestOptions,
} from '@/lib/passkey'
import type { VerificationMethod } from '@/features/auth/secure-verification/types'
import type {
  ApiResponse,
  UserProfile,
  UpdateUserRequest,
  UpdateUserSettingsRequest,
  DeleteAccountRequest,
  CheckinStatusResponse,
  CheckinResponse,
  LoginDeviceList,
  PersonalAccessToken,
  PersonalAccessTokenPage,
  UpdatePersonalAccessToken,
  CreatePersonalAccessToken,
} from './types'

// ============================================================================
// User Profile APIs
// ============================================================================

/**
 * Get current user profile
 */
export async function getUserProfile(): Promise<ApiResponse<UserProfile>> {
  const res = await api.get('/api/user/self')
  return res.data
}

/**
 * Update user profile
 */
export async function updateUserProfile(
  data: UpdateUserRequest
): Promise<ApiResponse> {
  const res = await api.put('/api/user/self', data)
  return res.data
}

/**
 * Update user settings
 */
export async function updateUserSettings(
  data: UpdateUserSettingsRequest
): Promise<ApiResponse> {
  const res = await api.put('/api/user/setting', data)
  return res.data
}

/**
 * Update interface language preference
 */
export async function updateUserLanguage(
  language: string
): Promise<ApiResponse> {
  const res = await api.put('/api/user/self', { language })
  return res.data
}

/**
 * Delete user account
 */
export async function deleteUserAccount(
  data?: DeleteAccountRequest
): Promise<ApiResponse> {
  const res = await api.delete('/api/user/self', { data })
  return res.data
}

async function securityRequest<T>(
  config: AxiosRequestConfig,
  signal: AbortSignal,
  owner: number
): Promise<ApiResponse<T>> {
  const assertOwner = () => {
    if (
      signal.aborted ||
      useAuthStore.getState().auth.user?.id !== owner ||
      localStorage.getItem('uid') !== String(owner)
    ) {
      throw new CanceledError('Security request owner changed')
    }
  }
  assertOwner()
  const transforms = api.defaults.transformRequest
  const response = await api.request<ApiResponse<T>>({
    ...config,
    signal,
    skipBusinessError: true,
    skipErrorHandler: true,
    // Handle HTTP errors here so an old owner's 401 cannot reset a new login.
    validateStatus: () => true,
    transformRequest: [
      function (data, headers) {
        // Runs after shared interceptors, immediately before dispatch.
        assertOwner()
        headers.set('New-Api-User', String(owner))
        return data
      },
      ...(Array.isArray(transforms)
        ? transforms
        : transforms
          ? [transforms]
          : []),
    ],
  })
  assertOwner()
  if (response.status < 200 || response.status >= 300) {
    throw new AxiosError(
      'Security request failed',
      undefined,
      response.config,
      response.request,
      response
    )
  }
  return response.data
}

export function getLoginDevices(signal: AbortSignal, owner: number) {
  return securityRequest<LoginDeviceList>(
    { method: 'get', url: '/api/user/sessions' },
    signal,
    owner
  )
}

export function revokeLoginDevice(
  id: number,
  signal: AbortSignal,
  owner: number
) {
  return securityRequest<{ current: boolean }>(
    { method: 'delete', url: `/api/user/sessions/${id}` },
    signal,
    owner
  )
}

export function revokeOtherLoginDevices(signal: AbortSignal, owner: number) {
  return securityRequest<{ revoked_count: number }>(
    { method: 'post', url: '/api/user/sessions/revoke-others', data: {} },
    signal,
    owner
  )
}

export async function getPersonalAccessTokens(
  signal: AbortSignal,
  owner: number,
  page = 1,
  pageSize = 20
) {
  const response = await securityRequest<PersonalAccessTokenPage>(
    { method: 'get', url: '/api/user/access-tokens', params: { page, page_size: pageSize } },
    signal,
    owner
  )
  const result = response.data
  // Do not mistake the legacy array response for an empty page or a zero limit.
  if (response.success && (!result || !Array.isArray(result.items) ||
    result.page !== page || result.page_size !== pageSize ||
    !Number.isSafeInteger(result.total) || result.total < 0 ||
    !Number.isSafeInteger(result.active_count) || result.active_count < 0 ||
    !Number.isSafeInteger(result.limit) || result.limit <= 0)) {
    throw new Error('Invalid personal access token page')
  }
  return response
}

export function updatePersonalAccessToken(
  id: number,
  data: UpdatePersonalAccessToken,
  signal: AbortSignal,
  owner: number
) {
  return securityRequest<Omit<PersonalAccessToken, 'status'>>(
    { method: 'put', url: `/api/user/access-tokens/${id}`, data },
    signal,
    owner
  )
}

export function createPersonalAccessToken(
  data: CreatePersonalAccessToken,
  signal: AbortSignal,
  owner: number
) {
  return securityRequest<{ token: string; record: PersonalAccessToken }>(
    { method: 'post', url: '/api/user/access-tokens', data },
    signal,
    owner
  )
}

export function revokePersonalAccessToken(
  id: number,
  signal: AbortSignal,
  owner: number
) {
  return securityRequest<unknown>(
    { method: 'delete', url: `/api/user/access-tokens/${id}` },
    signal,
    owner
  )
}

export async function getSecurityVerificationMethods(
  signal: AbortSignal,
  owner: number
) {
  const [twoFA, passkey, supported] = await Promise.all([
    securityRequest<{ enabled: boolean }>(
      { method: 'get', url: '/api/user/2fa/status' },
      signal,
      owner
    ),
    securityRequest<{ enabled: boolean }>(
      { method: 'get', url: '/api/user/passkey' },
      signal,
      owner
    ),
    isPasskeySupported(),
  ])
  if (!twoFA.success || !passkey.success)
    throw new Error('Verification methods unavailable')
  return {
    has2FA: twoFA.data?.enabled === true,
    hasPasskey: passkey.data?.enabled === true,
    passkeySupported: supported,
  }
}

export async function verifySecurityAction(
  method: VerificationMethod,
  code: string,
  signal: AbortSignal,
  owner: number
) {
  if (method === 'passkey') {
    const begin = await securityRequest<{ options?: unknown }>(
      { method: 'post', url: '/api/user/passkey/verify/begin' },
      signal,
      owner
    )
    if (!begin.success) throw new Error('Verification failed')
    const credential = (await navigator.credentials.get({
      publicKey: prepareCredentialRequestOptions(
        begin.data?.options ?? begin.data
      ),
      signal,
    })) as PublicKeyCredential | null
    if (!credential) throw new CanceledError('Passkey verification cancelled')
    const assertion = buildAssertionResult(credential)
    if (!assertion) throw new Error('Verification failed')
    const finish = await securityRequest<unknown>(
      {
        method: 'post',
        url: '/api/user/passkey/verify/finish',
        data: assertion,
      },
      signal,
      owner
    )
    if (!finish.success) throw new Error('Verification failed')
  } else if (method !== '2fa' || !code.trim()) {
    throw new Error('Verification code required')
  }
  const response = await securityRequest<unknown>(
    {
      method: 'post',
      url: '/api/verify',
      data: { method, ...(method === '2fa' ? { code: code.trim() } : {}) },
    },
    signal,
    owner
  )
  if (!response.success) throw new Error('Verification failed')
}

// ============================================================================
// Account Binding APIs
// ============================================================================

/**
 * Send email verification code
 */
export async function sendEmailVerification(
  email: string,
  turnstileToken?: string
): Promise<ApiResponse> {
  const params = new URLSearchParams({ email })
  if (turnstileToken) {
    params.append('turnstile', turnstileToken)
  }
  const res = await api.get(`/api/verification?${params}`)
  return res.data
}

/**
 * Bind email account
 */
export async function bindEmail(
  email: string,
  code: string
): Promise<ApiResponse> {
  const res = await api.post('/api/oauth/email/bind', {
    email,
    code,
  })
  return res.data
}

/**
 * Bind WeChat account
 */
export async function bindWeChat(code: string): Promise<ApiResponse> {
  const res = await api.get(`/api/oauth/wechat/bind?code=${code}`)
  return res.data
}

// ============================================================================
// Custom OAuth Binding APIs
// ============================================================================

export interface CustomOAuthBinding {
  provider_id: string
  provider_name: string
  external_id?: string
}

/**
 * Get current user's custom OAuth bindings
 */
export async function getSelfOAuthBindings(): Promise<
  ApiResponse<CustomOAuthBinding[]>
> {
  const res = await api.get('/api/user/oauth/bindings')
  return res.data
}

/**
 * Unbind a custom OAuth provider for current user
 */
export async function unbindCustomOAuth(
  providerId: string
): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/oauth/bindings/${providerId}`)
  return res.data
}

// ============================================================================
// Checkin APIs
// ============================================================================

/**
 * Get checkin status for a specific month
 */
export async function getCheckinStatus(
  month: string
): Promise<ApiResponse<CheckinStatusResponse>> {
  const res = await api.get(`/api/user/checkin?month=${month}`)
  return res.data
}

/**
 * Perform daily checkin
 */
export async function performCheckin(
  turnstileToken?: string
): Promise<ApiResponse<CheckinResponse>> {
  const url = turnstileToken
    ? `/api/user/checkin?turnstile=${encodeURIComponent(turnstileToken)}`
    : '/api/user/checkin'
  const res = await api.post(url)
  return res.data
}
