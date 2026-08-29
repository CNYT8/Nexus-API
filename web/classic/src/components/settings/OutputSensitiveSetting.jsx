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
import SettingsOutputSensitiveWords from '../../pages/Setting/Operation/SettingsOutputSensitiveWords';
import { API, showError } from '../../helpers';
import { useTranslation } from 'react-i18next';

const defaultConfig = {
  enabled: false,
  action: 'truncate',
  match_percent: 20,
  patterns: [],
};

const OutputSensitiveSetting = () => {
  const { t } = useTranslation();
  const [config, setConfig] = useState(defaultConfig);
  const [loading, setLoading] = useState(false);

  const refresh = async () => {
    try {
      setLoading(true);
      const res = await API.get('/api/option/output-sensitive', {
        disableDuplicate: true,
      });
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setConfig({
        enabled: Boolean(data?.enabled),
        action: data?.action === 'error' ? 'error' : 'truncate',
        match_percent: Math.min(
          100,
          Math.max(1, Number(data?.match_percent) || 20),
        ),
        patterns: Array.isArray(data?.patterns) ? data.patterns : [],
      });
    } catch (error) {
      showError(t('加载输出敏感词设置失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  return (
    <Spin spinning={loading} size='large'>
      <Card style={{ marginTop: '10px' }}>
        <SettingsOutputSensitiveWords config={config} refresh={refresh} />
      </Card>
    </Spin>
  );
};

export default OutputSensitiveSetting;
