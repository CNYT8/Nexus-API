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

import React, { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Avatar,
  Card,
  Empty,
  Progress,
  Skeleton,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { Crown } from 'lucide-react';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { API, renderQuotaWithAmount, showError } from '../../helpers';

const { Text, Title } = Typography;

const formatDiscount = (discount, t) => {
  const value = Number(discount || 1);
  if (!Number.isFinite(value) || value <= 0) {
    return t('{{discount}}折', { discount: 10 });
  }
  const discountValue = Number((value * 10).toFixed(2));
  return t('{{discount}}折', { discount: discountValue });
};

const getTierDiscounts = (tier, t) => {
  const discounts = [];
  if (tier.discount_all_groups) {
    discounts.push(
      `${t('全部分组')} ${formatDiscount(tier.all_group_discount, t)}`,
    );
  }
  (tier.group_discounts || []).forEach((item) => {
    if (!item.group) return;
    discounts.push(`${item.group} ${formatDiscount(item.discount, t)}`);
  });
  return discounts;
};

const getTierBestDiscount = (tier) => {
  const values = [];
  if (tier.discount_all_groups) {
    values.push(Number(tier.all_group_discount || 1));
  }
  (tier.group_discounts || []).forEach((item) => {
    values.push(Number(item.discount || 1));
  });
  const validValues = values.filter(
    (value) => Number.isFinite(value) && value > 0 && value <= 1,
  );
  if (validValues.length === 0) return 1;
  return Math.min(...validValues);
};

const decorateTier = (tier) => {
  const bestDiscount = getTierBestDiscount(tier);
  const discountDepth = Math.max(0, 1 - bestDiscount);
  return {
    ...tier,
    bestDiscount,
    discountDepth,
    filledSteps:
      discountDepth > 0 ? Math.min(4, Math.ceil(discountDepth * 5)) : 0,
    lift: Math.min(18, Math.round(discountDepth * 36)),
  };
};

const Membership = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [data, setData] = useState({
    enabled: false,
    tiers: [],
    current: {},
    next_tier: null,
    has_next_tier: false,
  });

  const loadMembership = async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/membership/self');
      const { success, message, data } = res.data;
      if (success) {
        setData(data || {});
      } else {
        showError(message);
      }
    } catch (error) {
      showError(t('加载失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadMembership();
  }, []);

  const current = data.current || {};
  const tiers = useMemo(
    () =>
      [...(data.tiers || [])]
        .sort((a, b) => a.threshold_amount - b.threshold_amount)
        .map(decorateTier),
    [data.tiers],
  );
  const maxTierLift = useMemo(
    () => Math.max(0, ...tiers.map((tier) => tier.lift || 0)),
    [tiers],
  );
  const nextTier = data.has_next_tier ? data.next_tier : null;
  const currentTier = current.tier_name || t('无');
  const cumulativeAmount = Number(current.cumulative_amount || 0);
  const nextPercent =
    nextTier && Number(nextTier.threshold_amount) > 0
      ? Math.min(
          100,
          (cumulativeAmount / Number(nextTier.threshold_amount)) * 100,
        )
      : 100;

  return (
    <div className='mt-[60px] px-2'>
      <Skeleton
        loading={loading}
        active
        placeholder={<Skeleton.Paragraph rows={8} />}
      >
        {!data.enabled ? (
          <Card>
            <Empty
              image={
                <IllustrationNoResult style={{ width: 150, height: 150 }} />
              }
              darkModeImage={
                <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
              }
              description={t('会员中心未开启')}
              style={{ padding: 30 }}
            />
          </Card>
        ) : (
          <div className='space-y-3'>
            <Card>
              <div className='flex flex-col gap-4 md:flex-row md:items-center md:justify-between'>
                <div className='flex items-center gap-3'>
                  <Avatar color='orange' size='medium'>
                    <Crown size={20} />
                  </Avatar>
                  <div>
                    <Title heading={4} className='m-0'>
                      {t('会员中心')}
                    </Title>
                    <Text type='secondary'>
                      {t('会员等级按账号累计充值金额解锁')}
                    </Text>
                  </div>
                </div>
                <Space wrap>
                  <Tag
                    color={current.tier_id ? 'yellow' : 'grey'}
                    shape='circle'
                  >
                    {currentTier}
                  </Tag>
                  <Tag color='white' shape='circle'>
                    {t('累计充值')} {renderQuotaWithAmount(cumulativeAmount)}
                  </Tag>
                </Space>
              </div>
              {nextTier && (
                <div className='mt-4'>
                  <div className='mb-2 flex items-center justify-between'>
                    <Text type='secondary' size='small'>
                      {t('下一等级')}：{nextTier.name}
                    </Text>
                    <Text type='secondary' size='small'>
                      {renderQuotaWithAmount(nextTier.threshold_amount)}
                    </Text>
                  </div>
                  <Progress
                    percent={Number(nextPercent.toFixed(2))}
                    showInfo
                    stroke='var(--semi-color-primary)'
                  />
                </div>
              )}
            </Card>

            <Card>
              <div className='mb-3'>
                <Text strong>{t('会员等级')}</Text>
              </div>
              <div
                className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'
                style={{ paddingTop: maxTierLift }}
              >
                {tiers.map((tier) => {
                  const active = current.tier_id === tier.id;
                  const discounts = getTierDiscounts(tier, t);
                  const hasDiscount = tier.discountDepth > 0;
                  const shadowOpacity = 0.08 + tier.discountDepth * 0.12;
                  return (
                    <Card
                      key={tier.id}
                      bodyStyle={{ padding: 16 }}
                      style={{
                        borderColor: active
                          ? 'var(--semi-color-primary)'
                          : hasDiscount
                            ? 'rgba(245, 158, 11, 0.45)'
                            : 'var(--semi-color-border)',
                        boxShadow: hasDiscount
                          ? `0 ${8 + tier.lift / 2}px ${18 + tier.lift}px rgba(180, 83, 9, ${shadowOpacity})`
                          : undefined,
                        transform: `translateY(-${tier.lift}px)`,
                        transition:
                          'transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease',
                      }}
                    >
                      <div className='mb-3 flex items-start justify-between gap-2'>
                        <div className='flex min-w-0 items-center gap-2'>
                          <Avatar
                            size='extra-small'
                            color={hasDiscount ? 'orange' : 'grey'}
                          >
                            <Crown size={14} />
                          </Avatar>
                          <Text strong ellipsis={{ showTooltip: true }}>
                            {tier.name}
                          </Text>
                        </div>
                        <Space wrap spacing={4}>
                          <Tag
                            color={hasDiscount ? 'yellow' : 'white'}
                            shape='circle'
                          >
                            {formatDiscount(tier.bestDiscount, t)}
                          </Tag>
                          {active && (
                            <Tag color='blue' shape='circle'>
                              {t('当前等级')}
                            </Tag>
                          )}
                        </Space>
                      </div>
                      <div
                        className='mb-3 flex items-end gap-1'
                        aria-hidden='true'
                      >
                        {Array.from({ length: 4 }).map((_, step) => {
                          const filled = step < tier.filledSteps;
                          return (
                            <span
                              key={step}
                              style={{
                                width: 18,
                                height: 4 + step * 4,
                                borderRadius: 999,
                                backgroundColor: filled
                                  ? 'rgba(245, 158, 11, 0.82)'
                                  : 'var(--semi-color-fill-1)',
                              }}
                            />
                          );
                        })}
                      </div>
                      <Text type='secondary' size='small' className='block'>
                        {t('门槛')}{' '}
                        {renderQuotaWithAmount(tier.threshold_amount)}
                      </Text>
                      <div className='mt-3 flex flex-wrap gap-2'>
                        {discounts.length > 0 ? (
                          discounts.map((item) => (
                            <Tag key={item} color='white' shape='circle'>
                              {item}
                            </Tag>
                          ))
                        ) : (
                          <Tag color='grey' shape='circle'>
                            {t('暂无权益')}
                          </Tag>
                        )}
                      </div>
                    </Card>
                  );
                })}
              </div>
            </Card>
          </div>
        )}
      </Skeleton>
    </div>
  );
};

export default Membership;
