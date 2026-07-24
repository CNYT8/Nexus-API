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

import React from 'react';
import { Skeleton } from '@douyinfe/semi-ui';

const TicketListSkeleton = ({ rows = 4, compact = false }) => (
  <div className='space-y-2' aria-hidden='true'>
    {Array.from({ length: rows }, (_, index) => (
      <div
        key={index}
        className={`flex items-center justify-between gap-4 rounded-lg border border-semi-color-border ${compact ? 'px-3 py-3' : 'px-4 py-3'}`}
      >
        <div className='min-w-0 flex-1'>
          <div className='flex items-center gap-2'>
            <Skeleton
              loading
              active
              placeholder={
                <Skeleton.Title
                  style={{ width: 52 + (index % 2) * 12, height: 16 }}
                />
              }
            />
            <Skeleton
              loading
              active
              placeholder={
                <Skeleton.Title
                  style={{ width: 86 + (index % 3) * 14, height: 14 }}
                />
              }
            />
          </div>
          <div className='mt-2'>
            <Skeleton
              loading
              active
              placeholder={
                <Skeleton.Title
                  style={{ width: 126 + (index % 2) * 18, height: 12 }}
                />
              }
            />
          </div>
        </div>
        <Skeleton
          loading
          active
          placeholder={
            <Skeleton.Title
              style={{ width: 58, height: 24, borderRadius: 9999 }}
            />
          }
        />
      </div>
    ))}
  </div>
);

export default TicketListSkeleton;
