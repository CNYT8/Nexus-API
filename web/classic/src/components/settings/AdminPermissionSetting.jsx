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
  Button,
  Card,
  Col,
  Empty,
  Modal,
  Row,
  Skeleton,
  Switch,
  Table,
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
  'Ticket Management': '工单管理',
};

const AdminPermissionSetting = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [modules, setModules] = useState([]);
  const [admins, setAdmins] = useState([]);
  const [selectedAdminId, setSelectedAdminId] = useState(null);

  const selectedAdmin =
    admins.find((admin) => admin.id === selectedAdminId) || null;

  const getEnabledCount = (admin, nextPermissions = admin.permissions) =>
    modules.filter((module) => nextPermissions?.[module.key] !== false).length;

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

    if (!checked && getEnabledCount(admin, permissions) === 0) {
      showError(t('至少需要保留一个管理员权限'));
      return;
    }

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

  const columns = [
    {
      title: t('管理员'),
      dataIndex: 'username',
      render: (_, admin, index) => (
        <div style={{ minWidth: 0 }}>
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
      ),
    },
    {
      title: t('权限数量'),
      width: 120,
      render: (_, admin) => `${getEnabledCount(admin)}/${modules.length}`,
    },
    {
      title: t('操作'),
      width: 140,
      render: (_, admin) => (
        <Button
          theme='solid'
          type='primary'
          onClick={() => setSelectedAdminId(admin.id)}
        >
          {t('管理权限')}
        </Button>
      ),
    },
  ];

  return (
    <Card style={{ marginTop: '10px' }}>
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
          <Table
            columns={columns}
            dataSource={admins}
            rowKey='id'
            pagination={false}
            size='small'
          />
        )}
      </div>

      <Modal
        title={t('管理员权限设置')}
        visible={!!selectedAdmin}
        footer={null}
        size='full-width'
        bodyStyle={{ maxHeight: 'calc(100vh - 120px)', overflowY: 'auto' }}
        onCancel={() => setSelectedAdminId(null)}
      >
        {selectedAdmin && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: 12,
              }}
            >
              <div>
                <div style={{ fontWeight: 600 }}>
                  {getAdminDisplayName(selectedAdmin)}
                </div>
                <Text type='tertiary' size='small'>
                  {selectedAdmin.email || selectedAdmin.username}
                </Text>
              </div>
              <Badge theme='light' type='primary'>
                {getEnabledCount(selectedAdmin)}/{modules.length}
              </Badge>
            </div>

            <Row gutter={[12, 12]}>
              {modules.map((module) => {
                const checked =
                  selectedAdmin.permissions?.[module.key] !== false;
                const enabledCount = getEnabledCount(selectedAdmin);
                return (
                  <Col key={module.key} xs={24} sm={12} lg={8}>
                    <div
                      style={{
                        height: '100%',
                        padding: 12,
                        border: '1px solid var(--semi-color-border)',
                        borderRadius: 8,
                        background: 'var(--semi-color-fill-0)',
                      }}
                    >
                      <div
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          alignItems: 'flex-start',
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
                          checked={checked}
                          disabled={saving || (checked && enabledCount <= 1)}
                          onChange={(nextChecked) =>
                            updatePermission(
                              selectedAdmin,
                              module.key,
                              nextChecked,
                            )
                          }
                        />
                      </div>
                    </div>
                  </Col>
                );
              })}
            </Row>
          </div>
        )}
      </Modal>
    </Card>
  );
};

export default AdminPermissionSetting;
