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

import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, MessageSquareText, Send } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { SectionPageLayout } from '@/components/layout'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import {
  getAdminTicket,
  getAdminTickets,
  getTicketSettings,
  replyAdminTicket,
  updateAdminTicketStatus,
} from './api'
import {
  getTicketTypeLabel,
  TicketMessages,
  TicketPagination,
  TicketStatusBadge,
} from './components'
import type { TicketStatus } from './types'

const PAGE_SIZE = 20

const statusOptions: { value: TicketStatus | 'all'; label: string }[] = [
  { value: 'all', label: 'All Tickets' },
  { value: 'pending', label: 'Ticket Pending' },
  { value: 'replied', label: 'Ticket Processed' },
  { value: 'closed', label: 'Ticket Closed' },
]

export function TicketManagement() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const adminId = useAuthStore((state) => state.auth.user?.id ?? 0)
  const previousAdminId = useRef(adminId)
  const role = useAuthStore((state) => state.auth.user?.role)
  const [status, setStatus] = useState<TicketStatus | 'all'>('all')
  const [page, setPage] = useState(1)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [reply, setReply] = useState('')

  const settingsQuery = useQuery({
    queryKey: ['ticket-settings'],
    queryFn: getTicketSettings,
    staleTime: 60 * 1000,
  })
  const maxContentLength = settingsQuery.data?.data?.max_content_length ?? 4000
  const canClose =
    role === ROLE.SUPER_ADMIN || settingsQuery.data?.data?.admin_can_close === true

  const ticketsQuery = useQuery({
    queryKey: ['admin-tickets', adminId, status, page],
    queryFn: () =>
      getAdminTickets({
        page,
        pageSize: PAGE_SIZE,
        ...(status === 'all' ? {} : { status }),
      }),
    enabled:
      adminId > 0 &&
      !settingsQuery.isLoading &&
      settingsQuery.data?.data?.enabled !== false,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[1] === adminId ? previous : undefined,
  })
  const detailQuery = useQuery({
    queryKey: ['admin-ticket', adminId, selectedId],
    queryFn: () => getAdminTicket(selectedId as number),
    enabled: adminId > 0 && selectedId !== null,
  })

  const refreshTickets = async () => {
    await queryClient.invalidateQueries({ queryKey: ['admin-tickets', adminId] })
    if (selectedId !== null) {
      await queryClient.invalidateQueries({
        queryKey: ['admin-ticket', adminId, selectedId],
      })
    }
  }

  const replyMutation = useMutation({
    mutationFn: () => replyAdminTicket(selectedId as number, reply.trim()),
    onSuccess: async (result) => {
      if (!result.success) {
        toast.error(result.message || t('Failed to send reply'))
        return
      }
      setReply('')
      await refreshTickets()
      toast.success(t('Reply sent'))
    },
  })

  const statusMutation = useMutation({
    mutationFn: (nextStatus: Extract<TicketStatus, 'pending' | 'closed'>) =>
      updateAdminTicketStatus(selectedId as number, nextStatus),
    onSuccess: async (result) => {
      if (!result.success) {
        toast.error(result.message || t('Operation failed, please retry'))
        return
      }
      await refreshTickets()
      toast.success(t('Ticket status updated'))
    },
  })

  useEffect(() => {
    if (selectedId === null) setReply('')
  }, [selectedId])

  useEffect(() => {
    const oldAdminId = previousAdminId.current
    if (oldAdminId === adminId) return
    queryClient.removeQueries({ queryKey: ['admin-tickets', oldAdminId] })
    queryClient.removeQueries({ queryKey: ['admin-ticket', oldAdminId] })
    previousAdminId.current = adminId
    setSelectedId(null)
    setPage(1)
  }, [adminId, queryClient])

  const tickets = ticketsQuery.data?.data?.items ?? []
  const ticketTotal = ticketsQuery.data?.data?.total ?? 0
  const selectedTicket = detailQuery.data?.data

  if (settingsQuery.isLoading && !settingsQuery.data) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Ticket Management')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <p className='text-muted-foreground py-12 text-center text-sm'>
            {t('Loading...')}
          </p>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  if (settingsQuery.data?.success && settingsQuery.data.data?.enabled === false) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Ticket Management')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <p className='text-muted-foreground py-12 text-center text-sm'>
            {t('Ticket Center is disabled')}
          </p>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Ticket Management')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Select
          value={status}
          onValueChange={(value) => {
            setStatus((value ?? 'all') as TicketStatus | 'all')
            setPage(1)
          }}
        >
          <SelectTrigger className='w-36'>
            <SelectValue placeholder={t('Filter ticket status')} />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            {statusOptions.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {t(option.label)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-5xl space-y-3'>
          <p className='text-muted-foreground text-sm'>
            {t('Review and respond to all user tickets.')}
          </p>
          {ticketsQuery.isLoading ? (
            <div className='text-muted-foreground py-12 text-center text-sm'>
              {t('Loading...')}
            </div>
          ) : tickets.length === 0 ? (
            <EmptyState
              icon={MessageSquareText}
              title={t('No tickets yet')}
              description={t('There are no tickets in this filter.')}
              className='min-h-52 border'
            />
          ) : (
            <div className='divide-y rounded-lg border'>
              {tickets.map((ticket) => (
                <button
                  key={ticket.id}
                  type='button'
                  className='hover:bg-muted/50 flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors'
                  onClick={() => setSelectedId(ticket.id)}
                >
                  <span className='min-w-0 space-y-1'>
                    <span className='flex flex-wrap items-center gap-2'>
                      <span className='font-medium'>#{ticket.id}</span>
                      <span>{ticket.username || t('Unknown User')}</span>
                      <span className='text-muted-foreground text-xs'>
                        {getTicketTypeLabel(t, ticket.type)}
                      </span>
                      {ticket.last_author === 'user' &&
                        ticket.has_admin_reply &&
                        ticket.status !== 'closed' && (
                          <span className='border-primary/30 bg-primary/10 text-primary rounded-full border px-2 py-0.5 text-xs'>
                            {t('Customer Replied')}
                          </span>
                        )}
                    </span>
                    <span className='text-muted-foreground block text-xs'>
                      {new Date(ticket.updated_at).toLocaleString()}
                    </span>
                  </span>
                  <TicketStatusBadge status={ticket.status} />
                </button>
              ))}
            </div>
          )}
          <TicketPagination
            page={page}
            pageSize={PAGE_SIZE}
            total={ticketTotal}
            onPageChange={setPage}
          />
        </div>
      </SectionPageLayout.Content>

      <Dialog
        open={selectedId !== null}
        onOpenChange={(open) => !open && setSelectedId(null)}
      >
        <DialogContent className='flex max-h-[85vh] max-w-2xl flex-col'>
          <DialogHeader>
            <DialogTitle className='flex items-center gap-2'>
              {t('Ticket Details')}
              {selectedTicket && <TicketStatusBadge status={selectedTicket.status} />}
            </DialogTitle>
            {selectedTicket && (
              <DialogDescription>
                {t('User')}: {selectedTicket.username || t('Unknown User')} ·{' '}
                {getTicketTypeLabel(t, selectedTicket.type)}
              </DialogDescription>
            )}
          </DialogHeader>
          {detailQuery.isLoading ? (
            <div className='text-muted-foreground py-10 text-center text-sm'>
              {t('Loading...')}
            </div>
          ) : selectedTicket ? (
            <div className='space-y-4 overflow-y-auto'>
              <TicketMessages messages={selectedTicket.messages} adminView />
              {selectedTicket.status !== 'closed' && (
                <div className='flex items-end gap-2'>
                  <Textarea
                    className='min-h-20 flex-1'
                    value={reply}
                    maxLength={maxContentLength}
                    placeholder={t('Enter a reply')}
                    onChange={(event) => setReply(event.target.value)}
                  />
                  <Button
                    type='button'
                    size='icon'
                    aria-label={t('Reply to user')}
                    disabled={!reply.trim() || replyMutation.isPending}
                    onClick={() => replyMutation.mutate()}
                  >
                    <Send />
                  </Button>
                </div>
              )}
              {canClose && (
                <div className='flex justify-end border-t pt-3'>
                  {selectedTicket.status === 'closed' ? (
                    <Button
                      type='button'
                      variant='outline'
                      disabled={statusMutation.isPending}
                      onClick={() => statusMutation.mutate('pending')}
                    >
                      {t('Reopen Ticket')}
                    </Button>
                  ) : (
                    <Button
                      type='button'
                      variant='outline'
                      disabled={statusMutation.isPending}
                      onClick={() => statusMutation.mutate('closed')}
                    >
                      <Check data-icon='inline-start' />
                      {t('Close Ticket')}
                    </Button>
                  )}
                </div>
              )}
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </SectionPageLayout>
  )
}
