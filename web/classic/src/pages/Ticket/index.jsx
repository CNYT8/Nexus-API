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

import React, { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Card,
  Empty,
  Form,
  Modal,
  Pagination,
  Select,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { List, MessageSquare, Send } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../helpers';

const { Text, Title } = Typography;

const TICKET_TYPES = [
  { value: 'finance', label: '财务问题' },
  { value: 'technical', label: '技术问题' },
  { value: 'other', label: '其他问题' },
];

const STATUS_COLORS = {
  pending: 'orange',
  replied: 'green',
  closed: 'grey',
};

const PAGE_SIZE = 10;

const TicketStatus = ({ status, t }) => (
  <Tag color={STATUS_COLORS[status] || 'grey'} shape='circle'>
    {t(
      {
        pending: '待处理',
        replied: '已回复',
        closed: '已关闭',
      }[status] || '未知状态',
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

const TicketDetailModal = ({
  ticket,
  visible,
  onClose,
  onRefresh,
  maxContentLength,
  t,
}) => {
  const [reply, setReply] = useState('');
  const [sending, setSending] = useState(false);

  useEffect(() => {
    if (!visible) setReply('');
  }, [visible]);

  const sendReply = async () => {
    if (!reply.trim()) return;
    setSending(true);
    try {
      const res = await API.post(`/api/tickets/${ticket.id}/replies`, {
        content: reply.trim(),
      });
      if (!res.data.success) {
        showError(res.data.message || t('发送失败，请重试'));
        return;
      }
      setReply('');
      showSuccess(t('回复已发送'));
      await onRefresh();
    } catch (error) {
      showError(error.message || t('发送失败，请重试'));
    } finally {
      setSending(false);
    }
  };

  return (
    <Modal
      title={
        <div className='flex items-center gap-2'>
          <MessageSquare size={18} />
          <span>{t('工单详情')}</span>
          <TicketStatus status={ticket?.status} t={t} />
        </div>
      }
      visible={visible}
      onCancel={onClose}
      footer={null}
      width={720}
    >
      {ticket && (
        <div className='flex flex-col gap-4'>
          <div className='flex items-center gap-2 text-sm'>
            <Text type='tertiary'>{t('工单类型')}</Text>
            <Text>{<TicketType type={ticket.type} t={t} />}</Text>
          </div>
          <div className='max-h-[45vh] space-y-3 overflow-y-auto rounded-lg border border-semi-color-border p-3'>
            {(ticket.messages || []).map((message) => (
              <div
                key={message.id}
                className={`flex ${message.author_role === 'user' ? 'justify-end' : 'justify-start'}`}
              >
                <div
                  className='max-w-[84%] rounded-lg px-3 py-2 text-sm text-semi-color-text-0'
                  style={{
                    background:
                      message.author_role === 'user'
                        ? 'var(--semi-color-primary-light-default)'
                        : 'var(--semi-color-fill-0)',
                  }}
                >
                  <div className='mb-1 text-xs text-semi-color-text-2'>
                    {message.author_role === 'user'
                      ? t('我')
                      : t('管理员')}
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
          {ticket.status === 'closed' ? (
            <Text type='tertiary'>{t('该工单已关闭，无法继续回复')}</Text>
          ) : (
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
                {t('发送')}
              </Button>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
};

const TicketCenter = () => {
  const { t } = useTranslation();
  const [type, setType] = useState('');
  const [content, setContent] = useState('');
  const [tickets, setTickets] = useState([]);
  const [ticketTotal, setTicketTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [showMyTickets, setShowMyTickets] = useState(false);
  const [selectedTicket, setSelectedTicket] = useState(null);
  const [maxContentLength, setMaxContentLength] = useState(4000);
  const [ticketEnabled, setTicketEnabled] = useState(true);
  const [settingsLoaded, setSettingsLoaded] = useState(false);

  useEffect(() => {
    const loadSettings = async () => {
      try {
        const res = await API.get('/api/tickets/settings');
        if (res.data.success && res.data.data) {
          setTicketEnabled(res.data.data.enabled !== false);
          if (res.data.data.max_content_length) {
            setMaxContentLength(res.data.data.max_content_length);
          }
        }
      } catch {
        // Keep the server default as a conservative client-side fallback.
      } finally {
        setSettingsLoaded(true);
      }
    };
    loadSettings();
  }, []);

  const typeOptions = useMemo(
    () => TICKET_TYPES.map((item) => ({ ...item, label: t(item.label) })),
    [t],
  );

  const loadTickets = async (nextPage = page) => {
    setLoading(true);
    try {
      const res = await API.get(
        `/api/tickets/self?p=${nextPage}&page_size=${PAGE_SIZE}`,
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
    if (showMyTickets) loadTickets(page);
  }, [showMyTickets, page]);

  const submitTicket = async () => {
    if (!type || !content.trim()) return;
    setSending(true);
    try {
      const res = await API.post('/api/tickets/', {
        type,
        content: content.trim(),
      });
      if (!res.data.success) {
        showError(res.data.message || t('发送失败，请重试'));
        return;
      }
      setContent('');
      setType('');
      showSuccess(t('工单已提交'));
      setShowMyTickets(true);
      setPage(1);
      if (showMyTickets && page === 1) await loadTickets(1);
    } catch (error) {
      showError(error.message || t('发送失败，请重试'));
    } finally {
      setSending(false);
    }
  };

  const openTicket = async (ticket) => {
    try {
      const res = await API.get(`/api/tickets/${ticket.id}`);
      if (res.data.success) setSelectedTicket(res.data.data);
      else showError(res.data.message || t('加载失败，请重试'));
    } catch (error) {
      showError(error.message || t('加载失败，请重试'));
    }
  };

  const refreshSelectedTicket = async () => {
    await loadTickets(page);
    if (selectedTicket) {
      const res = await API.get(`/api/tickets/${selectedTicket.id}`);
      if (res.data.success) setSelectedTicket(res.data.data);
    }
  };

  if (settingsLoaded && !ticketEnabled) {
    return (
      <div className='mt-[60px] px-2'>
        <Card className='w-full !rounded-lg' bodyStyle={{ padding: 24 }}>
          <Title heading={4}>{t('工单中心')}</Title>
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
              {t('工单中心')}
            </Title>
            <Text type='tertiary'>{t('提交问题并查看处理进度')}</Text>
          </div>
          <Button
            theme={showMyTickets ? 'solid' : 'light'}
            type='tertiary'
            icon={<List size={16} />}
            onClick={() => setShowMyTickets((value) => !value)}
          >
            {t('我的工单')}
          </Button>
        </div>

        <Form layout='vertical'>
          <Form.Select
            field='ticket_type'
            label={t('工单类型')}
            value={type || undefined}
            optionList={typeOptions}
            placeholder={t('请选择工单类型')}
            onChange={setType}
            showClear
          />
          <Form.TextArea
            field='ticket_content'
            label={t('问题描述')}
            value={content}
            onChange={setContent}
            autosize={{ minRows: 6, maxRows: 14 }}
            placeholder={t('请详细描述你遇到的问题')}
            maxLength={maxContentLength}
            showClear
          />
          <div className='flex justify-end'>
            <Button
              theme='solid'
              type='primary'
              icon={<Send size={15} />}
              loading={sending}
              disabled={!type || !content.trim()}
              onClick={submitTicket}
            >
              {t('发送工单')}
            </Button>
          </div>
        </Form>

        {showMyTickets && (
          <div className='mt-6 border-t border-semi-color-border pt-5'>
            <div className='mb-3 flex items-center justify-between'>
              <Text strong>{t('我的工单')}</Text>
              <Text type='tertiary' size='small'>
                {t('共 {{count}} 条', { count: ticketTotal })}
              </Text>
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
                    className='flex cursor-pointer items-center justify-between gap-3 rounded-lg border border-semi-color-border px-3 py-3 transition-colors hover:bg-semi-color-fill-0'
                  >
                    <div className='min-w-0'>
                      <div className='flex items-center gap-2'>
                        <Text strong ellipsis={{ showTooltip: true }}>
                          #{ticket.id}
                        </Text>
                        <Text type='tertiary' size='small'>
                          <TicketType type={ticket.type} t={t} />
                        </Text>
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
                  onPageChange={setPage}
                />
              </div>
            )}
          </div>
        )}
      </Card>

      <TicketDetailModal
        ticket={selectedTicket}
        visible={Boolean(selectedTicket)}
        onClose={() => setSelectedTicket(null)}
        onRefresh={refreshSelectedTicket}
        maxContentLength={maxContentLength}
        t={t}
      />
    </div>
  );
};

export default TicketCenter;
