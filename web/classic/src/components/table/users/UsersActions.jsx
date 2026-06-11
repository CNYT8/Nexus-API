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

import React, { useEffect, useState } from 'react';
import { Button, Select, Typography } from '@douyinfe/semi-ui';
import { API, isRoot, showError, showSuccess } from '../../../helpers';

const { Text } = Typography;

const UsersActions = ({ setShowAddUser, groupOptions, t }) => {
  const [defaultGroup, setDefaultGroup] = useState('default');
  const [groupSaving, setGroupSaving] = useState(false);
  const showDefaultGroup = isRoot();

  // Add new user
  const handleAddUser = () => {
    setShowAddUser(true);
  };

  const loadDefaultGroup = async () => {
    try {
      const res = await API.get('/api/option/');
      const { success, data } = res.data;
      if (success) {
        const item = data.find(
          (option) => option.key === 'register_setting.default_group',
        );
        if (item && item.value !== '') {
          setDefaultGroup(item.value);
        }
      }
    } catch (error) {
      showError(error.message);
    }
  };

  const handleDefaultGroupChange = async (value) => {
    const oldValue = defaultGroup;
    setDefaultGroup(value);
    setGroupSaving(true);
    try {
      const res = await API.put('/api/option/', {
        key: 'register_setting.default_group',
        value: value === 'default' ? '' : value,
      });
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('保存成功'));
      } else {
        showError(message);
        setDefaultGroup(oldValue);
      }
    } catch (error) {
      showError(error.message);
      setDefaultGroup(oldValue);
    } finally {
      setGroupSaving(false);
    }
  };

  useEffect(() => {
    if (showDefaultGroup) {
      loadDefaultGroup().then();
    }
  }, [showDefaultGroup]);

  return (
    <div className='flex flex-col md:flex-row md:items-center gap-2 w-full md:w-auto order-2 md:order-1'>
      <Button className='w-full md:w-auto' onClick={handleAddUser} size='small'>
        {t('添加用户')}
      </Button>
      {showDefaultGroup && (
        <div className='flex items-center gap-2'>
          <Text type='tertiary' className='whitespace-nowrap'>
            {t('新注册用户默认分组')}:
          </Text>
          <Select
            size='small'
            value={defaultGroup}
            optionList={groupOptions}
            onChange={handleDefaultGroupChange}
            loading={groupSaving}
            style={{ minWidth: 100 }}
          />
        </div>
      )}
    </div>
  );
};

export default UsersActions;
