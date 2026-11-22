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
import { api } from '@/lib/api'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'
import type {
  AdminPermissionsResponse,
  UpdateAdminPermissionsResponse,
} from './types'

async function permissionRequest<T>(
  config: AxiosRequestConfig,
  signal: AbortSignal,
  owner: number
): Promise<T> {
  const assertOwner = () => {
    if (signal.aborted || useAuthStore.getState().auth.user?.id !== owner ||
      localStorage.getItem('uid') !== String(owner)) {
      throw new CanceledError('Admin permission request owner changed')
    }
  }
  assertOwner()
  const transforms = api.defaults.transformRequest
  const response = await api.request<T>({
    ...config,
    signal,
    skipBusinessError: true,
    skipErrorHandler: true,
    validateStatus: () => true,
    transformRequest: [
      function (data, headers) {
        assertOwner()
        headers.set('New-Api-User', String(owner))
        return data
      },
      ...(Array.isArray(transforms) ? transforms : transforms ? [transforms] : []),
    ],
  })
  assertOwner()
  if (response.status < 200 || response.status >= 300) {
    throw new AxiosError('Admin permission request failed', undefined, response.config, response.request, response)
  }
  return response.data
}

export function getAdminPermissions(signal: AbortSignal, owner: number) {
  return permissionRequest<AdminPermissionsResponse>(
    { method: 'get', url: '/api/admin_permissions/' }, signal, owner
  )
}

export function updateAdminPermissions(
  id: number,
  permissions: Record<string, boolean>,
  signal: AbortSignal,
  owner: number
) {
  return permissionRequest<UpdateAdminPermissionsResponse>(
    { method: 'put', url: `/api/admin_permissions/${id}`, data: { permissions } }, signal, owner
  )
}

export function getAdminPermissionSelf(signal: AbortSignal, owner: number) {
  return permissionRequest<{ success: boolean; message: string; data?: AuthUser }>(
    { method: 'get', url: '/api/user/self' }, signal, owner
  )
}
