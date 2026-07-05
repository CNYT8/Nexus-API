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

const formatDiscount = (discount) => {
  const value = Number(discount || 1);
  if (!Number.isFinite(value) || value <= 0) return '1.00';
  return value.toFixed(2);
};

const getTierDiscounts = (tier, t) => {
  const discounts = [];
  if (tier.discount_all_groups) {
    discounts.push(
      `${t('全部分组')} ${formatDiscount(tier.all_group_discount)}`,
    );
  }
  (tier.group_discounts || []).forEach((item) => {
    if (!item.group) return;
    discounts.push(`${item.group} ${formatDiscount(item.discount)}`);
  });
  return discounts;
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
      [...(data.tiers || [])].sort(
        (a, b) => a.threshold_amount - b.threshold_amount,
      ),
    [data.tiers],
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
              <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'>
                {tiers.map((tier) => {
                  const active = current.tier_id === tier.id;
                  const discounts = getTierDiscounts(tier, t);
                  return (
                    <Card
                      key={tier.id}
                      bodyStyle={{ padding: 16 }}
                      style={{
                        borderColor: active
                          ? 'var(--semi-color-primary)'
                          : 'var(--semi-color-border)',
                      }}
                    >
                      <div className='mb-2 flex items-center justify-between gap-2'>
                        <Text strong>{tier.name}</Text>
                        {active && (
                          <Tag color='blue' shape='circle'>
                            {t('当前等级')}
                          </Tag>
                        )}
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
