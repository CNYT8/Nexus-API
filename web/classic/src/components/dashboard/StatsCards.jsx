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

import React from 'react';
import { Card, Skeleton, Tag } from '@douyinfe/semi-ui';
import { VChart } from '@visactor/react-vchart';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

const StatsCards = ({
  groupedStatsData,
  loading,
  getTrendSpec,
  CARD_PROPS,
  CHART_CONFIG,
  chartThemeKey = 'light',
}) => {
  const navigate = useNavigate();
  const { t } = useTranslation();

  return (
    <div className='mb-4'>
      <div className='grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4'>
        {groupedStatsData.map((group, idx) => (
          <Card
            key={idx}
            {...CARD_PROPS}
            className='nexus-stat-card nexus-reveal !rounded-2xl w-full'
            style={{ animationDelay: `${idx * 40}ms` }}
            title={group.title}
          >
            <div className='space-y-4'>
              {group.items.map((item, itemIdx) => (
                <div
                  key={itemIdx}
                  className={`nexus-stat-row flex flex-wrap items-center justify-between gap-x-3 gap-y-1 ${
                    item.onClick
                      ? 'nexus-pressable nexus-hover-fill cursor-pointer'
                      : ''
                  }`}
                  onClick={item.onClick}
                >
                  <div className='nexus-stat-primary flex items-center min-w-0'>
                    <span
                      className='nexus-stat-icon mr-3'
                      style={{
                        backgroundColor: `rgba(var(--semi-${item.avatarColor}-5), 0.12)`,
                        color: `rgba(var(--semi-${item.avatarColor}-6), 1)`,
                      }}
                    >
                      {item.icon}
                    </span>
                    <div className='min-w-0'>
                      <div className='nexus-stat-label'>{item.title}</div>
                      <div className='nexus-stat-value'>
                        <Skeleton
                          loading={loading}
                          active
                          placeholder={
                            <Skeleton.Paragraph
                              active
                              rows={1}
                              style={{ width: '65px' }}
                            />
                          }
                        >
                          {item.value}
                        </Skeleton>
                      </div>
                    </div>
                  </div>
                  {item.title === t('当前余额') ? (
                    <Tag
                      color='white'
                      shape='circle'
                      size='large'
                      onClick={(e) => {
                        e.stopPropagation();
                        navigate('/console/topup');
                      }}
                    >
                      {t('充值')}
                    </Tag>
                  ) : (
                    item.trendData &&
                    item.trendData.length > 0 && (
                      <div className='nexus-stat-trend hidden md:block w-24 h-10'>
                        <VChart
                          key={`dashboard-stat-${chartThemeKey}-${idx}-${itemIdx}`}
                          spec={getTrendSpec(item.trendData, item.trendColor)}
                          option={CHART_CONFIG}
                        />
                      </div>
                    )
                  )}
                </div>
              ))}
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
};

export default StatsCards;
