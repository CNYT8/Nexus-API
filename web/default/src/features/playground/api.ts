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
import type { AxiosRequestHeaders } from 'axios'
import { api } from '@/lib/api'
import { API_ENDPOINTS } from './constants'
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  ModelOption,
  GroupOption,
} from './types'

// 请求拦截器之后仍使用页面所属账号，避免异步调度时读取到其他账号的 uid。
function accountRequestOptions(userId: string | null, signal?: AbortSignal) {
  return {
    signal,
    skipErrorHandler: true,
    disableDuplicate: true,
    headers: { 'Content-Type': 'application/json' },
    transformRequest: [
      (data: unknown, headers: AxiosRequestHeaders) => {
        if (userId) headers.set('New-Api-User', userId)
        return data === undefined ? undefined : JSON.stringify(data)
      },
    ],
  }
}

/**
 * Send chat completion request (non-streaming)
 */
export async function sendChatCompletion(
  payload: ChatCompletionRequest,
  userId: string | null,
  signal: AbortSignal
): Promise<ChatCompletionResponse> {
  const res = await api.post(
    API_ENDPOINTS.CHAT_COMPLETIONS,
    payload,
    accountRequestOptions(userId, signal)
  )
  return res.data
}

/**
 * Get user available models
 */
export async function getUserModels(
  userId: string | null,
  group: string,
  signal: AbortSignal
): Promise<ModelOption[]> {
  const res = await api.get(
    API_ENDPOINTS.USER_MODELS,
    { ...accountRequestOptions(userId, signal), params: { group } }
  )
  const { data } = res

  if (!data.success || !Array.isArray(data.data)) {
    return []
  }

  return data.data.map((model: string) => ({
    label: model,
    value: model,
  }))
}

/**
 * Get user groups
 */
export async function getUserGroups(
  userId: string | null,
  signal: AbortSignal
): Promise<GroupOption[]> {
  const res = await api.get(
    API_ENDPOINTS.USER_GROUPS,
    accountRequestOptions(userId, signal)
  )
  const { data } = res

  if (!data.success || !data.data) {
    return []
  }

  const groupData = data.data as Record<string, { desc: string; ratio: number }>

  // label is for button display (name only); desc is for dropdown content
  return Object.entries(groupData).map(([group, info]) => ({
    label: group,
    value: group,
    ratio: info.ratio,
    desc: info.desc,
  }))
}
