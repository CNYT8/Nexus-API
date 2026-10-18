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

import React, { useRef } from 'react';
import { Form, Button } from '@douyinfe/semi-ui';
import { IconSearch } from '@douyinfe/semi-icons';

const UsersFilters = ({
  formInitValues,
  setFormApi,
  searchUsers,
  loadUsers,
  activePage,
  pageSize,
  groupOptions,
  membershipEnabled,
  membershipTierOptions,
  loading,
  searching,
  t,
}) => {
  const formApiRef = useRef(null);

  const handleReset = () => {
    if (!formApiRef.current) return;
    formApiRef.current.reset();
    setTimeout(() => {
      loadUsers(1, pageSize);
    }, 100);
  };

  return (
    <Form
      initValues={formInitValues}
      getFormApi={(api) => {
        setFormApi(api);
        formApiRef.current = api;
      }}
      onSubmit={() => {
        searchUsers(1, pageSize);
      }}
      allowEmpty={true}
      autoComplete='off'
      layout='horizontal'
      trigger='change'
      stopValidateWithError={false}
      className='order-1 w-full min-w-0 xl:order-2 xl:flex-1'
    >
      <div className='flex w-full min-w-0 flex-col items-stretch gap-2 md:flex-row md:items-center'>
        <div className='relative w-full min-w-0 md:flex-1'>
          <Form.Input
            field='searchKeyword'
            prefix={<IconSearch />}
            placeholder={t('支持搜索用户的 ID、用户名、显示名称和邮箱地址')}
            showClear
            pure
            size='small'
          />
        </div>
        <div
          className={
            membershipEnabled
              ? 'grid w-full min-w-0 grid-cols-2 gap-2 md:w-64 md:flex-none 2xl:w-80'
              : 'w-full min-w-0 md:w-36 md:flex-none 2xl:w-44'
          }
        >
          <Form.Select
            field='searchGroup'
            placeholder={t('选择分组')}
            optionList={groupOptions}
            onChange={(value) => {
              // Group change triggers automatic search
              setTimeout(() => {
                searchUsers(1, pageSize);
              }, 100);
            }}
            className='w-full'
            showClear
            pure
            size='small'
          />
          {membershipEnabled && (
            <Form.Select
              field='searchMembershipTier'
              placeholder={t('会员阶级')}
              optionList={membershipTierOptions}
              onChange={() => {
                setTimeout(() => {
                  searchUsers(1, pageSize);
                }, 100);
              }}
              className='w-full'
              showClear
              pure
              size='small'
            />
          )}
        </div>
        <div className='flex w-full min-w-0 gap-2 md:w-auto md:flex-none'>
          <Button
            type='tertiary'
            htmlType='submit'
            loading={loading || searching}
            className='min-w-0 flex-1 md:flex-initial md:w-auto'
            size='small'
          >
            {t('查询')}
          </Button>
          <Button
            type='tertiary'
            onClick={handleReset}
            className='min-w-0 flex-1 md:flex-initial md:w-auto'
            size='small'
          >
            {t('重置')}
          </Button>
        </div>
      </div>
    </Form>
  );
};

export default UsersFilters;
