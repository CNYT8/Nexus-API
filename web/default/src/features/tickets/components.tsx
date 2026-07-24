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

import { ChevronLeft, ChevronRight } from 'lucide-react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import type {
  TicketMessage,
  TicketPriority,
  TicketStatus,
  TicketType,
} from './types'

const statusClassName: Record<TicketStatus, string> = {
  pending: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  replied: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  closed: 'border-muted-foreground/20 bg-muted text-muted-foreground',
}

const statusTextKey: Record<TicketStatus, string> = {
  pending: 'Ticket Pending',
  replied: 'Ticket Replied',
  closed: 'Ticket Closed',
}

const typeTextKey: Record<TicketType, string> = {
  finance: 'Financial Issue',
  technical: 'Technical Issue',
  account: 'Account Issue',
  other: 'Other Issue',
}

const priorityClassName: Record<TicketPriority, string> = {
  low: 'border-slate-400/30 bg-slate-400/10 text-slate-600 dark:text-slate-300',
  medium:
    'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  high: 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300',
}

const priorityTextKey: Record<TicketPriority, string> = {
  low: 'Low Priority',
  medium: 'Medium Priority',
  high: 'High Priority',
}

export function TicketStatusBadge(props: { status: TicketStatus }) {
  const { t } = useTranslation()
  return (
    <Badge
      variant='outline'
      className={cn('shrink-0', statusClassName[props.status])}
    >
      {t(statusTextKey[props.status])}
    </Badge>
  )
}

export function getTicketTypeLabel(t: TFunction, type: TicketType) {
  return t(typeTextKey[type])
}

export function getTicketPriorityLabel(
  t: TFunction,
  priority: TicketPriority = 'medium'
) {
  return t(priorityTextKey[priority])
}

export function TicketPriorityBadge(props: { priority?: TicketPriority }) {
  const { t } = useTranslation()
  const priority = props.priority || 'medium'
  return (
    <Badge
      variant='outline'
      className={cn('shrink-0', priorityClassName[priority])}
    >
      {getTicketPriorityLabel(t, priority)}
    </Badge>
  )
}

export function TicketMessages(props: {
  messages: TicketMessage[]
  adminView?: boolean
  customerName?: string
}) {
  const { t } = useTranslation()
  return (
    <div className='border-border max-h-[50vh] space-y-3 overflow-y-auto rounded-lg border p-3'>
      {props.messages.map((message) => {
        const isAdmin = message.author_role === 'admin'
        return (
          <div
            key={message.id}
            className={cn('flex', isAdmin ? 'justify-start' : 'justify-end')}
          >
            <div
              className={cn(
                'max-w-[85%] rounded-lg px-3 py-2 text-sm',
                isAdmin ? 'bg-muted' : 'bg-primary/10'
              )}
            >
              <div className='text-muted-foreground mb-1 text-xs'>
                {isAdmin
                  ? t('Administrator')
                  : props.adminView
                    ? props.customerName || t('Customer')
                    : t('Me')}
              </div>
              <div className='whitespace-pre-wrap break-words'>
                {message.content}
              </div>
              <div className='text-muted-foreground mt-1 text-right text-[11px]'>
                {new Date(message.created_at).toLocaleString()}
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function TicketPagination(props: {
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
}) {
  const { t } = useTranslation()
  const pageCount = Math.max(1, Math.ceil(props.total / props.pageSize))
  if (pageCount <= 1) return null

  return (
    <div className='flex items-center justify-center gap-2 pt-4'>
      <Button
        type='button'
        variant='outline'
        size='icon-sm'
        aria-label={t('Previous page')}
        disabled={props.page <= 1}
        onClick={() => props.onPageChange(props.page - 1)}
      >
        <ChevronLeft />
      </Button>
      <span className='text-muted-foreground min-w-16 text-center text-xs tabular-nums'>
        {props.page} / {pageCount}
      </span>
      <Button
        type='button'
        variant='outline'
        size='icon-sm'
        aria-label={t('Next page')}
        disabled={props.page >= pageCount}
        onClick={() => props.onPageChange(props.page + 1)}
      >
        <ChevronRight />
      </Button>
    </div>
  )
}
