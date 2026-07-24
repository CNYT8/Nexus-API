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

export type TicketType = 'finance' | 'technical' | 'account' | 'other'
export type TicketPriority = 'low' | 'medium' | 'high'
export type TicketStatus = 'pending' | 'replied' | 'closed'
export type TicketAuthorRole = 'user' | 'admin'

export type TicketSettings = {
  enabled: boolean
  admin_manage_enabled: boolean
  admin_can_close: boolean
  max_content_length: number
}

export type TicketMessage = {
  id: number
  author_role: TicketAuthorRole
  content: string
  created_at: string
}

export type TicketSummary = {
  id: number
  user_id: number
  username?: string
  type: TicketType
  priority: TicketPriority
  status: TicketStatus
  last_author: TicketAuthorRole
  has_admin_reply: boolean
  created_at: string
  updated_at: string
  closed_at?: string
}

export type TicketDetail = TicketSummary & {
  messages: TicketMessage[]
}

export type TicketList = {
  page: number
  page_size: number
  total: number
  items: TicketSummary[]
}

export type ApiResponse<T> = {
  success: boolean
  message?: string
  data?: T
}
