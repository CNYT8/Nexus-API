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

import React, { useContext, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Form,
  InputNumber,
  Spin,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../helpers';
import { StatusContext } from '../../context/Status';

const { Text } = Typography;

const TicketSetting = () => {
  const { t } = useTranslation();
  const [statusState, statusDispatch] = useContext(StatusContext);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [settings, setSettings] = useState({
    enabled: true,
    admin_manage_enabled: true,
    admin_can_close: true,
    max_content_length: 4000,
  });

  const loadSettings = async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/tickets/settings');
      if (res.data.success) {
        setSettings((current) => ({ ...current, ...res.data.data }));
      } else {
        showError(res.data.message || t('加载失败，请重试'));
      }
    } catch (error) {
      showError(error.message || t('加载失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadSettings();
  }, []);

  const updateSetting = (key, value) => {
    setSettings((current) => ({ ...current, [key]: value }));
  };

  const saveSettings = async () => {
    setSaving(true);
    try {
      const res = await API.put('/api/tickets/settings', settings);
      if (!res.data.success) {
        showError(res.data.message || t('保存失败，请重试'));
        return;
      }
      setSettings(res.data.data);
      statusDispatch({
        type: 'set',
        payload: {
          ...(statusState?.status || {}),
          ticket_enabled: res.data.data.enabled,
          ticket_admin_manage_enabled:
            res.data.data.admin_manage_enabled,
        },
      });
      showSuccess(t('保存成功'));
    } catch (error) {
      showError(error.message || t('保存失败，请重试'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Spin spinning={loading}>
      <Card style={{ marginTop: 10 }}>
        <Form.Section
          text={t('工单设置')}
          extraText={t('配置工单中心和管理员处理权限')}
        >
          <div className='space-y-4'>
            <div className='flex items-center justify-between gap-4 border-b border-semi-color-border py-3'>
              <div>
                <Text strong>{t('启用工单中心')}</Text>
                <div>
                  <Text type='tertiary' size='small'>
                    {t('关闭后用户无法创建、查看或回复工单')}
                  </Text>
                </div>
              </div>
              <Switch
                checked={settings.enabled}
                onChange={(value) => updateSetting('enabled', value)}
              />
            </div>
            <div className='flex items-center justify-between gap-4 border-b border-semi-color-border py-3'>
              <div>
                <Text strong>{t('允许管理员管理工单')}</Text>
                <div>
                  <Text type='tertiary' size='small'>
                    {t('管理员仍需在管理员权限设置中拥有工单权限')}
                  </Text>
                </div>
              </div>
              <Switch
                checked={settings.admin_manage_enabled}
                onChange={(value) =>
                  updateSetting('admin_manage_enabled', value)
                }
              />
            </div>
            <div className='flex items-center justify-between gap-4 border-b border-semi-color-border py-3'>
              <div>
                <Text strong>{t('允许管理员关闭工单')}</Text>
                <div>
                  <Text type='tertiary' size='small'>
                    {t('超级管理员始终可以关闭和重新打开工单')}
                  </Text>
                </div>
              </div>
              <Switch
                checked={settings.admin_can_close}
                disabled={!settings.admin_manage_enabled}
                onChange={(value) => updateSetting('admin_can_close', value)}
              />
            </div>
            <div className='flex items-center justify-between gap-4 py-3'>
              <div>
                <Text strong>{t('单条内容最大长度')}</Text>
                <div>
                  <Text type='tertiary' size='small'>
                    {t('适用于新建工单和后续回复')}
                  </Text>
                </div>
              </div>
              <InputNumber
                min={100}
                max={20000}
                step={100}
                value={settings.max_content_length}
                suffix={t('字符')}
                onChange={(value) =>
                  updateSetting('max_content_length', Number(value) || 4000)
                }
                style={{ width: 180 }}
              />
            </div>
          </div>
          <div className='mt-5 flex justify-end'>
            <Button
              theme='solid'
              type='primary'
              loading={saving}
              onClick={saveSettings}
            >
              {t('保存设置')}
            </Button>
          </div>
        </Form.Section>
      </Card>
    </Spin>
  );
};

export default TicketSetting;
