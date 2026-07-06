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
import { Card, Spin } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import SettingsErrorMask from '../../pages/Setting/ErrorMask/SettingsErrorMask';
import { API, showError, toBoolean } from '../../helpers';

const ErrorMaskSetting = () => {
  const { t } = useTranslation();
  let [inputs, setInputs] = useState({
    'error_mask_setting.enabled': false,
    'error_mask_setting.rules': '',
  });

  let [loading, setLoading] = useState(false);

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      let newInputs = {};
      data.forEach((item) => {
        if (typeof inputs[item.key] === 'boolean') {
          newInputs[item.key] = toBoolean(item.value);
        } else {
          newInputs[item.key] = item.value;
        }
      });
      setInputs(newInputs);
    } else {
      showError(message);
    }
  };

  async function onRefresh() {
    try {
      setLoading(true);
      await getOptions();
    } catch (error) {
      showError(t('刷新失败'));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    onRefresh();
  }, []);

  return (
    <>
      <Spin spinning={loading} size='large'>
        <Card style={{ marginTop: '10px' }}>
          <SettingsErrorMask options={inputs} refresh={onRefresh} />
        </Card>
      </Spin>
    </>
  );
};

export default ErrorMaskSetting;
