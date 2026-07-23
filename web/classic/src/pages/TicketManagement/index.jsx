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

import React, { useEffect, useState } from 'react';
import {
  Button,
  Card,
  Empty,
  Modal,
  Pagination,
  Select,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { Check, MessageSquare, Send } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { API, isRoot, showError, showSuccess } from '../../helpers';

const { Text, Title } = Typography;

const STATUS_COLORS = { pending: 'orange', replied: 'green', closed: 'grey' };
const PAGE_SIZE = 20;

const TicketStatus = ({ status, t }) => (
  <Tag color={STATUS_COLORS[status] || 'grey'} shape='circle'>
    {t(
      { pending: '待处理', replied: '已回复', closed: '已关闭' }[status] ||
        '未知状态',
    )}
  </Tag>
);

const TicketType = ({ type, t }) =>
  t(
    {
      finance: '财务问题',
      technical: '技术问题',
      other: '其他问题',
    }[type] || '其他问题',
  );

const TicketManagement = () => {
  const { t } = useTranslation();
  const [status, setStatus] = useState('');
  const [tickets, setTickets] = useState([]);
  const [ticketTotal, setTicketTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [selectedTicket, setSelectedTicket] = useState(null);
  const [reply, setReply] = useState('');
  const [sending, setSending] = useState(false);
  const [maxContentLength, setMaxContentLength] = useState(4000);
  const [adminCanClose, setAdminCanClose] = useState(true);
  const [ticketEnabled, setTicketEnabled] = useState(true);
  const [settingsLoaded, setSettingsLoaded] = useState(false);

  useEffect(() => {
    const loadSettings = async () => {
      try {
        const res = await API.get('/api/tickets/settings');
        if (res.data.success && res.data.data) {
          setMaxContentLength(res.data.data.max_content_length || 4000);
          setAdminCanClose(res.data.data.admin_can_close === true);
          setTicketEnabled(res.data.data.enabled !== false);
        }
      } catch {
        // The backend remains authoritative if settings cannot be read.
      } finally {
        setSettingsLoaded(true);
      }
    };
    loadSettings();
  }, []);

  const loadTickets = async (nextPage = page, nextStatus = status) => {
    setLoading(true);
    try {
      const query = new URLSearchParams({
        p: String(nextPage),
        page_size: String(PAGE_SIZE),
      });
      if (nextStatus) query.set('status', nextStatus);
      const res = await API.get(
        `/api/tickets/admin/?${query.toString()}`,
      );
      if (res.data.success) {
        setTickets(res.data.data?.items || []);
        setTicketTotal(res.data.data?.total || 0);
      }
      else showError(res.data.message || t('加载失败，请重试'));
    } catch (error) {
      showError(error.message || t('加载失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!settingsLoaded || !ticketEnabled) return;
    setPage(1);
    loadTickets(1, status);
  }, [settingsLoaded, status, ticketEnabled]);

  const openTicket = async (ticket) => {
    try {
      const res = await API.get(`/api/tickets/admin/${ticket.id}`);
      if (res.data.success) {
        setSelectedTicket(res.data.data);
        setReply('');
      } else showError(res.data.message || t('加载失败，请重试'));
    } catch (error) {
      showError(error.message || t('加载失败，请重试'));
    }
  };

  const sendReply = async () => {
    if (!selectedTicket || !reply.trim()) return;
    setSending(true);
    try {
      const res = await API.post(
        `/api/tickets/admin/${selectedTicket.id}/replies`,
        { content: reply.trim() },
      );
      if (!res.data.success) {
        showError(res.data.message || t('发送失败，请重试'));
        return;
      }
      setSelectedTicket(res.data.data);
      setReply('');
      await loadTickets(page);
      showSuccess(t('回复已发送'));
    } catch (error) {
      showError(error.message || t('发送失败，请重试'));
    } finally {
      setSending(false);
    }
  };

  const updateStatus = async (nextStatus) => {
    if (!selectedTicket) return;
    try {
      const res = await API.patch(
        `/api/tickets/admin/${selectedTicket.id}/status`,
        { status: nextStatus },
      );
      if (!res.data.success) {
        showError(res.data.message || t('操作失败，请重试'));
        return;
      }
      setSelectedTicket(res.data.data);
      await loadTickets(page);
      showSuccess(t('工单状态已更新'));
    } catch (error) {
      showError(error.message || t('操作失败，请重试'));
    }
  };

  if (!ticketEnabled) {
    return (
      <div className='mt-[60px] px-2'>
        <Card className='w-full !rounded-lg' bodyStyle={{ padding: 24 }}>
          <Title heading={4}>{t('工单管理')}</Title>
          <Text type='tertiary'>{t('工单中心未开启')}</Text>
        </Card>
      </div>
    );
  }

  return (
    <div className='mt-[60px] px-2'>
      <Card className='w-full !rounded-lg' bodyStyle={{ padding: 24 }}>
        <div className='mb-5 flex flex-wrap items-center justify-between gap-3'>
          <div>
            <Title heading={4} className='!mb-1'>
              {t('工单管理')}
            </Title>
            <Text type='tertiary'>{t('处理所有用户提交的工单')}</Text>
          </div>
          <Select
            value={status || undefined}
            optionList={[
              { value: '', label: t('全部工单') },
              { value: 'pending', label: t('待处理') },
              { value: 'replied', label: t('已处理') },
              { value: 'closed', label: t('已关闭') },
            ]}
            placeholder={t('筛选工单状态')}
            onChange={(value) => setStatus(value || '')}
            showClear
            style={{ minWidth: 150 }}
          />
        </div>

        {loading && tickets.length === 0 ? null : tickets.length === 0 ? (
          <Empty description={t('暂无工单')} />
        ) : (
          <div className='space-y-2'>
            {tickets.map((ticket) => (
              <div
                key={ticket.id}
                role='button'
                tabIndex={0}
                onClick={() => openTicket(ticket)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') openTicket(ticket);
                }}
                className='flex cursor-pointer items-center justify-between gap-4 rounded-lg border border-semi-color-border px-4 py-3 transition-colors hover:bg-semi-color-fill-0'
              >
                <div className='min-w-0'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <Text strong>#{ticket.id}</Text>
                    <Text>{ticket.username || t('未知用户')}</Text>
                    <Text type='tertiary' size='small'>
                      <TicketType type={ticket.type} t={t} />
                    </Text>
                    {ticket.last_author === 'user' &&
                      ticket.has_admin_reply &&
                      ticket.status !== 'closed' && (
                        <Tag color='blue' shape='circle'>
                          {t('客户回复')}
                        </Tag>
                      )}
                  </div>
                  <Text type='tertiary' size='small'>
                    {new Date(ticket.updated_at).toLocaleString()}
                  </Text>
                </div>
                <TicketStatus status={ticket.status} t={t} />
              </div>
            ))}
          </div>
        )}
        {ticketTotal > PAGE_SIZE && (
          <div className='mt-4 flex justify-center'>
            <Pagination
              currentPage={page}
              pageSize={PAGE_SIZE}
              total={ticketTotal}
              onPageChange={(nextPage) => {
                setPage(nextPage);
                loadTickets(nextPage);
              }}
            />
          </div>
        )}
      </Card>

      <Modal
        title={
          <div className='flex items-center gap-2'>
            <MessageSquare size={18} />
            <span>{t('工单详情')}</span>
            <TicketStatus status={selectedTicket?.status} t={t} />
          </div>
        }
        visible={Boolean(selectedTicket)}
        onCancel={() => setSelectedTicket(null)}
        footer={null}
        width={760}
      >
        {selectedTicket && (
          <div className='flex flex-col gap-4'>
            <div className='flex items-center gap-3 text-sm'>
              <Text type='tertiary'>{t('用户')}</Text>
              <Text>{selectedTicket.username || t('未知用户')}</Text>
              <Text type='tertiary'>{t('工单类型')}</Text>
              <Text>
                <TicketType type={selectedTicket.type} t={t} />
              </Text>
            </div>
            <div className='max-h-[50vh] space-y-3 overflow-y-auto rounded-lg border border-semi-color-border p-3'>
              {(selectedTicket.messages || []).map((message) => (
                <div key={message.id} className='flex justify-start'>
                  <div
                    className='max-w-[84%] rounded-lg px-3 py-2 text-sm text-semi-color-text-0'
                    style={{
                      background:
                        message.author_role === 'admin'
                          ? 'var(--semi-color-primary-light-default)'
                          : 'var(--semi-color-fill-0)',
                    }}
                  >
                    <div className='mb-1 text-xs text-semi-color-text-2'>
                      {message.author_role === 'admin'
                        ? t('管理员')
                        : selectedTicket.username || t('客户')}
                    </div>
                    <div className='whitespace-pre-wrap break-words'>
                      {message.content}
                    </div>
                    <div className='mt-1 text-right text-[11px] text-semi-color-text-3'>
                      {new Date(message.created_at).toLocaleString()}
                    </div>
                  </div>
                </div>
              ))}
            </div>
            {selectedTicket.status !== 'closed' && (
              <div className='flex items-end gap-2'>
                <TextArea
                  value={reply}
                  onChange={setReply}
                  autosize={{ minRows: 2, maxRows: 6 }}
                  placeholder={t('请输入回复内容')}
                  maxLength={maxContentLength}
                  showClear
                  className='flex-1'
                />
                <Button
                  theme='solid'
                  type='primary'
                  icon={<Send size={15} />}
                  loading={sending}
                  disabled={!reply.trim()}
                  onClick={sendReply}
                >
                  {t('回复用户')}
                </Button>
              </div>
            )}
            {(isRoot() || adminCanClose) && (
              <div className='flex justify-end gap-2 border-t border-semi-color-border pt-3 pb-2'>
                {selectedTicket.status !== 'closed' ? (
                  <Button
                    type='tertiary'
                    icon={<Check size={15} />}
                    onClick={() => updateStatus('closed')}
                  >
                    {t('关闭工单')}
                  </Button>
                ) : (
                  <Button
                    type='tertiary'
                    onClick={() => updateStatus('pending')}
                  >
                    {t('重新打开')}
                  </Button>
                )}
              </div>
            )}
          </div>
        )}
      </Modal>
    </div>
  );
};

export default TicketManagement;
