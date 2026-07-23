/*
Copyright (C) 2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import { api } from '@/lib/api'
import type {
  ApiResponse,
  TicketDetail,
  TicketList,
  TicketSettings,
  TicketStatus,
  TicketType,
} from './types'

type PageParams = {
  page: number
  pageSize: number
}

const toPageParams = (params: PageParams) => ({
  p: params.page,
  page_size: params.pageSize,
})

export async function getTicketSettings(): Promise<ApiResponse<TicketSettings>> {
  const res = await api.get<ApiResponse<TicketSettings>>('/api/tickets/settings')
  return res.data
}

export async function updateTicketSettings(
  settings: TicketSettings
): Promise<ApiResponse<TicketSettings>> {
  const res = await api.put<ApiResponse<TicketSettings>>(
    '/api/tickets/settings',
    settings,
    { skipBusinessError: true }
  )
  return res.data
}

export async function getMyTickets(
  params: PageParams
): Promise<ApiResponse<TicketList>> {
  const res = await api.get<ApiResponse<TicketList>>('/api/tickets/self', {
    params: toPageParams(params),
  })
  return res.data
}

export async function getMyTicket(id: number): Promise<ApiResponse<TicketDetail>> {
  const res = await api.get<ApiResponse<TicketDetail>>(`/api/tickets/${id}`)
  return res.data
}

export async function createMyTicket(
  type: TicketType,
  content: string
): Promise<ApiResponse<TicketDetail>> {
  const res = await api.post<ApiResponse<TicketDetail>>('/api/tickets/', {
    type,
    content,
  }, { skipBusinessError: true })
  return res.data
}

export async function replyMyTicket(
  id: number,
  content: string
): Promise<ApiResponse<TicketDetail>> {
  const res = await api.post<ApiResponse<TicketDetail>>(
    `/api/tickets/${id}/replies`,
    { content },
    { skipBusinessError: true }
  )
  return res.data
}

export async function getAdminTickets(
  params: PageParams & { status?: TicketStatus }
): Promise<ApiResponse<TicketList>> {
  const res = await api.get<ApiResponse<TicketList>>('/api/tickets/admin/', {
    params: {
      ...toPageParams(params),
      ...(params.status ? { status: params.status } : {}),
    },
  })
  return res.data
}

export async function getAdminTicket(
  id: number
): Promise<ApiResponse<TicketDetail>> {
  const res = await api.get<ApiResponse<TicketDetail>>(
    `/api/tickets/admin/${id}`
  )
  return res.data
}

export async function replyAdminTicket(
  id: number,
  content: string
): Promise<ApiResponse<TicketDetail>> {
  const res = await api.post<ApiResponse<TicketDetail>>(
    `/api/tickets/admin/${id}/replies`,
    { content },
    { skipBusinessError: true }
  )
  return res.data
}

export async function updateAdminTicketStatus(
  id: number,
  status: Extract<TicketStatus, 'pending' | 'closed'>
): Promise<ApiResponse<TicketDetail>> {
  const res = await api.patch<ApiResponse<TicketDetail>>(
    `/api/tickets/admin/${id}/status`,
    { status },
    { skipBusinessError: true }
  )
  return res.data
}
