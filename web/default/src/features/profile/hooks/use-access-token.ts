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
import { useCallback, useRef, useState } from 'react'
import { isAxiosError } from 'axios'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import {
  createPersonalAccessToken,
  getPersonalAccessTokens,
  revokePersonalAccessToken,
  updatePersonalAccessToken,
} from '../api'
import type { CreatePersonalAccessToken, UpdatePersonalAccessToken } from '../types'
import { useSecurityResource } from './use-security-resource'
import { useSecurityVerification } from './use-security-verification'

// ============================================================================
// Access Token Hook
// ============================================================================

const TOKEN_ERROR_KEYS: Record<string, string> = {
  ACCESS_TOKEN_INVALID: '个人访问令牌参数无效，请检查名称和权限',
  ACCESS_TOKEN_NOT_ACTIVE: '个人访问令牌不存在或已失效，请刷新列表',
  ACCESS_TOKEN_LIMIT: '已达到个人访问令牌数量上限，请先撤销不再使用的令牌',
  ACCESS_TOKEN_ERROR: '个人访问令牌操作失败，请稍后重试',
}

export function useAccessToken() {
  const { t } = useTranslation()
  const [tokenErrorKey, setTokenErrorKey] = useState('')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const load = useCallback(
    (signal: AbortSignal, owner: number) =>
      getPersonalAccessTokens(signal, owner, page, pageSize),
    [page, pageSize]
  )
  const resource = useSecurityResource(load)
  const verification = useSecurityVerification()
  const [secret, setSecret] = useState('')
  const [pending, setPending] = useState(false)
  const locked = useRef(false)

  const tokenAction = async <T,>(request: () => Promise<T>, signal: AbortSignal, owner: number): Promise<T> => {
    try {
      return await request()
    } catch (cause) {
      if (!signal.aborted && useAuthStore.getState().auth.user?.id === owner &&
        localStorage.getItem('uid') === String(owner) && isAxiosError<{ code?: string }>(cause)) {
        const key = TOKEN_ERROR_KEYS[cause.response?.data?.code ?? '']
        if (key) setTokenErrorKey(key)
      }
      // Keep the original response, including VERIFICATION_REQUIRED, intact.
      throw cause
    }
  }

  const authorize = async (action: () => Promise<unknown>) => {
    if (locked.current) return
    locked.current = true
    setTokenErrorKey('')
    setPending(true)
    try {
      return await verification.withVerification(action)
    } finally {
      locked.current = false
      setPending(false)
    }
  }

  const create = (data: CreatePersonalAccessToken, onSuccess: () => void) =>
    authorize(() =>
      resource.run(
        (signal, owner) => tokenAction(() => createPersonalAccessToken(data, signal, owner), signal, owner),
        (result) => {
          if (result?.token) setSecret(result.token)
          onSuccess()
        }
      )
    )

  const update = (id: number, data: UpdatePersonalAccessToken, onSuccess: () => void) =>
    authorize(() =>
      resource.run(
        (signal, owner) => tokenAction(() => updatePersonalAccessToken(id, data, signal, owner), signal, owner),
        onSuccess
      )
    )

  const revoke = (id: number, onSuccess: () => void) =>
    authorize(() =>
      resource.run(
        (signal, owner) => tokenAction(() => revokePersonalAccessToken(id, signal, owner), signal, owner),
        onSuccess
      )
    )

  return {
    ...resource,
    error: verification.error || (tokenErrorKey ? t(tokenErrorKey) : resource.error),
    refresh: () => {
      setTokenErrorKey('')
      return resource.refresh()
    },
    secret,
    clearSecret: () => setSecret(''),
    create,
    update,
    revoke,
    page,
    pageSize,
    setPage,
    changePageSize: (size: number) => {
      if (![10, 20, 50, 100].includes(size)) return
      setPage(1)
      setPageSize(size)
    },
    verification,
    busy: resource.busy || pending || verification.open,
  }
}
