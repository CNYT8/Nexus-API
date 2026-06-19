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
import { useTranslation } from 'react-i18next';
import {
  Badge,
  Card,
  Col,
  Empty,
  Row,
  Skeleton,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../helpers';

const { Text } = Typography;

const getAdminDisplayName = (admin) =>
  admin.display_name || admin.username || `#${admin.id}`;

const MODULE_TITLE_KEYS = {
  Channels: '渠道管理',
  Models: '模型管理',
  Users: '用户管理',
  'Redeem codes': '兑换码管理',
  'Subscription Management': '订阅管理',
};

const AdminPermissionSetting = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [modules, setModules] = useState([]);
  const [admins, setAdmins] = useState([]);

  const loadPermissions = async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/admin_permissions/');
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setModules(data?.modules || []);
      setAdmins(data?.admins || []);
    } catch (error) {
      showError(error.message || error);
    } finally {
      setLoading(false);
    }
  };

  const updatePermission = async (admin, moduleKey, checked) => {
    const permissions = {
      ...admin.permissions,
      [moduleKey]: checked,
    };

    setSaving(true);
    try {
      const res = await API.put(`/api/admin_permissions/${admin.id}`, {
        permissions,
      });
      const { success, message } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setAdmins((current) =>
        current.map((item) =>
          item.id === admin.id ? { ...item, permissions } : item,
        ),
      );
      showSuccess(t('保存成功'));
    } catch (error) {
      showError(error.message || error);
    } finally {
      setSaving(false);
    }
  };

  useEffect(() => {
    loadPermissions();
  }, []);

  return (
    <Card>
      <Card.Meta
        title={t('管理员权限设置')}
        description={t('只列出普通管理员，超级管理员不受这些开关限制')}
      />

      <div style={{ marginTop: 16 }}>
        {loading ? (
          <Skeleton placeholder={<Skeleton.Paragraph rows={6} />} loading />
        ) : admins.length === 0 ? (
          <Empty title={t('暂无存在管理员')} />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            {admins.map((admin, index) => (
              <Card
                key={admin.id}
                bodyStyle={{ padding: 16 }}
                style={{ border: '1px solid var(--semi-color-border)' }}
              >
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    gap: 12,
                    marginBottom: 16,
                  }}
                >
                  <div>
                    <div
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 8,
                        fontWeight: 600,
                      }}
                    >
                      <span>
                        {index + 1}. {getAdminDisplayName(admin)}
                      </span>
                      <Badge theme='solid' type='primary'>
                        {t('管理员')}
                      </Badge>
                    </div>
                    <Text type='tertiary' size='small'>
                      {admin.email || admin.username}
                    </Text>
                  </div>
                </div>

                <Row gutter={[16, 16]}>
                  {modules.map((module) => (
                    <Col key={module.key} xs={24} sm={12} md={8}>
                      <Card
                        bodyStyle={{ padding: 14 }}
                        style={{
                          height: '100%',
                          background: 'var(--semi-color-fill-0)',
                        }}
                      >
                        <div
                          style={{
                            display: 'flex',
                            justifyContent: 'space-between',
                            gap: 12,
                          }}
                        >
                          <div style={{ minWidth: 0 }}>
                            <div style={{ fontWeight: 600, marginBottom: 4 }}>
                              {t(
                                MODULE_TITLE_KEYS[module.title_key] ||
                                  module.title_key,
                              )}
                            </div>
                            <Text type='tertiary' size='small'>
                              {t(module.description)}
                            </Text>
                          </div>
                          <Switch
                            checked={admin.permissions?.[module.key] !== false}
                            disabled={saving}
                            onChange={(checked) =>
                              updatePermission(admin, module.key, checked)
                            }
                          />
                        </div>
                      </Card>
                    </Col>
                  ))}
                </Row>
              </Card>
            ))}
          </div>
        )}
      </div>
    </Card>
  );
};

export default AdminPermissionSetting;
