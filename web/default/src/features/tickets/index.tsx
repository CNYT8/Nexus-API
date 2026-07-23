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
import { List, MessageSquareText, Send } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { SectionPageLayout } from '@/components/layout'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
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
import {
  createMyTicket,
  getMyTicket,
  getMyTickets,
  getTicketSettings,
  replyMyTicket,
} from './api'
import {
  getTicketTypeLabel,
  TicketMessages,
  TicketPagination,
  TicketStatusBadge,
} from './components'
import type { TicketType } from './types'

const PAGE_SIZE = 10

const ticketTypes: TicketType[] = ['finance', 'technical', 'other']

export function TicketCenter() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id ?? 0)
  const previousUserId = useRef(userId)
  const [type, setType] = useState<TicketType | ''>('')
  const [content, setContent] = useState('')
  const [showHistory, setShowHistory] = useState(false)
  const [page, setPage] = useState(1)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [reply, setReply] = useState('')

  const settingsQuery = useQuery({
    queryKey: ['ticket-settings'],
    queryFn: getTicketSettings,
    staleTime: 60 * 1000,
  })
  const maxContentLength = settingsQuery.data?.data?.max_content_length ?? 4000

  const ticketsQuery = useQuery({
    queryKey: ['my-tickets', userId, page],
    queryFn: () => getMyTickets({ page, pageSize: PAGE_SIZE }),
    enabled: showHistory && userId > 0,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[1] === userId ? previous : undefined,
  })
  const detailQuery = useQuery({
    queryKey: ['my-ticket', userId, selectedId],
    queryFn: () => getMyTicket(selectedId as number),
    enabled: userId > 0 && selectedId !== null,
  })

  const refreshTickets = async () => {
    await queryClient.invalidateQueries({ queryKey: ['my-tickets', userId] })
    if (selectedId !== null) {
      await queryClient.invalidateQueries({
        queryKey: ['my-ticket', userId, selectedId],
      })
    }
  }

  const createMutation = useMutation({
    mutationFn: () => createMyTicket(type as TicketType, content.trim()),
    onSuccess: async (result) => {
      if (!result.success) {
        toast.error(result.message || t('Failed to send ticket'))
        return
      }
      setType('')
      setContent('')
      setShowHistory(true)
      setPage(1)
      await refreshTickets()
      toast.success(t('Ticket submitted'))
    },
  })

  const replyMutation = useMutation({
    mutationFn: () => replyMyTicket(selectedId as number, reply.trim()),
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

  useEffect(() => {
    if (selectedId === null) setReply('')
  }, [selectedId])

  useEffect(() => {
    const oldUserId = previousUserId.current
    if (oldUserId === userId) return
    queryClient.removeQueries({ queryKey: ['my-tickets', oldUserId] })
    queryClient.removeQueries({ queryKey: ['my-ticket', oldUserId] })
    previousUserId.current = userId
    setSelectedId(null)
    setPage(1)
  }, [queryClient, userId])

  const tickets = ticketsQuery.data?.data?.items ?? []
  const ticketTotal = ticketsQuery.data?.data?.total ?? 0
  const selectedTicket = detailQuery.data?.data

  if (settingsQuery.isLoading && !settingsQuery.data) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Ticket Center')}</SectionPageLayout.Title>
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
        <SectionPageLayout.Title>{t('Ticket Center')}</SectionPageLayout.Title>
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
      <SectionPageLayout.Title>{t('Ticket Center')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          type='button'
          variant={showHistory ? 'secondary' : 'outline'}
          onClick={() => setShowHistory((visible) => !visible)}
        >
          <List data-icon='inline-start' />
          {t('My Tickets')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-4xl flex-col gap-6'>
          <Card>
            <CardHeader>
              <CardTitle>{t('Submit a ticket')}</CardTitle>
              <CardDescription>
                {t('Describe your issue and track its progress.')}
              </CardDescription>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='space-y-2'>
                <label className='text-sm font-medium'>{t('Ticket Type')}</label>
                <Select
                  value={type || null}
                  onValueChange={(value) => setType((value ?? '') as TicketType | '')}
                >
                  <SelectTrigger className='w-full'>
                    <SelectValue placeholder={t('Select a ticket type')} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    {ticketTypes.map((ticketType) => (
                      <SelectItem key={ticketType} value={ticketType}>
                        {getTicketTypeLabel(t, ticketType)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className='space-y-2'>
                <label className='text-sm font-medium'>{t('Issue Description')}</label>
                <Textarea
                  value={content}
                  maxLength={maxContentLength}
                  rows={7}
                  placeholder={t('Please describe the issue in detail')}
                  onChange={(event) => setContent(event.target.value)}
                />
              </div>
              <div className='flex justify-end'>
                <Button
                  type='button'
                  disabled={!type || !content.trim() || createMutation.isPending}
                  onClick={() => createMutation.mutate()}
                >
                  <Send data-icon='inline-start' />
                  {t('Send Ticket')}
                </Button>
              </div>
            </CardContent>
          </Card>

          {showHistory && (
            <section className='space-y-3'>
              <div className='flex items-center justify-between gap-3'>
                <h3 className='text-sm font-semibold'>{t('My Tickets')}</h3>
                <span className='text-muted-foreground text-xs'>
                  {t('{{count}} total', { count: ticketTotal })}
                </span>
              </div>
              {ticketsQuery.isLoading ? (
                <div className='text-muted-foreground py-10 text-center text-sm'>
                  {t('Loading...')}
                </div>
              ) : tickets.length === 0 ? (
                <EmptyState
                  icon={MessageSquareText}
                  title={t('No tickets yet')}
                  description={t('Create a ticket when you need help.')}
                  className='min-h-44 border'
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
                        <span className='flex items-center gap-2'>
                          <span className='font-medium'>#{ticket.id}</span>
                          <span className='text-muted-foreground text-xs'>
                            {getTicketTypeLabel(t, ticket.type)}
                          </span>
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
            </section>
          )}
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
              <TicketMessages messages={selectedTicket.messages} />
              {selectedTicket.status === 'closed' ? (
                <p className='text-muted-foreground text-sm'>
                  {t('This ticket is closed and cannot receive replies.')}
                </p>
              ) : (
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
                    aria-label={t('Send')}
                    disabled={!reply.trim() || replyMutation.isPending}
                    onClick={() => replyMutation.mutate()}
                  >
                    <Send />
                  </Button>
                </div>
              )}
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </SectionPageLayout>
  )
}
