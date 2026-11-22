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

import React, { useCallback, useContext, useEffect, useRef, useState } from 'react';
import { AxiosError, CanceledError } from 'axios';
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
import { UserContext } from '../../context/User';
import { currentSecurityOwner } from './personal/security/securityManagement';

const { Text } = Typography;

const getAdminDisplayName = (admin) =>
  admin.display_name || admin.username || `#${admin.id}`;

const MODULE_LABELS = {
  channel: { title: '渠道管理', description: '配置上游服务商和路由。' },
  models: { title: '模型管理', description: '管理模型可见性、定价、分组、供应商和部署。' },
  user: { title: '用户管理', description: '管理用户账号和角色。' },
  redemption: { title: '兑换码管理', description: '创建和查看邀请码或额度兑换码。' },
  subscription: { title: '订阅管理', description: '管理订阅套餐和用户订阅。' },
  ticket: { title: '工单管理', description: '查看并回复用户支持工单。' },
};

const moduleEnabled = (permissions, module) =>
  permissions?.[module.key] ?? module.enabled;

const actionAllowed = (permissions, module, action) => {
  const enabled = moduleEnabled(permissions, module);
  return enabled && (permissions?.[`${module.key}:${action}`] ?? enabled);
};

async function permissionRequest(config, signal, owner) {
  const assertOwner = () => {
    if (signal.aborted || !owner || currentSecurityOwner() !== owner) {
      throw new CanceledError('Admin permission request owner changed');
    }
  };
  assertOwner();
  const transforms = API.defaults.transformRequest;
  const response = await API.request({
    ...config,
    signal,
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
    validateStatus: () => true,
    transformRequest: [
      (data, headers) => {
        assertOwner();
        headers.set('New-API-User', String(owner));
        return data;
      },
      ...(Array.isArray(transforms) ? transforms : transforms ? [transforms] : []),
    ],
  });
  assertOwner();
  if (response.status < 200 || response.status >= 300) {
    throw new AxiosError('Admin permission request failed', undefined, response.config, response.request, response);
  }
  return response.data;
}

const AdminPermissionSetting = () => {
  const [userState] = useContext(UserContext);
  const owner = userState.user?.id;
  return <AdminPermissionContent key={owner ?? 'guest'} owner={owner} />;
};

const AdminPermissionContent = ({ owner }) => {
  const { t } = useTranslation();
  const lifetime = useRef(null);
  const locked = useRef(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [modules, setModules] = useState([]);
  const [admins, setAdmins] = useState([]);
  const [selectedAdminId, setSelectedAdminId] = useState(null);

  const selectedAdmin =
    admins.find((admin) => admin.id === selectedAdminId) || null;

  const isCurrent = useCallback((controller) =>
    !!controller && lifetime.current === controller && !controller.signal.aborted &&
    !!owner && currentSecurityOwner() === owner, [owner]);

  const getEnabledCount = (admin, nextPermissions = admin.permissions) =>
    modules.filter((module) =>
      actionAllowed(nextPermissions, module, 'read') ||
      actionAllowed(nextPermissions, module, 'write'),
    ).length;

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    locked.current = false;
    setSaving(false);
    setLoading(true);
    const load = async () => {
      try {
        const { success, message, data } = await permissionRequest(
          { method: 'get', url: '/api/admin_permissions/' }, controller.signal, owner,
        );
        if (!isCurrent(controller)) return;
        if (!success) {
          showError(message || t('加载失败，请重试'));
          return;
        }
        if (!Array.isArray(data?.modules) || !Array.isArray(data?.admins)) {
          throw new Error(t('加载失败，请重试'));
        }
        setModules(data.modules);
        setAdmins(data.admins);
      } catch (error) {
        if (isCurrent(controller)) showError(t('加载失败，请重试'));
      } finally {
        if (isCurrent(controller)) setLoading(false);
      }
    };
    void load();
    return () => controller.abort();
  }, [owner, isCurrent, t]);

  const updatePermission = async (admin, moduleKey, checked) => {
    const controller = lifetime.current;
    if (!isCurrent(controller) || locked.current || loading) return;
    const permissions = { ...admin.permissions, [moduleKey]: checked };
    if (getEnabledCount(admin, permissions) === 0) {
      showError(t('至少需要保留一个管理员权限'));
      return;
    }
    locked.current = true;
    setSaving(true);
    try {
      const { success, message, data } = await permissionRequest({
        method: 'put', url: `/api/admin_permissions/${admin.id}`, data: { permissions },
      }, controller.signal, owner);
      if (!isCurrent(controller)) return;
      if (!success) {
        showError(message || t('保存失败'));
        return;
      }
      if (data?.id !== admin.id || !data.permissions) {
        throw new Error(t('保存失败，请重试'));
      }
      setAdmins((current) => current.map((item) => item.id === admin.id ? data : item));
      showSuccess(t('保存成功'));
    } catch (error) {
      if (isCurrent(controller)) showError(t('保存失败，请重试'));
    } finally {
      if (isCurrent(controller)) {
        locked.current = false;
        setSaving(false);
      }
    }
  };

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

            <Text type='tertiary' size='small'>
              {t('模块关闭时读写均不可用；未单独配置的读写权限继承模块开关，写入不包含读取。')}
            </Text>
            <Row gutter={[12, 12]}>
              {modules.map((module) => {
                const checked = moduleEnabled(selectedAdmin.permissions, module);
                const labels = MODULE_LABELS[module.key];
                const wouldDisableLastModule = getEnabledCount(selectedAdmin, {
                  ...selectedAdmin.permissions, [module.key]: false,
                }) === 0;
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
                            {t(labels?.title ?? '未知权限模块')}
                          </div>
                          <Text type='tertiary' size='small'>
                            {t(labels?.description ?? '请以服务器授权规则为准')}
                          </Text>
                        </div>
                      </div>
                      <div className='mt-3 flex items-center justify-between gap-2'>
                        <Text size='small'>{t('模块开关')}</Text>
                        <Switch
                          aria-label={t('模块开关')}
                          checked={checked}
                          disabled={saving || loading || (checked && wouldDisableLastModule)}
                          onChange={(nextChecked) => updatePermission(selectedAdmin, module.key, nextChecked)}
                        />
                      </div>
                      {['read', 'write'].map((action) => {
                        const key = `${module.key}:${action}`;
                        const allowed = actionAllowed(selectedAdmin.permissions, module, action);
                        const wouldDisableLastAction = getEnabledCount(selectedAdmin, {
                          ...selectedAdmin.permissions, [key]: false,
                        }) === 0;
                        return (
                          <div key={key} className='mt-2 flex items-center justify-between gap-2'>
                            <Text size='small'>{action === 'read' ? t('读取权限') : t('写入权限')}</Text>
                            <Switch
                              aria-label={action === 'read' ? t('读取权限') : t('写入权限')}
                              checked={allowed}
                              disabled={saving || loading || !checked || (allowed && wouldDisableLastAction)}
                              onChange={(nextChecked) => updatePermission(selectedAdmin, key, nextChecked)}
                            />
                          </div>
                        );
                      })}
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
