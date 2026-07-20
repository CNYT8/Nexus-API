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
import { Button } from '@douyinfe/semi-ui';

const RedemptionsActions = ({
  selectedKeys,
  setEditingRedemption,
  setShowEdit,
  batchCopyRedemptions,
  batchDeleteSelectedRedemptions,
  clearInvalidRedemptions,
  t,
}) => {
  // Add new redemption code
  const handleAddRedemption = () => {
    setEditingRedemption({
      id: undefined,
    });
    setShowEdit(true);
  };

  return (
    <div className='flex flex-wrap gap-2 w-full md:w-auto order-2 md:order-1'>
      <Button
        type='primary'
        className='flex-1 md:flex-initial'
        onClick={handleAddRedemption}
        size='small'
      >
        {t('添加兑换码')}
      </Button>

      <Button
        type='tertiary'
        className='flex-1 md:flex-initial'
        onClick={batchCopyRedemptions}
        size='small'
      >
        {t('复制所选兑换码到剪贴板')}
      </Button>

      <Button
        type='danger'
        className='flex-1 md:flex-initial'
        onClick={batchDeleteSelectedRedemptions}
        disabled={selectedKeys.length === 0}
        size='small'
      >
        {t('批量删除')} ({selectedKeys.length})
      </Button>

      <Button
        type='danger'
        className='w-full md:w-auto'
        onClick={clearInvalidRedemptions}
        size='small'
      >
        {t('清除失效兑换码')}
      </Button>
    </div>
  );
};

export default RedemptionsActions;
