/*
Copyright (C) 2025 QuantumNous

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
import React, { useCallback, useEffect, useState } from 'react';
import {
  Avatar,
  Button,
  Card,
  Empty,
  Pagination,
  Spin,
  Table,
  Typography,
} from '@douyinfe/semi-ui';
import { Coins } from 'lucide-react';
import { API } from '../../helpers';

const PAGE_SIZE = 20;
const money = (value) => {
  const amount = Number(value || 0) / 1000000;
  return Number.isFinite(amount) ? amount.toFixed(2) : '-';
};

const RebateCard = ({ t, renderQuota }) => {
  const [overview, setOverview] = useState(null);
  const [records, setRecords] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [recordsLoading, setRecordsLoading] = useState(false);
  const [error, setError] = useState(false);
  const [recordsError, setRecordsError] = useState(false);

  const loadOverview = useCallback(async () => {
    setLoading(true);
    setError(false);
    try {
      const res = await API.get('/api/user/rebate');
      if (
        !(res.data?.success === true || res.data?.message === 'success') ||
        !res.data?.data
      )
        throw new Error('request failed');
      setOverview(res.data.data);
    } catch (e) {
      setError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadRecords = useCallback(async (nextPage) => {
    setRecordsLoading(true);
    setRecordsError(false);
    try {
      const res = await API.get(
        `/api/user/rebate/records?page=${nextPage}&page_size=${PAGE_SIZE}`,
      );
      if (
        !(res.data?.success === true || res.data?.message === 'success') ||
        !res.data?.data
      )
        throw new Error('request failed');
      setRecords(res.data.data.items || []);
      setTotal(Number(res.data.data.total) || 0);
      setPage(Number(res.data.data.page) || nextPage);
    } catch (e) {
      setRecordsError(true);
    } finally {
      setRecordsLoading(false);
    }
  }, []);

  useEffect(() => {
    loadOverview();
  }, [loadOverview]);
  useEffect(() => {
    if (overview?.enabled) loadRecords(page);
  }, [overview?.enabled, loadRecords, page]);

  if (loading)
    return (
      <Card className='!rounded-2xl shadow-sm border-0'>
        <Spin />
      </Card>
    );
  if (error)
    return (
      <Card className='!rounded-2xl shadow-sm border-0'>
        <div className='flex items-center justify-between gap-3 rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-700'>
          <span>{t('无法加载充值返利')}</span>
          <Button size='small' onClick={loadOverview} className='!rounded-lg'>
            {t('重试')}
          </Button>
        </div>
      </Card>
    );
  if (!overview?.enabled) return null;

  const statusKeys = new Set([
    'pending',
    'held',
    'paid',
    'discarded',
    'voided',
    'failed',
  ]);
  const statusLabels = {
    pending: t('待发放'),
    held: t('已冻结'),
    paid: t('已发放'),
    discarded: t('已丢弃'),
    voided: t('已作废'),
    failed: t('发放失败'),
  };
  const payoutLabels = {
    immediate: t('立即发放'),
    scheduled: t('定时发放'),
    half_day: t('每日两次'),
    custom: t('自定义间隔'),
  };
  const displayDate = (value) => {
    if (value == null) return '-';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
  };
  const columns = [
    { title: t('时间'), dataIndex: 'created_at', render: displayDate },
    {
      title: t('邀请用户'),
      dataIndex: 'invitee_username',
      render: (value) => value || '-',
    },
    {
      title: t('合格消费基数'),
      dataIndex: 'eligible_money_micros',
      render: money,
    },
    { title: t('返利奖励'), dataIndex: 'reward_money_micros', render: money },
    { title: t('可用时间'), dataIndex: 'available_at', render: displayDate },
    {
      title: t('状态'),
      dataIndex: 'status',
      render: (value) => (statusKeys.has(value) ? statusLabels[value] : value),
    },
  ];
  const cap = (value) =>
    Number(value || 0) === 0 ? t('无限制') : money(value);

  return (
    <Card className='!rounded-2xl shadow-sm border-0'>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='orange' className='mr-3 shadow-md'>
          <Coins size={16} />
        </Avatar>
        <div>
          <Typography.Text className='text-lg font-medium'>
            {t('充值返利')}
          </Typography.Text>
          <div className='text-xs'>
            {t('仅直属邀请关系产生返利，返利奖励本身不再参与返利。')}
          </div>
        </div>
      </div>
      <div className='grid grid-cols-1 sm:grid-cols-3 gap-3 mb-4'>
        <Card className='!rounded-xl'>
          <div className='text-xs text-gray-500'>{t('返利比例')}</div>
          <div className='text-lg font-semibold'>
            {(Number(overview.rate_basis_points || 0) / 100).toFixed(2)}%
          </div>
        </Card>
        <Card className='!rounded-xl'>
          <div className='text-xs text-gray-500'>{t('待发返利')}</div>
          <div className='text-lg font-semibold'>
            {renderQuota(overview.pending_quota || 0)}
          </div>
        </Card>
        <Card className='!rounded-xl'>
          <div className='text-xs text-gray-500'>{t('已发返利')}</div>
          <div className='text-lg font-semibold'>
            {renderQuota(overview.paid_quota || 0)}
          </div>
        </Card>
      </div>
      <div className='grid grid-cols-1 sm:grid-cols-2 gap-2 text-xs text-gray-500 mb-4'>
        <div>
          {t('被邀请人终身合格消费基数上限')}:{' '}
          {cap(overview.invitee_eligible_money_limit_micros)}
        </div>
        <div>
          {t('直属下级合格消费基数上限')}:{' '}
          {overview.inviter_eligible_money_limit_micros == null
            ? '-'
            : cap(overview.inviter_eligible_money_limit_micros)}
        </div>
        <div>
          {t('当前周期合格消费基数')}:{' '}
          {money(overview.inviter_period_eligible_money_micros)}
        </div>
        <div>
          {t('发放安排')}:{' '}
          {payoutLabels[overview.payout_mode] || overview.payout_mode}
          {overview.payout_mode === 'custom'
            ? ` (${overview.payout_interval_hours || 0}h，${overview.site_timezone || 'UTC'})`
            : overview.payout_mode !== 'immediate'
              ? ` (${overview.site_timezone || 'UTC'})`
              : ''}
        </div>
      </div>
      {recordsError ? (
        <div className='flex items-center justify-between gap-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-700'>
          <span>{t('无法加载返利明细')}</span>
          <Button size='small' onClick={() => loadRecords(page)}>
            {t('重试')}
          </Button>
        </div>
      ) : recordsLoading ? (
        <Spin />
      ) : records.length === 0 ? (
        <Empty title={t('暂无充值返利记录')} />
      ) : (
        <>
          <div className='overflow-x-auto'>
            <Table
              columns={columns}
              dataSource={records}
              rowKey='id'
              pagination={false}
              size='small'
            />
          </div>
          {total > PAGE_SIZE && (
            <div className='flex justify-center mt-3'>
              <Pagination
                currentPage={page}
                pageSize={PAGE_SIZE}
                total={total}
                onPageChange={setPage}
              />
            </div>
          )}
        </>
      )}
    </Card>
  );
};

export default RebateCard;
